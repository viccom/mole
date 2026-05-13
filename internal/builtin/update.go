package builtin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sync"

	selfupdate "github.com/viccom/go-selfupdater"

	"moleAgent_client/internal/version"
)

const updateURL = "https://fs.px.metme.top/app/molec/latest.json"

type updateManager struct {
	mu     sync.Mutex
	updater *selfupdate.Updater
}

var updateMgr = &updateManager{}

func getUpdater() *selfupdate.Updater {
	updateMgr.mu.Lock()
	defer updateMgr.mu.Unlock()
	if updateMgr.updater == nil {
		ver := version.Version
		if ver == "" || ver == "dev" {
			return nil
		}
		src := selfupdate.NewHTTPSource(updateURL)
		updateMgr.updater = selfupdate.New(src, ver)
	}
	return updateMgr.updater
}

func handleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	u := getUpdater()
	if u == nil {
		json.NewEncoder(w).Encode(map[string]any{
			"error":   true,
			"message": "dev build, update not available",
			"current": version.Version,
		})
		return
	}

	release, err := u.Check()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]any{
			"error":      true,
			"message":    err.Error(),
			"current":    version.Version,
			"has_update": false,
		})
		return
	}

	if release == nil {
		json.NewEncoder(w).Encode(map[string]any{
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

	json.NewEncoder(w).Encode(resp)
}

func handleSelfUpdate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	u := getUpdater()
	if u == nil {
		json.NewEncoder(w).Encode(map[string]any{
			"error":   true,
			"message": "dev build, cannot update",
		})
		return
	}

	release, err := u.Check()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]any{
			"error":   true,
			"message": fmt.Sprintf("check failed: %v", err),
		})
		return
	}

	if release == nil {
		json.NewEncoder(w).Encode(map[string]any{
			"error":   true,
			"message": "already up to date",
			"current": version.Version,
		})
		return
	}

	fmt.Printf("self-update: %s → %s, restarting...\n", version.Version, release.Version)

	json.NewEncoder(w).Encode(map[string]any{
		"success":     true,
		"message":     "updating and restarting",
		"old_version": version.Version,
		"new_version": release.Version,
		"platform":    runtime.GOOS + "/" + runtime.GOARCH,
	})

	go func() {
		if err := u.UpdateAndRestart(release); err != nil {
			fmt.Printf("self-update failed: %v\n", err)
		}
	}()
}
