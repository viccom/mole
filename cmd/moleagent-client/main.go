package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	_ "net/http/pprof" // 注册 /debug/pprof；通过 -debug 端口暴露，主服务卡死时可抓 goroutine 堆栈
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	selfupdate "github.com/viccom/go-selfupdater"

	moleAgent_client "moleAgent_client"
	"moleAgent_client/internal/builtin"
	"moleAgent_client/internal/version"
)

func main() {
	// 检测子命令
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-tunnels":
			os.Exit(handleTunnelsCmd(os.Args[2:]))
		case "-check-update":
			os.Exit(handleCheckUpdate())
		case "-self-update":
			os.Exit(handleSelfUpdate())
		}
	}

	cfgFlag := flag.String("config", "", "配置文件路径 (JSON)")
	serverFlag := flag.String("server", "", "服务端地址 (默认 127.0.0.1:9981)")
	tokenFlag := flag.String("token", "", "节点认证令牌")
	nodeIDFlag := flag.String("id", "", "节点 ID (默认自动生成)")
	nameFlag := flag.String("name", "", "节点名称")
	tlsFlag := flag.Bool("tls", false, "启用 TLS")
	transportFlag := flag.String("transport", "", "传输协议: tcp, ws, kcp")
	httpFlag := flag.String("http", "", "内置 HTTP 端口 (默认 127.0.0.1:59870, off 关闭)")
	verboseFlag := flag.Bool("verbose", false, "启用 DEBUG 级日志（webssh 会话细节等）")
	tunnelsFlag := flag.Bool("tunnels", false, "隧道管理子命令 (见: moleagent-client -tunnels -h)")
	versionFlag := flag.Bool("version", false, "打印版本信息并退出")
	debugFlag := flag.String("debug", "", "pprof 调试 HTTP 监听地址 (默认 off，如 127.0.0.1:59871)")
	flag.CommandLine.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: moleagent-client [options]

Options:
  -config <path>       配置文件路径 (JSON)
  -server <addr>       服务端地址
  -token <token>       节点认证令牌
  -id <nodeid>         节点 ID (默认自动生成)
  -name <name>         节点名称
  -tls                 启用 TLS
  -transport <proto>   传输协议 (tcp, ws, kcp)
  -http <port>         内置 HTTP 端口 (默认 127.0.0.1:59870, off 关闭)
  -tunnels             隧道管理子命令 (见: moleagent-client -tunnels -h)
  -check-update        检测新版本
  -self-update         自动升级并重启

Examples:
  moleagent-client -config client.json
  moleagent-client -server localhost:9981 -token mytoken
  moleagent-client -check-update
  moleagent-client -self-update
