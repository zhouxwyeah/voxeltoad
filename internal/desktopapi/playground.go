package desktopapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"voxeltoad/internal/adapter"
)

// playgroundRequest is the POST /api/v1/playground/chat payload.
type playgroundRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// handlePlaygroundChat runs a tiny non-streaming completion through the full
// data-plane chain in-process (route resolution → provider pick → credential
// → adapter → upstream) so the user can validate a provider/model config
// without pointing an external agent at the gateway. It intentionally does
// NOT go through the HTTP data plane (no auth header needed — the caller is
// the local UI) and is not recorded in request_logs: it's a config smoke
// test, not agent traffic.
//
// Errors surface the upstream message verbatim (HTTP 502) — diagnosis is the
// whole point of the page.
func (s *Server) handlePlaygroundChat(w http.ResponseWriter, r *http.Request) {
	if s.watcher == nil {
		writeError(w, http.StatusServiceUnavailable, "dispatcher not available")
		return
	}
	var req playgroundRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}
	disp := s.watcher.Current()
	if disp == nil {
		writeError(w, http.StatusServiceUnavailable, "dispatcher not built — fix the config and reload")
		return
	}

	maxTokens := 64 // keep the probe cheap
	ureq := &adapter.UnifiedRequest{
		Model: req.Model,
		Messages: []adapter.Message{{
			Role:    "user",
			Content: adapter.NewContentText(req.Prompt),
		}},
		MaxTokens: &maxTokens,
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	start := time.Now()
	resp, result, err := disp.Forward(ctx, req.Model, ureq)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":      err.Error(),
			"provider":   result.Provider,
			"latency_ms": latency,
		})
		return
	}

	content := ""
	if len(resp.Choices) > 0 {
		content = resp.Choices[0].Message.Content.Text()
	}
	out := map[string]any{
		"content":        content,
		"provider":       result.Provider,
		"model_resolved": result.ModelResolved,
		"fallback":       result.Fallback,
		"latency_ms":     latency,
	}
	if resp.Usage != nil {
		out["usage"] = map[string]int{
			"prompt_tokens":     resp.Usage.PromptTokens,
			"completion_tokens": resp.Usage.CompletionTokens,
			"total_tokens":      resp.Usage.TotalTokens,
		}
	}
	writeJSON(w, http.StatusOK, out)
}
