//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"voxeltoad/internal/config"
)

// Non-streaming responses are OpenAI-shaped: object, choices[].message.content,
// and usage with prompt/completion/total tokens.
func TestCompat_NonStreamingShape(t *testing.T) {
	h := NewHarness(t)
	up := jsonUpstream("hi there", 11, 7, nil)
	defer up.Close()
	h.AddProvider("p1", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{Provider: "p1", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "p1"})
	h.SeedKey("sk-c", "acme", "team", "key_c", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	resp := h.Chat("sk-c", "chat", false)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var chat struct {
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &chat); err != nil {
		t.Fatalf("decode: %v; body=%s", err, body)
	}
	if len(chat.Choices) != 1 || chat.Choices[0].Message.Content != "hi there" {
		t.Errorf("choices = %+v", chat.Choices)
	}
	if chat.Usage.PromptTokens != 11 || chat.Usage.CompletionTokens != 7 || chat.Usage.TotalTokens != 18 {
		t.Errorf("usage = %+v, want 11/7/18", chat.Usage)
	}
}

// Streaming responses relay SSE chunks and carry the trailing usage chunk; the
// gateway emits a [DONE] sentinel. Billing settles from the aggregated usage.
func TestCompat_StreamingSSE(t *testing.T) {
	h := NewHarness(t)
	up := sseUpstream("streamed", 9, 3, nil)
	defer up.Close()
	h.AddProvider("p1", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{Provider: "p1", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "p1"})
	h.SeedKey("sk-s", "acme", "team", "key_s", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	resp := h.Chat("sk-s", "chat", true)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	var content strings.Builder
	var sawDone, sawUsage bool
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			sawDone = true
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				TotalTokens int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		for _, ch := range chunk.Choices {
			content.WriteString(ch.Delta.Content)
		}
		if chunk.Usage != nil && chunk.Usage.TotalTokens == 12 {
			sawUsage = true
		}
	}
	if content.String() != "streamed" {
		t.Errorf("stitched content = %q, want streamed", content.String())
	}
	if !sawUsage {
		t.Error("did not see trailing usage chunk (total 12)")
	}
	if !sawDone {
		t.Error("did not see [DONE] sentinel")
	}

	// Billing settled from the streamed usage: cost = 9*1_000_000/1_000_000 + 3*2_000_000/1_000_000
	// = 9 + 6 = 15 micro-units. Poll (async settle is synchronous, but usage
	// record is async).
	waitFor(t, 2*time.Second, func() bool {
		return h.Balance("tenant:acme") == 1_000_000-15
	}, "quota settled to 999985 after streaming")
}

// TestCompat_StreamingTTFT asserts the first SSE content chunk reaches the
// client shortly after it's produced upstream, not only after the whole
// stream (including a deliberately delayed trailing chunk) has completed.
// This guards against the forwarding layer buffering the response instead of
// flushing each chunk immediately (design/e2e.md pitfall: "SSE 缓冲攒包" — a
// regression here inflates TTFT to the full stream duration).
func TestCompat_StreamingTTFT(t *testing.T) {
	h := NewHarness(t)
	const chunkDelay = 500 * time.Millisecond
	up := sseUpstreamDelayed("streamed", 9, 3, nil, chunkDelay)
	defer up.Close()
	h.AddProvider("p1", up.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 2_000_000, config.ModelUpstream{Provider: "p1", UpstreamModel: "gpt-4o"})
	h.AddRoute("chat", "priority", config.RouteProvider{Name: "p1"})
	h.SeedKey("sk-ttft", "acme", "team", "key_ttft", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	start := time.Now()
	resp := h.Chat("sk-ttft", "chat", true)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}

	sc := bufio.NewScanner(resp.Body)
	var ttft time.Duration
	gotContent := false
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				gotContent = true
			}
		}
		if gotContent {
			ttft = time.Since(start)
			break
		}
	}
	if !gotContent {
		t.Fatal("did not receive the first content chunk")
	}
	// The trailing usage/[DONE] chunks are held back by chunkDelay upstream; a
	// correctly-flushing forwarder delivers the first content chunk well before
	// that delay elapses. An unflushed/buffered forwarder would only deliver
	// bytes once the whole response (>= chunkDelay) is available.
	if ttft >= chunkDelay {
		t.Errorf("TTFT = %v, want well under the upstream chunk delay (%v) — forwarder may be buffering instead of flushing", ttft, chunkDelay)
	}
}

// Errors use the OpenAI-compatible envelope: {"error":{"message","type"}}.
func TestCompat_ErrorEnvelope(t *testing.T) {
	h := NewHarness(t)
	h.setupChatModel(t)
	h.SyncConfig()

	// Unknown key → 401 with the error envelope.
	resp := h.Chat("sk-unknown", "chat", false)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v; body=%s", err, body)
	}
	if env.Error.Type == "" || env.Error.Message == "" {
		t.Errorf("error envelope missing fields: %s", body)
	}
}
