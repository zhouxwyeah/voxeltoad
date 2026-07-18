package desktopapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sync"
)

// KeyState holds the desktop gateway's current plaintext API key in memory.
// The desktop is a local single-user tool and the key is already printed to
// the startup log, so keeping it in memory for the "copy key" affordances
// (UI button, Wails menu) adds no new exposure. The state is shared between
// the API server and the Wails menu so a rotation via one is immediately
// visible to the other. After a restart with a rotated key the plaintext is
// unknowable from the stored hash — callers must tolerate known=false.
type KeyState struct {
	mu        sync.RWMutex
	plaintext string
	known     bool
}

// NewKeyState seeds the state with the startup key (empty = unknown).
func NewKeyState(plaintext string) *KeyState {
	return &KeyState{plaintext: plaintext, known: plaintext != ""}
}

// Get returns the current plaintext and whether it is known.
func (k *KeyState) Get() (string, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.plaintext, k.known
}

// Set replaces the current plaintext (post-rotation).
func (k *KeyState) Set(plaintext string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.plaintext = plaintext
	k.known = plaintext != ""
}

// handleGetAPIKey reports the key id + the plaintext when known (the UI shows
// it for copying; this is a loopback-only single-user API).
func (s *Server) handleGetAPIKey(w http.ResponseWriter, r *http.Request) {
	if s.keyState == nil {
		writeError(w, http.StatusServiceUnavailable, "api key state not available")
		return
	}
	out := map[string]any{"key_id": "default"}
	if plaintext, known := s.keyState.Get(); known {
		out["key"] = plaintext
		out["plaintext_known"] = true
	} else {
		out["plaintext_known"] = false
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRotateAPIKey generates a fresh random key, swaps the stored hash, and
// returns the plaintext exactly once. The old key stops working immediately;
// agents must be reconfigured (the UI says so next to the button).
func (s *Server) handleRotateAPIKey(w http.ResponseWriter, r *http.Request) {
	if s.keyState == nil || s.keys == nil {
		writeError(w, http.StatusServiceUnavailable, "api key state not available")
		return
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		writeError(w, http.StatusInternalServerError, "generate key: "+err.Error())
		return
	}
	plaintext := "dt-sk-" + hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(plaintext))
	if err := s.keys.RotateDefaultKey(r.Context(), hex.EncodeToString(sum[:])); err != nil {
		writeError(w, http.StatusInternalServerError, "rotate key: "+err.Error())
		return
	}
	s.keyState.Set(plaintext)
	writeJSON(w, http.StatusOK, map[string]any{
		"key_id":  "default",
		"key":     plaintext,
		"warning": "旧密钥已立即失效，请更新各 Agent 的 Authorization 配置",
	})
}
