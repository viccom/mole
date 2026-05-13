package api

import (
	"fmt"
	"net/http"
	"runtime"
	"sync"

	selfupdate "github.com/viccom/go-selfupdater"

	"moleAgent_Serv/internal/version"
)

const updateURL = "https://fs.px.metme.top/app/moles/latest.json"

type UpdateHandler struct {
	mu      sync.Mutex
	updater *selfupdate.Updater
}

func NewUpdateHandler() *UpdateHandler {
	return &UpdateHandler{}
}

func (h *UpdateHandler) getUpdater() *selfupdate.Updater {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.updater == nil {
		ver := version.Version
		if ver == "" || ver == "dev" {
			return nil
		}
		src := selfupdate.NewHTTPSource(updateURL)
		h.updater = selfupdate.New(src, ver,
			selfupdate.WithLogger(func(format string, args ...any) {
				fmt.Printf("[update] "+format+"\n", args...)
			}),
		)
	}
	return h.updater
}

func (h *UpdateHandler) CheckUpdate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		ResponseError(w, http.StatusMethodNotAllowed, 405, "method not allowed")
		return
	}

	u := h.getUpdater()
	if u == nil {
		ResponseOK(w, map[string]any{
			"error":      true,
			"message":    "dev build",
			"current":    version.Version,
			"has_update": false,
		})
		return
	}

	release, err := u.Check()
	if err != nil {
		ResponseOK(w, map[string]any{
			"error":      true,
			"message":    err.Error(),
			"current":    version.Version,
			"has_update": false,
		})
		return
	}

	if release == nil {
		ResponseOK(w, map[string]any{
			"current":    version.Version,
			"has_update": false,
			"latest":     version.Version,
		})
		return
	}

	resp := map[string]any{
		"current":    version.Version,
		"has_update": true,
		"latest":     release.Version,
	}
	asset, err := release.AssetForCurrentPlatform()
	if err == nil {
		resp["download_url"] = asset.URL
		resp["download_size"] = asset.Size
	}
	ResponseOK(w, resp)
}

func (h *UpdateHandler) SelfUpdate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		ResponseError(w, http.StatusMethodNotAllowed, 405, "method not allowed")
		return
	}

	u := h.getUpdater()
	if u == nil {
		ResponseOK(w, map[string]any{
			"error":   true,
			"message": "dev build, cannot update",
		})
		return
	}

	progress := u.Progress()
	snapshot := progress.Snapshot()
	if active, _ := snapshot["active"].(bool); active {
		ResponseOK(w, map[string]any{
			"error":   true,
			"message": "update already in progress",
		})
		return
	}

	release, err := u.Check()
	if err != nil {
		ResponseOK(w, map[string]any{
			"error":   true,
			"message": fmt.Sprintf("check failed: %v", err),
		})
		return
	}
	if release == nil {
		ResponseOK(w, map[string]any{
			"error":   true,
			"message": "already up to date",
			"current": version.Version,
		})
		return
	}

	ResponseOK(w, map[string]any{
		"accepted":    true,
		"message":     "update started",
		"new_version": release.Version,
	})

	go func() {
		fmt.Printf("[update] %s → %s (%s/%s)\n", version.Version, release.Version, runtime.GOOS, runtime.GOARCH)
		if err := u.UpdateAndRestart(release); err != nil {
			fmt.Printf("[update] failed: %v\n", err)
		}
	}()
}

func (h *UpdateHandler) UpdateProgress(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		ResponseError(w, http.StatusMethodNotAllowed, 405, "method not allowed")
		return
	}
	u := h.getUpdater()
	if u == nil {
		ResponseOK(w, map[string]any{"active": false})
		return
	}
	ResponseOK(w, u.Progress().Snapshot())
}
