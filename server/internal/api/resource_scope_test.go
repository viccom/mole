package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
)

func TestIsAdmin(t *testing.T) {
	tests := []struct {
		name   string
		claims *core.Claims
		want   bool
	}{
		{"nil claims", nil, false},
		{"no roles", &core.Claims{UserID: "u1", Roles: []string{}}, false},
		{"operator role", &core.Claims{UserID: "u1", Roles: []string{"operator"}}, false},
		{"admin role", &core.Claims{UserID: "u1", Roles: []string{"admin"}}, true},
		{"mixed roles with admin", &core.Claims{UserID: "u1", Roles: []string{"operator", "admin"}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAdmin(tt.claims); got != tt.want {
				t.Errorf("IsAdmin() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCanAccessNode(t *testing.T) {
	nodeOwnedBySelf := &core.Node{ID: "n1", OwnerUserID: "user1"}
	nodeOwnedByOther := &core.Node{ID: "n2", OwnerUserID: "user2"}

	adminClaims := &core.Claims{UserID: "user1", Roles: []string{"admin"}}
	ownerClaims := &core.Claims{UserID: "user1", Roles: []string{"operator"}}
	nonOwnerClaims := &core.Claims{UserID: "user3", Roles: []string{"operator"}}

	tests := []struct {
		name   string
		claims *core.Claims
		node   *core.Node
		want   bool
	}{
		{"nil claims", nil, nodeOwnedBySelf, false},
		{"admin any node", adminClaims, nodeOwnedByOther, true},
		{"owner", ownerClaims, nodeOwnedBySelf, true},
		{"non-owner", nonOwnerClaims, nodeOwnedByOther, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanAccessNode(tt.claims, tt.node); got != tt.want {
				t.Errorf("CanAccessNode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilterNodes(t *testing.T) {
	node1 := &core.Node{ID: "n1", OwnerUserID: "user1"}
	node2 := &core.Node{ID: "n2", OwnerUserID: "user1"}
	node3 := &core.Node{ID: "n3", OwnerUserID: "user2"}
	threeNodes := []*core.Node{node1, node2, node3}
	noNodes := []*core.Node{}

	adminClaims := &core.Claims{UserID: "admin", Roles: []string{"admin"}}
	userClaims := &core.Claims{UserID: "user1", Roles: []string{"operator"}}

	tests := []struct {
		name       string
		claims     *core.Claims
		nodes      []*core.Node
		wantCount  int
	}{
		{"nil returns empty", nil, threeNodes, 0},
		{"admin sees all", adminClaims, threeNodes, 3},
		{"user sees own", userClaims, threeNodes, 2},
		{"empty input", adminClaims, noNodes, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterNodes(tt.claims, tt.nodes)
			if len(got) != tt.wantCount {
				t.Errorf("FilterNodes() returned %d nodes, want %d", len(got), tt.wantCount)
			}
		})
	}
}

func TestCheckNodeOwnership_NoClaims_PassesThrough(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/nodes/n1", nil)
	node := &core.Node{ID: "n1", OwnerUserID: "someone_else"}

	got := checkNodeOwnership(w, r, node)

	if !got {
		t.Error("checkNodeOwnership() = false, want true (no claims should pass through)")
	}
	if w.Code != http.StatusOK {
		t.Errorf("unexpected response status: got %d, want %d (no error response should be written)", w.Code, http.StatusOK)
	}
	// Verify the response body is empty — no error JSON was written
	if w.Body.Len() != 0 {
		t.Errorf("unexpected response body written: %q", w.Body.String())
	}
}

func TestCheckNodeOwnership_AdminPasses(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/nodes/n1", nil)

	adminClaims := &core.Claims{UserID: "admin", Roles: []string{"admin"}}
	ctx := auth.SetClaims(r.Context(), adminClaims)
	r = r.WithContext(ctx)

	node := &core.Node{ID: "n1", OwnerUserID: "someone_else"}

	got := checkNodeOwnership(w, r, node)

	if !got {
		t.Error("checkNodeOwnership() = false for admin, want true")
	}
}

func TestCheckNodeOwnership_NonOwnerGets404(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/nodes/n1", nil)

	nonOwnerClaims := &core.Claims{UserID: "user1", Roles: []string{"operator"}}
	ctx := auth.SetClaims(r.Context(), nonOwnerClaims)
	r = r.WithContext(ctx)

	node := &core.Node{ID: "n1", OwnerUserID: "user2"}

	got := checkNodeOwnership(w, r, node)

	if got {
		t.Error("checkNodeOwnership() = true for non-owner, want false")
	}
	if w.Code != http.StatusNotFound {
		t.Errorf("response status = %d, want %d", w.Code, http.StatusNotFound)
	}
}
