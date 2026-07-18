package proxy

import (
	"net/http"
	"strings"
)

// Agent type labels recorded on request_logs / trace_payloads (the agent_type
// column). A canonical short identifier per known agent so the column stays
// narrow and the UI can render an icon/label. Unknown clients get "" (the UI
// shows "—").
const (
	AgentClaudeCode = "claude-code"
	AgentCodex      = "codex"
	AgentCodeBuddy  = "codebuddy"
	AgentWorkBuddy  = "workbuddy"
	AgentOpenCode   = "opencode"
)

// agentRule maps a (case-insensitive) User-Agent substring to an agent type.
// The first match wins, so order matters when one UA could contain another's
// token. UA is the primary signal; the x-<vendor>-session-id header is a
// secondary confirmation (see detectAgent) but rarely disagrees.
var agentRules = []agentRule{
	// claude-cli is the User-Agent Anthropic's Claude Code CLI sends.
	{uaContains: "claude-cli", agentType: AgentClaudeCode, sessionHeader: "x-claude-code-session-id"},
	{uaContains: "claude-code", agentType: AgentClaudeCode, sessionHeader: "x-claude-code-session-id"},
	// codex CLI / the OpenAI Codex agent.
	{uaContains: "codex", agentType: AgentCodex, sessionHeader: "x-codex-session-id"},
	// codebuddy (Tencent).
	{uaContains: "codebuddy", agentType: AgentCodeBuddy, sessionHeader: "x-codebuddy-session-id"},
	// workbuddy.
	{uaContains: "workbuddy", agentType: AgentWorkBuddy, sessionHeader: "x-workbuddy-session-id"},
	// opencode.
	{uaContains: "opencode", agentType: AgentOpenCode, sessionHeader: "x-opencode-session-id"},
}

type agentRule struct {
	uaContains    string
	agentType     string
	sessionHeader string
}

// detectAgent identifies the calling agent/client from the request. It returns
// the canonical agent type label (e.g. "claude-code") or "" when the client is
// unrecognized (a plain OpenAI SDK, curl, a browser, etc.).
//
// The primary signal is the User-Agent header; a matching x-<vendor>-session-id
// header is a confirmation but not required (some agents route through an
// upstream proxy that strips it). Detection is deliberately substring-based and
// case-insensitive so it tolerates version suffixes (e.g.
// "claude-cli/1.0.0 (cli, native, ...)" ). The rule table is the single place
// to extend when a new agent needs to be recognized.
func detectAgent(r *http.Request) string {
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	if ua == "" {
		// Fall back to the session-id header family as the only signal when no
		// UA is present. genericSessionIDHeaderRE matches x-<vendor>-session-id;
		// we map the vendor token back to an agent type.
		if at := agentFromSessionHeader(r); at != "" {
			return at
		}
		return ""
	}
	for _, rule := range agentRules {
		if strings.Contains(ua, rule.uaContains) {
			return rule.agentType
		}
	}
	return ""
}

// agentFromSessionHeader inspects x-<vendor>-session-id headers (the LiteLLM
// convention that affinity already recognizes via genericSessionIDHeaderRE) and
// maps the vendor token to a known agent type. Returns "" if no known vendor
// header is present. http.Header keys are canonicalized.
func agentFromSessionHeader(r *http.Request) string {
	for h := range r.Header {
		if !genericSessionIDHeaderRE.MatchString(h) {
			continue
		}
		// h is like "X-Claude-Code-Session-Id"; the vendor token sits between
		// "x-" and "-session-id". Map it to a canonical agent type.
		lower := strings.ToLower(h)
		lower = strings.TrimPrefix(lower, "x-")
		lower = strings.TrimSuffix(lower, "-session-id")
		switch lower {
		case "claude-code":
			return AgentClaudeCode
		case "codex":
			return AgentCodex
		case "codebuddy":
			return AgentCodeBuddy
		case "workbuddy":
			return AgentWorkBuddy
		case "opencode":
			return AgentOpenCode
		}
	}
	return ""
}
