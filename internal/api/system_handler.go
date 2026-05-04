package api

import (
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"github.com/nalgeon/redka"

	"moleAgent_Serv/internal/config"
	"moleAgent_Serv/internal/version"
)

var startTime = time.Now()

type SystemHandler struct {
	db  *redka.DB
	cfg *config.Config
}

func NewSystemHandler(db *redka.DB, cfg *config.Config) *SystemHandler {
	return &SystemHandler{db: db, cfg: cfg}
}

func (h *SystemHandler) Version(w http.ResponseWriter, r *http.Request) {
	ResponseOK(w, version.GetSystemInfo())
}

func (h *SystemHandler) Health(w http.ResponseWriter, r *http.Request) {
	ResponseOK(w, map[string]any{
		"status":         "healthy",
		"uptime_seconds": int64(time.Since(startTime).Seconds()),
		"timestamp":      time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *SystemHandler) Metrics(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	ResponseOK(w, map[string]any{
		"cpu_num":         runtime.NumCPU(),
		"goroutines":      runtime.NumGoroutine(),
		"memory_alloc_mb": m.Alloc / 1024 / 1024,
		"memory_sys_mb":   m.Sys / 1024 / 1024,
		"uptime_seconds":  int64(time.Since(startTime).Seconds()),
	})
}

func (h *SystemHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	// Return sanitized runtime config (no secrets)
	ResponseOK(w, map[string]any{
		"server": map[string]any{
			"control_port":   h.cfg.Server.ControlPort,
			"gateway_port":   h.cfg.Server.GatewayPort,
			"api_port":       h.cfg.Server.APIPort,
			"max_nodes":      h.cfg.Server.MaxNodes,
			"max_concurrent": h.cfg.Server.MaxConcurrent,
			"tls_enabled":    h.cfg.Server.TLS.Enabled,
		},
		"mqtt": map[string]any{
			"enabled":  h.cfg.MQTT.Enabled,
			"tcp_port": h.cfg.MQTT.TCPPort,
			"ws_port":  h.cfg.MQTT.WSPort,
		},
		"auth": map[string]any{
			"jwt_expiry":  h.cfg.Auth.JWTExpiry,
			"bcrypt_cost": h.cfg.Auth.BcryptCost,
		},
		"database": map[string]any{
			"path": h.cfg.Database.Path,
		},
		"logging": map[string]any{
			"level":  h.cfg.Logging.Level,
			"format": h.cfg.Logging.Format,
		},
	})
}

func (h *SystemHandler) GetAccessKey(w http.ResponseWriter, r *http.Request) {
	val, err := h.db.Str().Get("global_access_key")
	if err != nil || val.String() == "" {
		ResponseOK(w, map[string]any{"enabled": false})
		return
	}
	ResponseOK(w, map[string]any{"enabled": true})
}

func (h *SystemHandler) SetAccessKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	if err := h.db.Str().Set("global_access_key", req.Key); err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to set access key")
		return
	}
	ResponseOK(w, "access key updated")
}

func (h *SystemHandler) DeleteAccessKey(w http.ResponseWriter, r *http.Request) {
	_, err := h.db.Key().Delete("global_access_key")
	if err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to delete access key")
		return
	}
	ResponseOK(w, "access key disabled")
}
