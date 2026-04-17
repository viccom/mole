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
	"os"
	"os/signal"
	"strings"
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
	// 检测 -tunnels 子命令
	if len(os.Args) > 1 && os.Args[1] == "-tunnels" {
		os.Exit(handleTunnelsCmd(os.Args[2:]))
	}

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
	log.Printf("  Tunnels:  %d (will be loaded from server after connect)", len(client.Tunnels()))

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

// handleTunnelsCmd 处理 -tunnels 子命令，向已运行的客户端 HTTP API 发送请求
func handleTunnelsCmd(args []string) int {
	tunFlag := flag.NewFlagSet("tunnels", flag.ExitOnError)
	listFlag := tunFlag.Bool("list", false, "列出所有隧道")
	addFlag := tunFlag.String("add", "", "添加隧道 (格式: name:type:target)")
	delFlag := tunFlag.String("del", "", "删除隧道 (按名称)")
	domainFlag := tunFlag.String("domain", "", "HTTP 隧道域名 (配合 --add 使用)")
	portFlag := tunFlag.Int("port", 0, "TCP/UDP 隧道监听端口 (配合 --add 使用)")
	addrFlag := tunFlag.String("addr", "127.0.0.1:18080", "客户端 HTTP API 地址")
	tunFlag.SetOutput(os.Stdout)
	tunFlag.Usage = func() {
		fmt.Fprintf(os.Stdout, `Usage: moleagent-client -tunnels [options]

Manage tunnels via the client's built-in HTTP API.

Options:
  --list              列出所有隧道
  --add name:type:target   添加隧道 (例: fnlist:http:127.0.0.1:8080)
  --del <name>        删除隧道
  --addr <host:port>  API 地址 (默认 127.0.0.1:18080)
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
		fmt.Fprintln(os.Stderr, "Example: web:http:http://127.0.0.1:8080")
		return 1
	}

	name, typ, target := parts[0], parts[1], parts[2]
	if name == "" || typ == "" || target == "" {
		fmt.Fprintf(os.Stderr, "Error: name, type, and target are all required\n")
		return 1
	}

	body := map[string]any{
		"name": name,
		"type": typ,
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
