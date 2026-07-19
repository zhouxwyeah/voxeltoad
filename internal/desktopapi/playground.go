package desktopapi

import (
	"context"
	"encoding/json"
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

	// 512 (not 64) so thinking models have budget left for actual content:
	// they burn output tokens on reasoning_content FIRST, and a 64-token cap
	// was fully consumed by the reasoning trace — the probe "worked" (200 +
	// usage) yet content came back empty, which reads as a false failure.
	maxTokens := 512 // keep the probe cheap
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
	finishReason := ""
	if len(resp.Choices) > 0 {
		content = resp.Choices[0].Message.Content.Text()
		finishReason = resp.Choices[0].FinishReason
	}
	out := map[string]any{
		"content":        content,
		"provider":       result.Provider,
		"model_resolved": result.ModelResolved,
		"fallback":       result.Fallback,
		"latency_ms":     latency,
	}
	if finishReason != "" {
		out["finish_reason"] = finishReason
	}
	// When content is empty, surface the reasoning trace from the raw upstream
	// body so the user can tell "model thought but produced no reply text"
	// apart from a genuinely empty reply. The unified adapter view drops
	// reasoning_content (no slot for it), so read it from the preserved Raw.
	if content == "" {
		if rc := reasoningFromRaw(resp.Raw); rc != "" {
			out["reasoning_content"] = rc
		}
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

// reasoningFromRaw pulls choices[0].message.reasoning_content out of the raw
// upstream response body. Thinking models (DeepSeek-R1 family etc.) put their
// chain-of-thought in that field; the unified adapter view has no slot for it,
// so the playground reads it straight from the preserved Raw.
func reasoningFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var body struct {
		Choices []struct {
			Message struct {
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || len(body.Choices) == 0 {
		return ""
	}
	return body.Choices[0].Message.ReasoningContent
}
