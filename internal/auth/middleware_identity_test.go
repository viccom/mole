package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nalgeon/redka"
	_ "modernc.org/sqlite"

	"moleAgent_Serv/internal/core"
)

func TestRequirePermission_UsesUserIDWhenUsernameDiffers(t *testing.T) {
	db, err := redka.Open(":memory:", &redka.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatalf("failed to open in-memory redka: %v", err)
	}
	defer db.Close()

	roleRepo := &mockRoleRepo{
		roles: map[string]*core.Role{
			"operator": {
				ID:   "operator",
				Name: "operator",
				Permissions: []core.Permission{
					{Resource: "mqtt", Action: "read"},
				},
			},
		},
	}

	userID := "uuid-op-003"
	if _, err := db.Hash().Set("user_roles", userID, `["operator"]`); err != nil {
		t.Fatalf("failed to set user_roles: %v", err)
	}

	jwtMgr := NewJWTManager("test-secret", time.Hour)
	token, _, err := jwtMgr.GenerateToken(&core.Claims{
		UserID:   userID,
		Username: "operator-display-name",
	})
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	rbac := NewRBACEngine(db, roleRepo)
	mw := NewAuthMiddleware(jwtMgr, rbac, func() string { return "" })

	called := false
	handler := mw.RequirePermission("mqtt", "read")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/mqtt", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Fatal("expected protected handler to be called")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
