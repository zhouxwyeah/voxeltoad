package desktopapi

import (
	"net/http"
	"time"
)

// SetQuitFunc injects the process-quit hook for POST /api/v1/app/quit. The
// Wails shell wires this to its runtime Quit so the request goes through the
// normal shutdown path (OnShutdown drains the HTTP server, main's defer chain
// flushes the recorders). Nil disables the endpoint (503). It is a setter —
// not a New() param — because the Wails app does not exist yet when the
// Server is constructed.
func (s *Server) SetQuitFunc(fn func()) {
	s.quitFn = fn
}

// handleAppQuit quits the desktop application (sidebar "退出应用" button,
// replacing the native menu quit that only existed on the Windows menu bar).
// The response is written BEFORE the quit fires: quitting synchronously would
// kill the HTTP server mid-response and the webview would see a failed
// request instead of a clean goodbye.
func (s *Server) handleAppQuit(w http.ResponseWriter, r *http.Request) {
	if s.quitFn == nil {
		writeError(w, http.StatusServiceUnavailable, "quit not available")
		return
	}
	writeJSON(w, http.StatusOK, envelope{Data: map[string]string{"status": "quitting"}})
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.quitFn()
	}()
}