`)
	}
	flag.Parse()
	if *versionFlag {
		fmt.Println(version.VersionString())
		os.Exit(0)
	}
	_ = *tunnelsFlag // 仅用于帮助信息展示

	// 加载配置
	var cfg *moleAgent_client.Config
	if *cfgFlag != "" {
		var err error
		cfg, err = moleAgent_client.LoadConfigFile(*cfgFlag)
		if err != nil {
			log.Fatalf("Failed to load config: %v", err)
		}
	} else {
		cfg = moleAgent_client.DefaultConfig()
	}

	// 命令行参数覆盖
	if *serverFlag != "" {
		cfg.ServerAddr = *serverFlag
	}
	if *tokenFlag != "" {
		cfg.Token = *tokenFlag
	}
	if *nodeIDFlag != "" {
		cfg.NodeID = *nodeIDFlag
	}
	if *nameFlag != "" {
		cfg.NodeName = *nameFlag
	}
	if *tlsFlag {
		cfg.UseTLS = true
	}
	if *transportFlag != "" {
		cfg.Transport = *transportFlag
	}
	if *httpFlag != "" {
		cfg.BuiltinHTTP = *httpFlag
	}

	// 创建客户端
	if *verboseFlag {
		moleAgent_client.EnableDebugLogging()
	}

	client, err := moleAgent_client.New(cfg)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	// 注册事件日志
	client.OnEvent("", func(ev moleAgent_client.Event) {
		log.Printf("[Event] %s %v", ev.Type, ev.Data)
	})

	log.Printf("moleAgent_client %s starting...", version.VersionString())
	log.Printf("  Node ID:  %s", client.NodeID())
	log.Printf("  Server:   %s (transport=%s, TLS=%v)", cfg.ServerAddr, cfg.Transport, cfg.UseTLS)
	log.Printf("  Tunnels:  %d (will be loaded from server after connect)", len(client.Tunnels()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 启动客户端
	runDone := make(chan struct{})
	go func() {
		if err := client.Run(ctx); err != nil {
			log.Printf("Client Run error: %v", err)
		}
		close(runDone)
	}()

	// 启动内置 HTTP 服务
	if cfg.BuiltinHTTP != "off" {
		go func() {
			if err := builtin.StartHTTPServer(cfg.BuiltinHTTP, client); err != nil {
				log.Fatalf("Built-in HTTP server error: %v", err)
			}
		}()
	}

	// 启动 pprof 调试服务（独立端口 + DefaultServeMux，不污染内置 API）
	// 主服务卡死时仍可经此端口抓 goroutine 堆栈定位 busy-loop
	if *debugFlag != "" {
		go func() {
			log.Printf("Debug (pprof) server listening on %s", *debugFlag)
			if err := http.ListenAndServe(*debugFlag, nil); err != nil {
				log.Printf("Debug (pprof) server error: %v", err)
			}
		}()
	}

	// 等待退出信号，或客户端自行退出（服务端远程 restart 触发）
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigCh:
		log.Printf("Received %s, shutting down...", sig)
	case <-runDone:
	}

	if client.RestartRequested() {
		// 服务端请求重启：退出进程，由外部进程管理器（systemd/docker 等）拉起完成重启
		log.Println("Restart requested by server, exiting for supervisor relaunch")
		cancel()
		client.Close()
		os.Exit(0)
	}

	cancel()
	client.Close()
	log.Println("Stopped.")
}

// handleTunnelsCmd 处理 -tunnels 子命令，向已运行的客户端 HTTP API 发送请求
func handleTunnelsCmd(args []string) int {
	tunFlag := flag.NewFlagSet("tunnels", flag.ExitOnError)
	listFlag := tunFlag.Bool("list", false, "列出所有隧道")
	addFlag := tunFlag.String("add", "", "添加隧道 (格式: name:type:target)")
	delFlag := tunFlag.String("del", "", "删除隧道 (按名称)")
	domainFlag := tunFlag.String("domain", "", "HTTP 隧道域名 (配合 --add 使用)")
	portFlag := tunFlag.Int("port", 0, "TCP/UDP 隧道监听端口 (配合 --add 使用)")
	addrFlag := tunFlag.String("addr", "127.0.0.1:59870", "客户端 HTTP API 地址")
	tunFlag.SetOutput(os.Stdout)
	tunFlag.Usage = func() {
		fmt.Fprintf(os.Stdout, `Usage: moleagent-client -tunnels [options]

Manage tunnels via the client's built-in HTTP API.

Options:
  --list              列出所有隧道
  --add name:type:target   添加隧道 (例: fnlist:http:127.0.0.1:8080)
  --del <name>        删除隧道
  --addr <host:port>  API 地址 (默认 127.0.0.1:59870)
  --domain <domain>   HTTP 隧道域名 (配合 --add 使用)
  --port <port>       TCP/UDP 隧道监听端口 (配合 --add 使用)

Examples:
  moleagent-client -tunnels --list
  moleagent-client -tunnels --add fnlist:http:192.168.1.100:8080 --domain fnlist.example.com
  moleagent-client -tunnels --add ssh:tcp:127.0.0.1:22 --port 2222
  moleagent-client -tunnels --del fnlist
