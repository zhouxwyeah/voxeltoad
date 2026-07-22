package claude_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/adapter/claude"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata %s: %v", name, err)
	}
	return b
}

func newAdapter(t *testing.T) adapter.Adapter {
	t.Helper()
	a, err := claude.New(claude.Options{BaseURL: "https://api.anthropic.com", APIKey: "sk-ant-test"})
	if err != nil {
		t.Fatalf("claude.New: %v", err)
	}
	return a
}

func TestName(t *testing.T) {
	if got := newAdapter(t).Name(); got != "claude" {
		t.Errorf("Name() = %q, want claude", got)
	}
}

func TestRegisteredInRegistry(t *testing.T) {
	a, err := adapter.New("claude", claude.Options{BaseURL: "https://x", APIKey: "k"})
	if err != nil {
		t.Fatalf("registry New(claude): %v", err)
	}
	if a.Name() != "claude" {
		t.Errorf("Name = %q, want claude", a.Name())
	}
}

func TestBuildRequest_MapsToMessagesAPI(t *testing.T) {
	a := newAdapter(t)
	n := 1024
	ur, err := a.BuildRequest(context.Background(), &adapter.UnifiedRequest{
		Model:     "claude-opus-4-5",
		MaxTokens: &n,
		Messages: []adapter.Message{
			{Role: adapter.RoleSystem, Content: adapter.NewContentText("be concise")},
			{Role: adapter.RoleUser, Content: adapter.NewContentText("hi")},
		},
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if ur.URL != "https://api.anthropic.com/v1/messages" {
		t.Errorf("URL = %q, want .../v1/messages", ur.URL)
	}
	if got := ur.Header.Get("x-api-key"); got != "sk-ant-test" {
		t.Errorf("x-api-key = %q", got)
	}
	if ur.Header.Get("anthropic-version") == "" {
		t.Error("anthropic-version header must be set")
	}
	body := string(ur.Body)
	// System must be lifted to a top-level field, not left in messages.
	if !strings.Contains(body, `"system":"be concise"`) {
		t.Errorf("body missing top-level system: %s", body)
	}
	if strings.Contains(body, `"role":"system"`) {
		t.Errorf("system must not appear as a message role: %s", body)
	}
	if !strings.Contains(body, `"max_tokens":1024`) {
		t.Errorf("body missing max_tokens: %s", body)
	}
	if !strings.Contains(body, `"hi"`) {
		t.Errorf("body missing user content: %s", body)
	}
}

func TestParseResponse_MapsContentAndUsage(t *testing.T) {
	a := newAdapter(t)
	resp, err := a.ParseResponse(readTestdata(t, "messages_response.json"))
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content.Text() != "Hello there!" {
		t.Errorf("content = %q", resp.Choices[0].Message.Content)
	}
	// stop_reason → finish_reason mapping (end_turn → stop).
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", resp.Choices[0].FinishReason)
	}
	// input/output_tokens → prompt/completion/total.
	if resp.Usage == nil {
		t.Fatal("usage nil")
	}
	if resp.Usage.PromptTokens != 9 || resp.Usage.CompletionTokens != 12 || resp.Usage.TotalTokens != 21 {
		t.Errorf("usage = %+v, want 9/12/21", resp.Usage)
	}
}

func TestExtractUsage(t *testing.T) {
	a := newAdapter(t)
	if _, err := a.ExtractUsage(&adapter.UnifiedResponse{}); err == nil {
		t.Error("expected error when usage absent")
	}
	u, err := a.ExtractUsage(&adapter.UnifiedResponse{Usage: &adapter.Usage{TotalTokens: 5}})
	if err != nil || u.TotalTokens != 5 {
		t.Errorf("ExtractUsage = %+v, %v", u, err)
	}
}

// TestParseResponse_UpstreamRequestIDFromBody asserts the Anthropic body
// request_id is parsed into UnifiedResponse.UpstreamRequestID as a fallback
// (the Forwarder overrides it with the response header when present).
func TestParseResponse_UpstreamRequestIDFromBody(t *testing.T) {
	a := newAdapter(t)
	body := []byte(`{
		"id": "msg_01",
		"model": "claude-3",
		"stop_reason": "end_turn",
		"request_id": "req_01XFD",
		"content": [{"type":"text","text":"hi"}],
		"usage": {"input_tokens": 1, "output_tokens": 1}
	}`)
	resp, err := a.ParseResponse(body)
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}
	if resp.UpstreamRequestID != "req_01XFD" {
		t.Errorf("UpstreamRequestID = %q, want req_01XFD", resp.UpstreamRequestID)
	}
}

// TestParseStream verifies ADR-0010: stream ends on message_stop (no [DONE]),
// content is assembled from content_block_delta, and the trailing chunk carries
// usage combining input_tokens (message_start) + output_tokens (message_delta).
func TestParseStream(t *testing.T) {
	a := newAdapter(t)
	sr, err := a.ParseStream(strings.NewReader(string(readTestdata(t, "messages_stream.txt"))))
	if err != nil {
		t.Fatalf("ParseStream: %v", err)
	}
	defer func() { _ = sr.Close() }()

	var content strings.Builder
	var finish string
	var lastUsage *adapter.Usage
	for {
		c, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		content.WriteString(c.DeltaContent)
		if c.FinishReason != "" {
			finish = c.FinishReason
		}
		if c.Usage != nil {
			lastUsage = c.Usage
		}
	}

	if content.String() != "Hello" {
		t.Errorf("content = %q, want Hello", content.String())
	}
	if finish != "stop" {
		t.Errorf("finish = %q, want stop", finish)
	}
	if lastUsage == nil {
		t.Fatal("trailing usage nil")
	}
	// input 9 (message_start) + output 2 (message_delta) → 9/2/11.
	if lastUsage.PromptTokens != 9 || lastUsage.CompletionTokens != 2 || lastUsage.TotalTokens != 11 {
		t.Errorf("assembled usage = %+v, want 9/2/11", lastUsage)
	}
}

