//go:build e2e

// Package e2e holds end-to-end tests exercising the full path: client → gateway
// → mock (or real) upstream. Guarded by the `e2e` build tag so `go test ./...`
// stays fast and hermetic; run via `make test-e2e`. See design/e2e.md.
package e2e

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"voxeltoad/internal/config"
	"voxeltoad/test/testsupport"
)

// repoRoot resolves the repository root relative to this test file.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller")
	}
	// test/e2e/<file> -> repo root is two levels up.
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestProfileLoads(t *testing.T) {
	p, err := testsupport.LoadProfile(repoRoot(t))
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	if p.Gateway.BaseURL == "" {
		t.Error("expected gateway.base_url to be set")
	}
}

// seedRealOpenAI wires the full stack to forward to the real OpenAI API using
// the credentials from the loaded profile, and returns a ready client key. It
// registers a provider pointing at the profile's OpenAI base_url, a "gpt-4o"
// model alias + priority route over it, and a funded client key. Shared by the
// real-OpenAI tests below; both skip when the profile has no real key.
func seedRealOpenAI(t *testing.T) (h *Harness, clientKey string) {
	t.Helper()
	p, err := testsupport.LoadProfile(repoRoot(t))
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	if !p.Features.HasRealOpenAI {
		t.Skip("skipping: profile has no real OpenAI key (set one in real-providers.yaml)")
	}
	oai := p.Providers["openai"]

	h = NewHarness(t)
	// The provider key is a real secret from the profile; pass it inline via the
	// plain:// scheme (profiles are gitignored, ADR-0003).
	h.AddProvider("openai", oai.BaseURL, "plain://"+oai.APIKey)
	h.AddModel("gpt-4o", 1_000_000, 2_000_000, config.ModelUpstream{
		Provider: "openai", UpstreamModel: "gpt-4o",
	})
	h.AddRoute("gpt-4o", "priority", config.RouteProvider{Name: "openai"})

	clientKey = "sk-client-realoai"
	h.SeedKey(clientKey, "acme", "team-a", "key_realoai", nil)
	h.SetQuota("tenant:acme", 1_000_000_000)
	h.SyncConfig()
	return h, clientKey
}

// TestChatCompletion_RealOpenAI drives a non-streaming chat completion through
// the gateway to the real OpenAI API and asserts an OpenAI-compatible response
// with a non-empty assistant message and billed usage. Skipped unless the
// active profile carries a real OpenAI key.
func TestChatCompletion_RealOpenAI(t *testing.T) {
	h, key := seedRealOpenAI(t)

	resp := h.Chat(key, "gpt-4o", false)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat status = %d, want 200; body=%s", resp.StatusCode, body)
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
		t.Fatalf("decode chat response: %v; body=%s", err, body)
	}
	if len(chat.Choices) == 0 || chat.Choices[0].Message.Content == "" {
		t.Errorf("expected a non-empty assistant message, got %+v", chat.Choices)
	}
	if chat.Choices[0].Message.Role != "assistant" {
		t.Errorf("first choice role = %q, want assistant", chat.Choices[0].Message.Role)
	}
	// Real usage comes from the upstream; assert it was reported (billing basis).
	if chat.Usage.TotalTokens <= 0 {
		t.Errorf("expected positive total_tokens from upstream, got %d", chat.Usage.TotalTokens)
	}

	// The quota was funded at 1e9 micro-units; a real call must have debited it.
	if bal := h.Balance("tenant:acme"); bal >= 1_000_000_000 {
		t.Errorf("quota balance = %d, expected it to be debited below the initial 1e9", bal)
	}
}

// TestChatCompletion_RealOpenAI_Streaming drives a streaming (SSE) chat
// completion to the real OpenAI API and asserts the gateway relays
// OpenAI-compatible chunks terminated by [DONE]. Skipped without a real key.
func TestChatCompletion_RealOpenAI_Streaming(t *testing.T) {
	h, key := seedRealOpenAI(t)

	resp := h.Chat(key, "gpt-4o", true)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("stream status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	var (
		chunks  int
		content strings.Builder
		sawDone bool
		sawRole bool
		scanner = bufio.NewScanner(resp.Body)
	)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			sawDone = true
			break
		}
		chunks++
		var chunk struct {
			Choices []struct {
				Delta struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatalf("decode SSE chunk: %v; data=%s", err, data)
		}
		if len(chunk.Choices) > 0 {
			if chunk.Choices[0].Delta.Role == "assistant" {
				sawRole = true
			}
			content.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read stream: %v", err)
	}

	if chunks == 0 {
		t.Error("expected at least one SSE chunk")
	}
	if !sawRole {
		t.Error("expected the first delta to carry role=assistant")
	}
	if !sawDone {
		t.Error("expected the stream to terminate with [DONE]")
	}
	if content.Len() == 0 {
		t.Error("expected non-empty assembled content across chunks")
	}
}