`)
	}
	tunFlag.Parse(args)

	baseURL := "http://" + *addrFlag

	switch {
	case *listFlag:
		return listTunnels(baseURL)
	case *addFlag != "":
		return addTunnel(baseURL, *addFlag, *domainFlag, *portFlag)
	case *delFlag != "":
		return deleteTunnel(baseURL, *delFlag)
	default:
		tunFlag.Usage()
		return 1
	}
}

func listTunnels(baseURL string) int {
	resp, err := http.Get(baseURL + "/api/tunnels")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot connect to client at %s: %v\n", baseURL, err)
		return 1
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var tunnels []struct {
		Name       string `json:"name"`
		Type       string `json:"type"`
		Target     string `json:"target"`
		Domain     string `json:"domain,omitempty"`
		ListenPort int    `json:"listen_port,omitempty"`
	}
	if err := json.Unmarshal(body, &tunnels); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		fmt.Println(string(body))
		return 1
	}

	if len(tunnels) == 0 {
		fmt.Println("No tunnels configured.")
		return 0
	}

	fmt.Printf("%-15s %-8s %-35s %-25s %s\n", "NAME", "TYPE", "TARGET", "DOMAIN", "PORT")
	fmt.Println(strings.Repeat("-", 90))
	for _, t := range tunnels {
		portStr := ""
		if t.ListenPort > 0 {
			portStr = fmt.Sprintf("%d", t.ListenPort)
		}
		fmt.Printf("%-15s %-8s %-35s %-25s %s\n", t.Name, t.Type, t.Target, t.Domain, portStr)
	}
	return 0
}

func addTunnel(baseURL, spec, domain string, port int) int {
	parts := strings.SplitN(spec, ":", 3)
	if len(parts) != 3 {
		fmt.Fprintf(os.Stderr, "Error: invalid tunnel spec %q, expected name:type:target\n", spec)
		fmt.Fprintln(os.Stderr, "Example: web:http:127.0.0.1:8080")
		return 1
	}

	name, typ, target := parts[0], parts[1], parts[2]
	if name == "" || typ == "" || target == "" {
		fmt.Fprintf(os.Stderr, "Error: name, type, and target are all required\n")
		return 1
	}

	body := map[string]any{
		"name":   name,
		"type":   typ,
		"target": target,
	}
	if domain != "" {
		body["domain"] = domain
	}
	if port > 0 {
		body["listen_port"] = port
	}

	data, _ := json.Marshal(body)
	resp, err := http.Post(baseURL+"/api/tunnels", "application/json", bytes.NewReader(data))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot connect to client at %s: %v\n", baseURL, err)
		return 1
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error (%d): %s\n", resp.StatusCode, string(respBody))
		return 1
	}

	fmt.Printf("Tunnel %q added successfully.\n", name)
	return 0
}

func deleteTunnel(baseURL, name string) int {
	req, _ := http.NewRequest("DELETE", baseURL+"/api/tunnels/"+name, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot connect to client at %s: %v\n", baseURL, err)
		return 1
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error (%d): %s\n", resp.StatusCode, string(respBody))
		return 1
	}

	fmt.Printf("Tunnel %q deleted successfully.\n", name)
	return 0
}

const defaultUpdateURL = "https://fs.px.metme.top/app/molec/latest.json"

func handleCheckUpdate() int {
	ver := version.Version
	if ver == "dev" || ver == "" {
		fmt.Fprintln(os.Stderr, "Error: cannot check update for dev build")
		return 1
	}

	src := selfupdate.NewHTTPSource(defaultUpdateURL)
	u := selfupdate.New(src, ver)

	release, err := u.Check()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	if release == nil {
		fmt.Printf("moleAgent_client %s is up to date (%s/%s)\n", ver, runtime.GOOS, runtime.GOARCH)
		return 0
	}

	fmt.Printf("Update available: %s → %s\n", ver, release.Version)
	asset, err := release.AssetForCurrentPlatform()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: %v\n", err)
		return 0
	}
	fmt.Printf("  URL:    %s\n", asset.URL)
	fmt.Printf("  SHA256: %s\n", asset.SHA256)
	fmt.Printf("  Size:   %d bytes\n", asset.Size)
	fmt.Printf("\nRun 'moleagent-client -self-update' to upgrade.\n")
	return 0
}

func handleSelfUpdate() int {
	ver := version.Version
	if ver == "dev" || ver == "" {
		fmt.Fprintln(os.Stderr, "Error: cannot update dev build")
		return 1
	}

	src := selfupdate.NewHTTPSource(defaultUpdateURL)
	u := selfupdate.New(src, ver,
		selfupdate.WithLogger(func(format string, args ...any) {
			fmt.Printf(format+"\n", args...)
		}),
	)

	release, err := u.Check()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	if release == nil {
		fmt.Printf("moleAgent_client %s is already up to date.\n", ver)
		return 0
	}

	fmt.Printf("Updating %s → %s ...\n", ver, release.Version)
	if err := u.UpdateAndRestart(release); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	return 0
}
