package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	moleAgent_client "moleAgent_client"
	"moleAgent_client/internal/builtin"
)

var (
	version   = "dev"
	build     = "unknown"
	buildDate = "unknown"
)

func main() {
	cfgFlag := flag.String("config", "", "配置文件路径 (JSON)")
	serverFlag := flag.String("server", "", "服务端地址 (默认 127.0.0.1:9981)")
	tokenFlag := flag.String("token", "", "节点认证令牌")
	nodeIDFlag := flag.String("id", "", "节点 ID (默认自动生成)")
	nameFlag := flag.String("name", "", "节点名称")
	tlsFlag := flag.Bool("tls", false, "启用 TLS")
	httpFlag := flag.String("http", "", "内置 HTTP 端口 (默认 127.0.0.1:18080, off 关闭)")
	flag.Parse()

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
	if *httpFlag != "" {
		cfg.BuiltinHTTP = *httpFlag
	}

	// 创建客户端
	client, err := moleAgent_client.New(cfg)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	// 注册事件日志
	client.OnEvent("", func(ev moleAgent_client.Event) {
		log.Printf("[Event] %s %v", ev.Type, ev.Data)
	})

	log.Printf("moleAgent_client %s starting...", version)
	log.Printf("  Node ID:  %s", client.NodeID())
	log.Printf("  Server:   %s (TLS=%v)", cfg.ServerAddr, cfg.UseTLS)
	log.Printf("  Tunnels:  %d", len(client.Tunnels()))
	for _, t := range client.Tunnels() {
		log.Printf("    - %s (%s) → %s", t.Name, t.Type, t.Target)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 启动客户端
	go client.Run(ctx)

	// 启动内置 HTTP 服务
	if cfg.BuiltinHTTP != "off" {
		go func() {
			if err := builtin.StartHTTPServer(cfg.BuiltinHTTP, client); err != nil {
				log.Fatalf("Built-in HTTP server error: %v", err)
			}
		}()
	}

	// 等待退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("Received %s, shutting down...", sig)
	cancel()
	client.Close()
	log.Println("Stopped.")
}
