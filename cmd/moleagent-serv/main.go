package main

import (
	"context"
	"crypto/tls"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"moleAgent_Serv/internal/api"
	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/config"
	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/logging"
	"moleAgent_Serv/internal/mqtt"
	"moleAgent_Serv/internal/node"
	"moleAgent_Serv/internal/storage"
	"moleAgent_Serv/internal/tunnel"
)

func main() {
	configPath := flag.String("config", "", "配置文件路径 (YAML)，默认查找顺序: config.yaml → configs/config.yaml")
	nodeToken := flag.String("nodetoken", "", "节点认证令牌")
	flag.Parse()

	// 确定配置文件路径：命令行 > config.yaml > configs/config.yaml > 无配置启动
	resolved := *configPath
	if resolved == "" {
		if _, err := os.Stat("config.yaml"); err == nil {
			resolved = "config.yaml"
		} else if _, err := os.Stat("configs/config.yaml"); err == nil {
			resolved = "configs/config.yaml"
		}
	}

	// 加载配置
	cfg, err := config.Load(resolved)
	if err != nil {
		slog.Error("Failed to load config", "error", err)
		os.Exit(1)
	}

	// 初始化日志
	logging.Init(cfg.Logging)

	slog.Info("moleAgent_Serv starting...")

	// 初始化数据库
	if err := storage.Init(cfg.Database); err != nil {
		slog.Error("Failed to init database", "error", err)
		os.Exit(1)
	}
	defer storage.Close()

	// 优雅关闭
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		slog.Info("Received signal, shutting down...", "signal", sig.String())
		cancel()
	}()

	// --- 依赖注入 ---
	db := storage.DB()
	userRepo := storage.NewUserRepo()
	roleRepo := storage.NewRoleRepo()

	jwtMgr := auth.NewJWTManager(cfg.Auth.JWTSecret, parseExpiry(cfg.Auth.JWTExpiry))
	rbacEngine := auth.NewRBACEngine(db, roleRepo)
	authSvc := auth.NewAuthService(jwtMgr, userRepo, roleRepo, rbacEngine, db)
	authMW := auth.NewAuthMiddleware(jwtMgr, rbacEngine, func() string {
		return storage.GetGlobalAccessKey()
	})

	nodeMgr := node.NewShardedNodeManager(256)
	nodeRepo := storage.NewNodeRepo()

	// --- 节点管理器 + 健康检查 ---
	go node.StartHealthCheck(ctx, nodeMgr)

	// --- 网关服务（HTTP 隧道）---（在 controlSrv 之前创建，因为注册回调需要引用）
	gateway := tunnel.NewTunnelGateway(nodeMgr, cfg.Server.MaxConcurrent)
	tunnel.HyphenRouting = cfg.Server.Gateway.HyphenRouting

	// --- 控制端口 ---
	token := *nodeToken
	if token == "" {
		token = os.Getenv("MA_NODE_TOKEN")
	}
	if token == "" {
		token = "default-node-token-change-me"
	}
	var tlsConfig *tls.Config
	if cfg.Server.TLS.Enabled {
		cert, err := tls.LoadX509KeyPair(cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile)
		if err != nil {
			slog.Error("Failed to load TLS certificate", "error", err)
			os.Exit(1)
		}
		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
	}
	controlSrv := tunnel.NewControlServer(cfg.Server.ControlPort, nodeMgr, token, tlsConfig, nodeRepo)
	controlSrv.SetOnNodeChange(func() {
		gateway.RebuildIndex(context.Background())
	})
	go func() {
		if err := controlSrv.Start(ctx); err != nil {
			slog.Error("Control server error", "error", err)
			cancel()
		}
	}()

	// --- MQTT Broker ---
	var mqttBroker *mqtt.EmbeddedBroker
	if cfg.MQTT.Enabled {
		mqttBroker = mqtt.NewEmbeddedBroker(cfg.MQTT.TCPPort, cfg.MQTT.WSPort, authSvc, rbacEngine)
		if err := mqttBroker.Start(ctx); err != nil {
			slog.Error("MQTT broker error", "error", err)
			cancel()
		}
	}

	// --- HTTP API 服务（静态文件 + API） ---
	apiRouter := buildAPIRouter(authMW, authSvc, nodeMgr, cfg, mqttBroker, gateway, controlSrv, userRepo, roleRepo, rbacEngine, nodeRepo)
	adminDir, _ := os.Getwd()
	adminFS := http.StripPrefix("/admin", http.FileServer(http.Dir(filepath.Join(adminDir, "admin"))))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/admin") {
			adminFS.ServeHTTP(w, r)
			return
		}
		apiRouter.ServeHTTP(w, r)
	})
	apiSrv := &http.Server{
		Addr:    cfg.Server.APIPort,
		Handler: handler,
	}
	go func() {
		slog.Info("API server listening", "addr", cfg.Server.APIPort)
		if err := apiSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("API server error", "error", err)
			cancel()
		}
	}()

	gatewaySrv := &http.Server{
		Addr:    cfg.Server.GatewayPort,
		Handler: gateway,
	}
	go func() {
		slog.Info("Gateway server listening", "addr", cfg.Server.GatewayPort)
		if err := gatewaySrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Gateway server error", "error", err)
			cancel()
		}
	}()

	slog.Info("moleAgent_Serv started",
		"controlPort", cfg.Server.ControlPort,
		"gatewayPort", cfg.Server.GatewayPort,
		"apiPort", cfg.Server.APIPort,
	)

	// 等待关闭信号
	<-ctx.Done()

	// 优雅关闭 HTTP 服务
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	apiSrv.Shutdown(shutdownCtx)
	gatewaySrv.Shutdown(shutdownCtx)
	gateway.Stop()

	slog.Info("moleAgent_Serv stopped gracefully")
}

