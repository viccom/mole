package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"moleAgent_Serv/internal/core"
)

func TestResponseOK(t *testing.T) {
	w := httptest.NewRecorder()
	ResponseOK(w, map[string]string{"key": "value"})

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp core.ApiResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Code != 0 {
		t.Errorf("expected code 0, got %d", resp.Code)
	}
	if resp.Msg != "success" {
		t.Errorf("expected msg 'success', got '%s'", resp.Msg)
	}
}

func TestResponseError(t *testing.T) {
	w := httptest.NewRecorder()
	ResponseError(w, http.StatusUnauthorized, 401, "Unauthorized")

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}

	var resp core.ApiResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Code != 401 {
		t.Errorf("expected code 401, got %d", resp.Code)
	}
	if resp.Msg != "Unauthorized" {
		t.Errorf("expected msg 'Unauthorized', got '%s'", resp.Msg)
	}
}

func TestResponsePaginated(t *testing.T) {
	w := httptest.NewRecorder()
	ResponsePaginated(w, []string{"a", "b"}, 2, 1, 20)

	var resp core.ApiResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Code != 0 {
		t.Errorf("expected code 0, got %d", resp.Code)
	}
}
