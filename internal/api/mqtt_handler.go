package api

import (
	"encoding/json"
	"net/http"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/mqtt"
)

type MQTTHandler struct {
	broker *mqtt.EmbeddedBroker
}

func NewMQTTHandler(broker *mqtt.EmbeddedBroker) *MQTTHandler {
	return &MQTTHandler{broker: broker}
}

func (h *MQTTHandler) Clients(w http.ResponseWriter, r *http.Request) {
	if h.broker == nil {
		ResponseOK(w, []core.MQTTClientInfo{})
		return
	}
	ResponseOK(w, h.broker.GetClients())
}

func (h *MQTTHandler) Topics(w http.ResponseWriter, r *http.Request) {
	if h.broker == nil {
		ResponseOK(w, []any{})
		return
	}
	ResponseOK(w, mqtt.GetSubscriptionsInfo(h.broker.GetServer()))
}

func (h *MQTTHandler) Stats(w http.ResponseWriter, r *http.Request) {
	if h.broker == nil {
		ResponseOK(w, core.MQTTStats{})
		return
	}
	ResponseOK(w, h.broker.GetStats())
}

func (h *MQTTHandler) Publish(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Topic   string `json:"topic"`
		Payload string `json:"payload"`
		QoS     byte   `json:"qos"`
		Retain  bool   `json:"retain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	if req.Topic == "" {
		ResponseError(w, http.StatusBadRequest, 400, "Topic required")
		return
	}
	if h.broker == nil {
		ResponseError(w, http.StatusServiceUnavailable, 503, "MQTT broker not enabled")
		return
	}
	// SEC-09：Publish 走 broker inline client，不经 aclHook——发布前必须按
	// aclHook 同规则校验调用者对该 topic 的归属（admin 放行；其余身份须为
	// 节点归属者且持 mqtt:write；access_key 对齐 aclHook 语义按无 RBAC
	// 记录处理，同样被拒）
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		ResponseError(w, http.StatusUnauthorized, 401, "Unauthorized")
		return
	}
	if !h.broker.CheckTopicACL(claims.UserID, req.Topic, true) {
		ResponseError(w, http.StatusForbidden, 403, "Forbidden: topic not permitted")
		return
	}
	if err := h.broker.Publish(req.Topic, []byte(req.Payload), req.Retain, req.QoS); err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Publish failed")
		return
	}
	ResponseOK(w, "published")
}

func (h *MQTTHandler) Health(w http.ResponseWriter, r *http.Request) {
	if h.broker == nil {
		ResponseOK(w, map[string]string{"status": "disabled"})
		return
	}
	ResponseOK(w, map[string]string{"status": "ok"})
}
