package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"

	"moleAgent_client/internal/builtin"
)

type builtinHTTPServer struct {
	server   *http.Server
	listener net.Listener
}

func (s builtinHTTPServer) addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s builtinHTTPServer) close(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

func (a *App) startBuiltinHTTPServer() error {
	currentPort := a.builtinPortAddr()
	candidates := []string{currentPort}
	defaultAddr := "127.0.0.1:59870"
	if defaultAddr != "" && defaultAddr != currentPort {
		candidates = append(candidates, defaultAddr)
	}
	candidates = append(candidates, "127.0.0.1:0")

	var lastErr error
	for _, candidate := range candidates {
		server, actualAddr, err := a.openBuiltinHTTPServer(candidate)
		if err != nil {
			lastErr = err
			continue
		}

		a.serverMu.Lock()
		a.apiServer = server
		a.builtinPort = actualAddr
		a.serverMu.Unlock()

		if a.nodeMgr != nil && candidate != "127.0.0.1:0" && actualAddr != a.nodeMgr.GetBuiltinHTTP() {
			a.nodeMgr.SetBuiltinHTTP(actualAddr)
			if err := a.nodeMgr.Save(); err != nil {
				log.Printf("save built-in HTTP port error: %v", err)
			}
		}
		if candidate == "127.0.0.1:0" {
			log.Printf("built-in HTTP server fell back to temporary address %s", actualAddr)
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errString("unable to start built-in HTTP server")
	}
	return lastErr
}

func (a *App) stopBuiltinHTTPServer(ctx context.Context) error {
	a.serverMu.Lock()
	server := a.apiServer
	a.apiServer = builtinHTTPServer{}
	a.serverMu.Unlock()
	return server.close(ctx)
}

func (a *App) restartBuiltinHTTPServer() error {
	server, actualAddr, err := a.openBuiltinHTTPServer(a.builtinPortAddr())
	if err != nil {
		return err
	}

	a.serverMu.Lock()
	old := a.apiServer
	a.apiServer = server
	a.builtinPort = actualAddr
	a.serverMu.Unlock()

	return old.close(context.Background())
}

func (a *App) setBuiltinHTTPPort(port string) error {
	if port == "" || port == a.builtinPortAddr() {
		return nil
	}
	server, actualAddr, err := a.openBuiltinHTTPServer(port)
	if err != nil {
		return err
	}

	a.serverMu.Lock()
	old := a.apiServer
	a.apiServer = server
	oldPort := a.builtinPort
	a.builtinPort = actualAddr
	a.serverMu.Unlock()

	if a.nodeMgr != nil {
		a.nodeMgr.SetBuiltinHTTP(actualAddr)
		if err := a.nodeMgr.Save(); err != nil {
			a.serverMu.Lock()
			a.builtinPort = oldPort
			a.apiServer = old
			a.serverMu.Unlock()
			_ = server.close(context.Background())
			return err
		}
	}

	if err := old.close(context.Background()); err != nil {
		log.Printf("stop previous built-in HTTP server error: %v", err)
	}
	return nil
}

func (a *App) openBuiltinHTTPServer(addr string) (builtinHTTPServer, string, error) {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return builtinHTTPServer{}, "", fmt.Errorf("invalid built-in HTTP address %q", addr)
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return builtinHTTPServer{}, "", err
	}

	server := &http.Server{
		Handler: builtin.NewHandler(a.currentClient),
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("built-in HTTP server error: %v", err)
		}
	}()

	return builtinHTTPServer{
		server:   server,
		listener: listener,
	}, listener.Addr().String(), nil
}
