package desktopapi

import (
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
)

// RevealConfigFile reveals the YAML config file in the platform's file manager:
// macOS Finder (`open -R`), Windows Explorer (`explorer.exe /select,`), or the
// Linux desktop's default folder opener (`xdg-open` on the parent dir). Shared
// by the HTTP endpoint below (sidebar button) and the macOS menu item in
// deploy/desktop/app.
func RevealConfigFile(path string) error {
	if path == "" {
		return fmt.Errorf("config path is empty")
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", path).Run()
	case "windows":
		// explorer.exe /select,<path> — backslash separators preferred by
		// Explorer. Start (not Run): explorer exits non-zero even when the
		// window opened fine, and the user only cares that it appeared.
		return exec.Command("explorer.exe", "/select,"+filepath.FromSlash(path)).Start()
	default:
		return exec.Command("xdg-open", filepath.Dir(path)).Run()
	}
}

// handleConfigReveal reveals the YAML config file in the OS file manager
// (sidebar "打开配置目录" button). Needs only configPath — no watcher — so it
// stays available whenever the server knows where the config lives.
func (s *Server) handleConfigReveal(w http.ResponseWriter, r *http.Request) {
	if s.configPath == "" {
		writeError(w, http.StatusServiceUnavailable, "config reveal disabled (no config file)")
		return
	}
	if err := RevealConfigFile(s.configPath); err != nil {
		writeError(w, http.StatusInternalServerError, "reveal config: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, envelope{Data: map[string]string{"path": s.configPath}})
}