func TestParseStream_PartialFramesAcrossReads(t *testing.T) {
	a := newAdapter(t)
	raw := string(readTestdata(t, "messages_stream.txt"))
	sr, err := a.ParseStream(iotest.OneByteReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("ParseStream: %v", err)
	}
	defer func() { _ = sr.Close() }()

	var content strings.Builder
	for {
		c, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		content.WriteString(c.DeltaContent)
	}
	if content.String() != "Hello" {
		t.Errorf("content = %q, want Hello", content.String())
	}
}

// TestParseResponse_PromptCache verifies that Claude's
// cache_creation_input_tokens / cache_read_input_tokens are decoded:
// PromptTokens sums all three input components, CachedPromptTokens captures
// the cache read hit. cache_creation is folded into PromptTokens at full price.
func TestParseResponse_PromptCache(t *testing.T) {
	a := newAdapter(t)
	resp, err := a.ParseResponse(readTestdata(t, "messages_response_cached.json"))
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}
	u := resp.Usage
	if u == nil {
		t.Fatal("usage nil")
	}
	// input 20 + cache_creation 30 + cache_read 50 = 100 prompt tokens.
	if u.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", u.PromptTokens)
	}
	if u.CompletionTokens != 10 {
		t.Errorf("CompletionTokens = %d, want 10", u.CompletionTokens)
	}
	if u.TotalTokens != 110 {
		t.Errorf("TotalTokens = %d, want 110", u.TotalTokens)
	}
	if u.CachedPromptTokens != 50 {
		t.Errorf("CachedPromptTokens = %d, want 50 (cache_read)", u.CachedPromptTokens)
	}
}

// TestParseStream_PromptCache verifies streamed cache fields: message_start
// carries cache_creation/cache_read; message_delta carries output; the trailing
// chunk's usage must combine them.
func TestParseStream_PromptCache(t *testing.T) {
	a := newAdapter(t)
	sr, err := a.ParseStream(strings.NewReader(string(readTestdata(t, "messages_stream_cached.txt"))))
	if err != nil {
		t.Fatalf("ParseStream: %v", err)
	}
	defer func() { _ = sr.Close() }()

	var lastUsage *adapter.Usage
	for {
		c, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if c.Usage != nil {
			lastUsage = c.Usage
		}
	}
	if lastUsage == nil {
		t.Fatal("no usage chunk observed")
	}
	// input 20 + cache_creation 30 + cache_read 50 = 100 prompt; output 5.
	if lastUsage.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", lastUsage.PromptTokens)
	}
	if lastUsage.CompletionTokens != 5 {
		t.Errorf("CompletionTokens = %d, want 5", lastUsage.CompletionTokens)
	}
	if lastUsage.CachedPromptTokens != 50 {
		t.Errorf("CachedPromptTokens = %d, want 50", lastUsage.CachedPromptTokens)
	}
}

// TestParseStream_PassthroughFrames verifies that content_block_start and
// content_block_stop events ALSO produce Raw-carrying chunks (not just
// content_block_delta). Passthrough mode (ADR-0047) relays every upstream
// frame verbatim to the client; if start/stop are swallowed by the default
// branch, the client's Anthropic stream is missing the block boundaries and
// tool_use blocks are unparseable — a protocol violation.
//
// This is a regression test for the bug where the default branch ignored
// content_block_start/stop (safe for the translating path, which synthesizes
// its own start/stop, but fatal for passthrough).
func TestParseStream_PassthroughFrames(t *testing.T) {
	a := newAdapter(t)
	sr, err := a.ParseStream(strings.NewReader(string(readTestdata(t, "messages_stream.txt"))))
	if err != nil {
		t.Fatalf("ParseStream: %v", err)
	}
	defer func() { _ = sr.Close() }()

	var frames []string
	for {
		c, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if len(c.Raw) == 0 {
			continue
		}
		// Extract the event type from the reassembled Raw frame.
		raw := string(c.Raw)
		if strings.HasPrefix(raw, "event: ") {
			frames = append(frames, strings.SplitN(strings.TrimPrefix(raw, "event: "), "\n", 2)[0])
		}
	}

	// Every upstream event must surface as a Raw frame for passthrough:
	// message_start, content_block_start, 2× content_block_delta,
	// content_block_stop, message_delta. (message_stop terminates via EOF, not
	// a chunk.) Before the fix, content_block_start/stop were missing.
	want := []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
	}
	if len(frames) != len(want) {
		t.Fatalf("Raw frame count = %d (%v), want %d (%v)", len(frames), frames, len(want), want)
	}
	for i, w := range want {
		if frames[i] != w {
			t.Errorf("frames[%d] = %q, want %q", i, frames[i], w)
		}
	}
}
