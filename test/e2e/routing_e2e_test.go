//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"voxeltoad/internal/config"
)

// Priority routing: with two candidates, the first is used while healthy.
func TestRouting_PriorityPicksFirst(t *testing.T) {
	h := NewHarness(t)

	var primaryHits, backupHits int
	primary := jsonUpstream("from primary", 5, 5, &primaryHits)
	defer primary.Close()
	backup := jsonUpstream("from backup", 5, 5, &backupHits)
	defer backup.Close()

	h.AddProvider("primary", primary.URL(), "plain://k")
	h.AddProvider("backup", backup.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 1_000_000,
		config.ModelUpstream{Provider: "primary", UpstreamModel: "gpt-4o"},
		config.ModelUpstream{Provider: "backup", UpstreamModel: "gpt-4o"},
	)
	h.AddRoute("chat", "priority",
		config.RouteProvider{Name: "primary"}, config.RouteProvider{Name: "backup"})
	h.SeedKey("sk-route", "acme", "team", "key_route", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	resp := h.Chat("sk-route", "chat", false)
	content := decodeContent(t, resp)
	if content != "from primary" {
		t.Errorf("content = %q, want from primary (priority)", content)
	}
	if primaryHits != 1 || backupHits != 0 {
		t.Errorf("hits primary=%d backup=%d, want 1/0", primaryHits, backupHits)
	}
}

// Failover: the primary returns a retryable 500, the dispatcher fails over to
// the backup and the client still gets a 200.
func TestRouting_FailsOverOnUpstreamError(t *testing.T) {
	h := NewHarness(t)

	var badHits, goodHits int
	bad := failingUpstream(http.StatusInternalServerError, &badHits)
	defer bad.Close()
	good := jsonUpstream("from backup", 5, 5, &goodHits)
	defer good.Close()

	h.AddProvider("bad", bad.URL(), "plain://k")
	h.AddProvider("good", good.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 1_000_000,
		config.ModelUpstream{Provider: "bad", UpstreamModel: "gpt-4o"},
		config.ModelUpstream{Provider: "good", UpstreamModel: "gpt-4o"},
	)
	h.AddRoute("chat", "priority",
		config.RouteProvider{Name: "bad"}, config.RouteProvider{Name: "good"})
	h.SeedKey("sk-fo", "acme", "team", "key_fo", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	resp := h.Chat("sk-fo", "chat", false)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("status = %d, want 200 after failover; body=%s", resp.StatusCode, body)
	}
	content := decodeContent(t, resp)
	if content != "from backup" {
		t.Errorf("content = %q, want from backup (failed over)", content)
	}
	if badHits < 1 || goodHits != 1 {
		t.Errorf("hits bad=%d good=%d, want bad≥1 good=1", badHits, goodHits)
	}
}

// Streaming failover: the primary returns a retryable 500 before any byte is
// streamed, so the dispatcher fails over to the backup and the client still
// gets a 200 SSE stream (ADR-0011: streaming failover only happens before the
// first byte).
func TestRouting_StreamFailsOverOnUpstreamError(t *testing.T) {
	h := NewHarness(t)

	var badHits, goodHits int
	bad := failingUpstream(http.StatusInternalServerError, &badHits)
	defer bad.Close()
	good := sseUpstream("from backup", 5, 5, &goodHits)
	defer good.Close()

	h.AddProvider("bad", bad.URL(), "plain://k")
	h.AddProvider("good", good.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 1_000_000,
		config.ModelUpstream{Provider: "bad", UpstreamModel: "gpt-4o"},
		config.ModelUpstream{Provider: "good", UpstreamModel: "gpt-4o"},
	)
	h.AddRoute("chat", "priority",
		config.RouteProvider{Name: "bad"}, config.RouteProvider{Name: "good"})
	h.SeedKey("sk-sfo", "acme", "team", "key_sfo", nil)
	h.SetQuota("tenant:acme", 1_000_000)
	h.SyncConfig()

	resp := h.Chat("sk-sfo", "chat", true)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 after streaming failover; body=%s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	var content strings.Builder
	sc := bufio.NewScanner(resp.Body)
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
			content.WriteString(ch.Delta.Content)
		}
	}
	if content.String() != "from backup" {
		t.Errorf("stitched content = %q, want from backup (failed over)", content.String())
	}
	if badHits < 1 || goodHits != 1 {
		t.Errorf("hits bad=%d good=%d, want bad≥1 good=1", badHits, goodHits)
	}
}

// Weighted routing: over many requests, both candidates are hit (distribution is
// random, so we only assert both are exercised, not exact ratios).
func TestRouting_WeightedHitsBoth(t *testing.T) {
	h := NewHarness(t)

	var aHits, bHits int
	a := jsonUpstream("a", 1, 1, &aHits)
	defer a.Close()
	b := jsonUpstream("b", 1, 1, &bHits)
	defer b.Close()

	h.AddProvider("pa", a.URL(), "plain://k")
	h.AddProvider("pb", b.URL(), "plain://k")
	h.AddModel("chat", 1_000_000, 1_000_000,
		config.ModelUpstream{Provider: "pa", UpstreamModel: "gpt-4o"},
		config.ModelUpstream{Provider: "pb", UpstreamModel: "gpt-4o"},
	)
	h.AddRoute("chat", "weighted",
		config.RouteProvider{Name: "pa", Weight: 1}, config.RouteProvider{Name: "pb", Weight: 1})
	h.SeedKey("sk-w", "acme", "team", "key_w", nil)
	h.SetQuota("tenant:acme", 100_000_000)
	h.SyncConfig()

	for i := 0; i < 40; i++ {
		resp := h.Chat("sk-w", "chat", false)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	if aHits == 0 || bHits == 0 {
		t.Errorf("weighted routing did not exercise both: a=%d b=%d", aHits, bHits)
	}
	if aHits+bHits != 40 {
		t.Errorf("total hits = %d, want 40", aHits+bHits)
	}
}

// decodeContent reads the assistant message content from a non-streaming
// response and closes the body.
func decodeContent(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var chat struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &chat); err != nil {
		t.Fatalf("decode: %v; body=%s", err, body)
	}
	if len(chat.Choices) == 0 {
		t.Fatalf("no choices in response: %s", body)
	}
	return chat.Choices[0].Message.Content
}
