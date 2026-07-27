package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDetectAgent(t *testing.T) {
	tests := []struct {
		name   string
		ua     string
		hdrKey string
		hdrVal string
		want   string
	}{
		// User-Agent based detection (primary signal).
		{"claude-cli UA", "claude-cli/1.0.83 (external, cli)", "", "", AgentClaudeCode},
		{"claude-code UA variant", "Claude-Code/2.1.0", "", "", AgentClaudeCode},
		{"codex UA", "codex/0.20.0", "", "", AgentCodex},
		{"codebuddy UA", "CodeBuddy/1.2.3", "", "", AgentCodeBuddy},
		{"workbuddy UA", "workbuddy-agent/3.0", "", "", AgentWorkBuddy},
		{"opencode UA", "opencode/0.5.1", "", "", AgentOpenCode},
		// ZCode (Z.ai). Real UA observed from the desktop client.
		{"zcode UA", "ZCode/3.4.2 ai-sdk/provider-utils/4.0.39 runtime/node.js/24", "", "", AgentZCode},
		{"zcode UA lowercase", "zcode/3.4.2 ...", "", "", AgentZCode},

		// Unknown / absent UA.
		{"unknown UA", "curl/8.5.0", "", "", ""},
		{"browser UA", "Mozilla/5.0 (Macintosh)", "", "", ""},
		{"empty UA, no header", "", "", "", ""},
		{"openai python UA", "OpenAI/Python 1.40.0", "", "", ""},
		// A bare ai-sdk UA (no zcode token) must NOT be misread as zcode.
		{"bare ai-sdk UA", "ai-sdk/provider-utils/4.0.39 runtime/node.js/24", "", "", ""},

		// Session-id header fallback when UA is absent.
		{"no UA but claude session header", "", "X-Claude-Code-Session-Id", "abc123def456", AgentClaudeCode},
		{"no UA but codex session header", "", "X-Codex-Session-Id", "xyz789", AgentCodex},
		{"no UA but opencode session header", "", "X-Opencode-Session-Id", "sess-1", AgentOpenCode},
		{"no UA but unknown vendor session header", "", "X-Futureagent-Session-Id", "sess-2", ""},

		// UA wins over session header when both present (UA is primary).
		{"UA + mismatched header", "claude-cli/1.0", "X-Codex-Session-Id", "sess-3", AgentClaudeCode},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			if tt.ua != "" {
				req.Header.Set("User-Agent", tt.ua)
			}
			if tt.hdrKey != "" {
				req.Header.Set(tt.hdrKey, tt.hdrVal)
			}
			got := detectAgent(req)
			if got != tt.want {
				t.Errorf("detectAgent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCapUserAgent(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"trimmed", "  claude-cli/1.0  ", "claude-cli/1.0"},
		{"normal UA", "claude-cli/1.0.83 (cli, native)", "claude-cli/1.0.83 (cli, native)"},
		{"truncated to cap", strings.Repeat("a", userAgentMaxLen+50), strings.Repeat("a", userAgentMaxLen)},
		{"exactly at cap", strings.Repeat("a", userAgentMaxLen), strings.Repeat("a", userAgentMaxLen)},
		// Multi-byte UTF-8 must truncate at a rune boundary, not mid-rune:
		// 86 "中" = 258 bytes (>256); cap falls back to 85 "中" = 255 bytes
		// (last byte is a rune start, no invalid UTF-8 produced).
		{"multi-byte CJK truncates at rune boundary", strings.Repeat("中", userAgentMaxLen/3+1), strings.Repeat("中", userAgentMaxLen/3)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := capUserAgent(tt.in); got != tt.want {
				t.Errorf("capUserAgent(%q) = %q (len %d), want %q (len %d)",
					tt.in, got, len(got), tt.want, len(tt.want))
			}
		})
	}
}