func buildAPIRouter(
	mw *auth.AuthMiddleware,
	authSvc *auth.AuthService,
	nodeMgr *node.ShardedNodeManager,
	cfg *config.Config,
	mqttBroker *mqtt.EmbeddedBroker,
	gateway *tunnel.TunnelGateway,
	controlSrv *tunnel.ControlServer,
	userRepo core.UserRepo,
	roleRepo core.RoleRepo,
	rbacEngine *auth.RBACEngine,
	nodeRepo core.NodeRepo,
) http.Handler {
	router := api.NewRouter(mw)

	// Handlers
	authH := api.NewAuthHandler(authSvc)
	userH := api.NewUserHandler(userRepo, rbacEngine, cfg.Auth.BcryptCost)
	roleH := api.NewRoleHandler(roleRepo)
	nodeH := api.NewNodeHandler(nodeMgr, nodeRepo)
	tunnelH := api.NewTunnelHandler(nodeMgr, gateway, controlSrv, nodeRepo)
	mqttH := api.NewMQTTHandler(mqttBroker)
	sysH := api.NewSystemHandler(storage.DB(), cfg)

	// === 公开端点 ===
	router.RegisterPublic("POST", "/api/v1/auth/login", authH.Login)
	router.RegisterPublic("GET", "/api/v1/health", sysH.Health)

	// === 需要认证的端点 ===
	router.RegisterAuth("POST", "/api/v1/auth/logout", authH.Logout)
	router.RegisterAuth("POST", "/api/v1/auth/refresh", authH.Refresh)
	router.RegisterAuth("GET", "/api/v1/auth/me", authH.Me)
	router.RegisterAuth("POST", "/api/v1/auth/changepass", authH.ChangePass)

	// === 用户管理（RBAC） ===
	router.Register("GET", "/api/v1/users", userH.List, "users", "read")
	router.Register("POST", "/api/v1/users", userH.Create, "users", "write")
	router.Register("GET", "/api/v1/users/", userH.Get, "users", "read")
	router.Register("PUT", "/api/v1/users/", userH.Update, "users", "write")
	router.Register("DELETE", "/api/v1/users/", userH.Delete, "users", "delete")
	router.Register("POST", "/api/v1/users/", userH.AssignRole, "users", "admin")

	// === 角色管理 ===
	router.Register("GET", "/api/v1/roles", roleH.List, "roles", "read")
	router.Register("POST", "/api/v1/roles", roleH.Create, "roles", "write")
	router.Register("GET", "/api/v1/roles/", roleH.Get, "roles", "read")
	router.Register("PUT", "/api/v1/roles/", roleH.Update, "roles", "write")
	router.Register("DELETE", "/api/v1/roles/", roleH.Delete, "roles", "delete")
	router.Register("GET", "/api/v1/permissions", roleH.ListPermissions, "roles", "read")

	// === 节点管理 ===
	router.Register("GET", "/api/v1/nodes", nodeH.List, "nodes", "read")
	router.Register("POST", "/api/v1/nodes", nodeH.Create, "nodes", "write")
	router.Register("GET", "/api/v1/nodes/", nodeH.Get, "nodes", "read")
	router.Register("PUT", "/api/v1/nodes/", nodeH.Update, "nodes", "write")
	router.Register("DELETE", "/api/v1/nodes/", nodeH.Delete, "nodes", "delete")

	// === 隧道管理 ===
	router.Register("GET", "/api/v1/tunnels", tunnelH.List, "tunnels", "read")
	router.Register("GET", "/api/v1/tunnels/stats", tunnelH.Stats, "tunnels", "read")
	router.Register("POST", "/api/v1/tunnels", tunnelH.Create, "tunnels", "write")
	router.Register("DELETE", "/api/v1/tunnels/", tunnelH.Delete, "tunnels", "delete")

	// === MQTT 管理 ===
	router.Register("GET", "/api/v1/mqtt/clients", mqttH.Clients, "mqtt", "read")
	router.Register("GET", "/api/v1/mqtt/topics", mqttH.Topics, "mqtt", "read")
	router.Register("GET", "/api/v1/mqtt/stats", mqttH.Stats, "mqtt", "read")
	router.Register("POST", "/api/v1/mqtt/publish", mqttH.Publish, "mqtt", "write")
	router.Register("GET", "/api/v1/mqtt/health", mqttH.Health, "mqtt", "read")

	// === 系统管理 ===
	router.Register("GET", "/api/v1/metrics", sysH.Metrics, "system", "read")
	router.Register("GET", "/api/v1/config", sysH.GetConfig, "system", "admin")
	router.Register("GET", "/api/v1/accesskey", sysH.GetAccessKey, "accesskey", "read")
	router.Register("PUT", "/api/v1/accesskey", sysH.SetAccessKey, "accesskey", "admin")
	router.Register("DELETE", "/api/v1/accesskey", sysH.DeleteAccessKey, "accesskey", "admin")

	return router.Build()
}

func parseExpiry(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 24 * time.Hour
	}
	return d
}
