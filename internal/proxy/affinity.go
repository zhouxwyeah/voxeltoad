package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/config"
)

// sessionKeyCtxType is the context key under which the extracted session key is
// carried from the HTTP handler down to the dispatcher/router.
type sessionKeyCtxType struct{}

// withSessionKey returns ctx carrying the session key for affinity routing.
func withSessionKey(ctx context.Context, key string) context.Context {
	if key == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionKeyCtxType{}, key)
}

// sessionKeyFrom returns the session key carried on ctx, or "" if none. A missing
// key means affinity degrades to the provider list's natural order (no stickiness
// possible), which is safe.
func sessionKeyFrom(ctx context.Context) string {
	if v, ok := ctx.Value(sessionKeyCtxType{}).(string); ok {
		return v
	}
	return ""
}

// sessionSourceCtxType carries the origin of the session key (which source it
// was resolved from) for observability, alongside the key itself.
type sessionSourceCtxType struct{}

// withSessionSource returns ctx carrying the session-key source label. Empty
// source is stored as-is (means "no session key at all").
func withSessionSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, sessionSourceCtxType{}, source)
}

// sessionSourceFrom returns the session-key source on ctx, or "" if none.
func sessionSourceFrom(ctx context.Context) string {
	if v, ok := ctx.Value(sessionSourceCtxType{}).(string); ok {
		return v
	}
	return ""
}

// ingressProtocolCtxType carries the client's ingress protocol ("openai" /
// "anthropic") from the HTTP handler down to the dispatcher, so protocol-aware
// routing (ADR-0047) can prefer providers whose adapter speaks the same wire
// protocol. Empty = unknown / single-provider test mode (no protocol preference).
type ingressProtocolCtxType struct{}

// withIngressProtocol returns ctx carrying the ingress protocol for the
// dispatcher's protocol-aware candidate reordering (ADR-0047).
func withIngressProtocol(ctx context.Context, protocol string) context.Context {
	if protocol == "" {
		return ctx
	}
	return context.WithValue(ctx, ingressProtocolCtxType{}, protocol)
}

// ingressProtocolFrom returns the ingress protocol on ctx, or "" if none. The
// dispatcher reads this after Candidates to reorder candidates so
// protocol-matching providers come first (passthrough), with others as
// failover (translated).
func ingressProtocolFrom(ctx context.Context) string {
	if v, ok := ctx.Value(ingressProtocolCtxType{}).(string); ok {
		return v
	}
	return ""
}

// Session-source labels recorded on request_logs.session_source and the trace
// span, so operators can see which mechanism carried each request's session key
// (and thus how reliable its affinity is).
const (
	sourceHeaderConfig  = "header-config"  // X-Voxeltoad-Session / X-Session-Id (configured list)
	sourceHeaderGeneric = "header-generic" // any x-<vendor>-session-id matched by regex
	sourceBodySession   = "body-session"   // top-level body session_id (Cline/OpenRouter)
	sourceBodyMetadata  = "body-metadata"  // body metadata.session_id (LiteLLM proxy)
	sourceBodyUser      = "body-user"      // body user field (weak session approximation)
	sourcePrefix        = "prefix"         // prefixHash(system + first user)
)

// genericSessionIDHeaderRE matches any vendor session-id header, mirroring the
// convention LiteLLM uses (litellm_pre_call_utils.py: ^x-.+-session-id$). It
// lets the gateway recognize Claude Code's x-claude-code-session-id (and any
// future agent following the same convention) without per-agent code.
// Case-insensitive; trace-id headers are deliberately excluded to keep the
// trace_id field semantically distinct from the session key.
var genericSessionIDHeaderRE = regexp.MustCompile(`(?i)^x-.+-session-id$`)

// sessionIDMinLen / sessionIDMaxLen bound how long a client-supplied session
// id may be. The minimum prevents a stray short value from hijacking affinity;
// the maximum prevents log/index bloat via overlong values (DEFECT-A).
const (
	sessionIDMinLen = 8
	sessionIDMaxLen = 128
)

// sessionIDValueRE gates which client-supplied values are accepted as a session
// key: sessionIDMinLen..sessionIDMaxLen word/hyphen chars. Applied uniformly to
// every extraction level (header-config, header-generic, body-session,
// body-metadata, body-user) so no path is looser than another.
var sessionIDValueRE = regexp.MustCompile(`^[\w-]{8,128}$`)

// validateSessionID returns v if it is a well-formed session id (matches
// sessionIDValueRE), "" otherwise. A "" return tells the extractor to skip this
// source and continue down the precedence chain.
func validateSessionID(v string) string {
	if sessionIDValueRE.MatchString(v) {
		return v
	}
	return ""
}

// hrwOrder orders providers by Rendezvous / Highest-Random-Weight hashing for a
// session key (ADR-0018): each provider is scored by hash(sessionKey|name) and
// the full list is returned sorted by descending score. The result is a
// deterministic permutation — the same key always yields the same order, so a
// session sticks to its top provider (and, on failover, to the same next
// provider). Adding/removing a provider only reshuffles the keys that mapped to
// the changed provider (~1/n), preserving affinity for everyone else.
//
// It is a pure function of (sessionKey, providers): stateless, and therefore
// consistent across data-plane instances with no shared store.
func hrwOrder(sessionKey string, providers []config.RouteProvider) []config.RouteProvider {
	type scored struct {
		p     config.RouteProvider
		score uint64
	}
	arr := make([]scored, len(providers))
	for i, p := range providers {
		arr[i] = scored{p: p, score: hrwScore(sessionKey, p.Name)}
	}
	sort.SliceStable(arr, func(i, j int) bool {
		if arr[i].score != arr[j].score {
			return arr[i].score > arr[j].score // higher score first
		}
		return arr[i].p.Name < arr[j].p.Name // stable tiebreak on name
	})
	out := make([]config.RouteProvider, len(arr))
	for i, s := range arr {
		out[i] = s.p
	}
	return out
}

// hrwScore is the HRW weight for (sessionKey, providerName): the first 8 bytes
// of SHA-256("key|name") as a uint64. SHA-256 gives a good avalanche so scores
// are well-distributed and independent per provider.
func hrwScore(sessionKey, providerName string) uint64 {
	h := sha256.New()
	_, _ = h.Write([]byte(sessionKey))
	_, _ = h.Write([]byte{'|'})
	_, _ = h.Write([]byte(providerName))
	sum := h.Sum(nil)
	return binary.BigEndian.Uint64(sum[:8])
}

// sessionKeyExtractor resolves the session key used for affinity routing from a
// request, via the session-first precedence chain (ADR-0018): a configured
// header, then a body identity field, then a stable-prefix hash. headers is the
// ordered list of candidate header names (default configured elsewhere).
type sessionKeyExtractor struct {
	headers []string
}

// bodyIdentity carries the request-body fields the extractor reads for a session
// key (UnifiedRequest.Extra is json:"-", so the handler pulls these from the raw
// body and passes them in).
type bodyIdentity struct {
	// SessionID is the top-level body "session_id" field, used by Cline /
	// OpenRouter (json-body sticky-session transport).
	SessionID string
	// MetadataSessionID is body "metadata.session_id", the field LiteLLM's proxy
	// injects from headers and some SDKs set directly.
	MetadataSessionID string
	// User is the OpenAI-standard "user" field; a weak session approximation
	// (per-terminal-user, not per-conversation).
	User string
}

// key returns the session key for the request via the precedence chain:
// configured header > generic x-*-session-id header > body session_id >
// body metadata.session_id > body user > stable-prefix hash. The second return
// is the source label (one of the source* constants, or "" when no key at all).
// For a chat request the prefix hash is a last-resort fallback that produces a
// value whenever there are messages.
//
// Every client-supplied source runs through validateSessionID (uniform
// sessionIDValueRE gate, DEFECT-A): a malformed/overlong value is skipped and
// the next source is tried. Only the prefix-hash fallback (server-generated,
// fixed format) bypasses the gate.
func (e sessionKeyExtractor) key(r *http.Request, req *adapter.UnifiedRequest, id bodyIdentity) (string, string) {
	// 1. Configured header(s) — highest priority (X-Voxeltoad-Session / X-Session-Id).
	for _, h := range e.headers {
		if v := validateSessionID(strings.TrimSpace(r.Header.Get(h))); v != "" {
			return v, sourceHeaderConfig
		}
	}
	// 2. Any x-<vendor>-session-id header matched by the generic regex (e.g.
	// x-claude-code-session-id). http.Header keys are already canonicalized, so
	// a case-insensitive regex is belt-and-suspenders.
	for h, vs := range r.Header {
		if !genericSessionIDHeaderRE.MatchString(h) {
			continue
		}
		// Skip headers already covered by the configured list to avoid double
		// reporting a different source for the same value.
		if e.isConfigured(h) {
			continue
		}
		if len(vs) == 0 {
			continue
		}
		if v := validateSessionID(strings.TrimSpace(vs[0])); v != "" {
			return v, sourceHeaderGeneric
		}
	}
	// 3. Body top-level session_id (Cline / OpenRouter).
	if v := validateSessionID(id.SessionID); v != "" {
		return v, sourceBodySession
	}
	// 4. Body metadata.session_id (LiteLLM proxy convention).
	if v := validateSessionID(id.MetadataSessionID); v != "" {
		return v, sourceBodyMetadata
	}
	// 5. Body user (weak per-user approximation).
	if v := validateSessionID(id.User); v != "" {
		return v, sourceBodyUser
	}
	// 6. Stable-prefix fallback: hash of system + first user message, so a
	// multi-turn conversation with no explicit session id still groups by its
	// shared prefix (aligns with upstream prompt-cache semantics).
	if req != nil {
		if k := prefixHash(req.Messages); k != "" {
			return k, sourcePrefix
		}
	}
	return "", ""
}

// isConfigured reports whether header name h (canonical form) is in the
// configured list, matched case-insensitively.
func (e sessionKeyExtractor) isConfigured(h string) bool {
	for _, c := range e.headers {
		if strings.EqualFold(c, h) {
			return true
		}
	}
	return false
}

// invalidSessionSources returns the source labels of client-supplied session-id
// sources that were present but failed validateSessionID (too short, too long,
// or illegal characters). Used to drive the session_id_invalid_total counter
// (DEFECT-A) without polluting the pure key() return. Each source appears at
// most once. An empty slice means no malformed values were seen.
func (e sessionKeyExtractor) invalidSessionSources(r *http.Request, id bodyIdentity) []string {
	var out []string
	for _, h := range e.headers {
		if raw := strings.TrimSpace(r.Header.Get(h)); raw != "" && validateSessionID(raw) == "" {
			out = append(out, sourceHeaderConfig)
			break // one entry per source kind is enough
		}
	}
	hasGeneric := false
	for h, vs := range r.Header {
		if !genericSessionIDHeaderRE.MatchString(h) || e.isConfigured(h) || len(vs) == 0 {
			continue
		}
		if raw := strings.TrimSpace(vs[0]); raw != "" && validateSessionID(raw) == "" {
			hasGeneric = true
			break
		}
	}
	if hasGeneric {
		out = append(out, sourceHeaderGeneric)
	}
	if id.SessionID != "" && validateSessionID(id.SessionID) == "" {
		out = append(out, sourceBodySession)
	}
	if id.MetadataSessionID != "" && validateSessionID(id.MetadataSessionID) == "" {
		out = append(out, sourceBodyMetadata)
	}
	if id.User != "" && validateSessionID(id.User) == "" {
		out = append(out, sourceBodyUser)
	}
	return out
}

// prefixHashSystemCut is the maximum number of bytes of the system message that
// participate in the prefix hash. Coding agents (Claude Code, Codex, …) prepend
// a stable identity block followed by a dynamic tail (env block with wall-clock
// time, git branch, cwd, token budget). Hashing only the leading cut keeps a
// session's fallback key stable across turns even as that tail churns. 512
// bytes comfortably captures the stable identity prefix across observed agents.
const prefixHashSystemCut = 512

// prefixHash hashes the stable prefix of a conversation — the system message
// (truncated to its first prefixHashSystemCut bytes) plus the first user message
// — so turns that share that prefix map to the same key. Returns "" when there
// is nothing to hash. The system truncation is deliberate: it shields the
// fallback key from dynamic env metadata that coding agents append to the system
// prompt (RISK-2 fallback aggregation drift).
func prefixHash(msgs []adapter.Message) string {
	var system, firstUser string
	for _, m := range msgs {
		if m.Role == "system" && system == "" {
			system = m.Content.Text()
		}
		if m.Role == "user" {
			firstUser = m.Content.Text()
			break
		}
	}
	if system == "" && firstUser == "" {
		return ""
	}
	if len(system) > prefixHashSystemCut {
		system = system[:prefixHashSystemCut]
	}
	h := sha256.New()
	_, _ = h.Write([]byte(system))
	_, _ = h.Write([]byte{0}) // separator so "ab"+"c" ≠ "a"+"bc"
	_, _ = h.Write([]byte(firstUser))
	sum := h.Sum(nil)
	// Prefix the hash so it can't collide with a real header/user value form.
	return "prefix:" + encodeHex(sum[:16])
}

const hexdigits = "0123456789abcdef"

func encodeHex(b []byte) string {
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexdigits[c>>4]
		out[i*2+1] = hexdigits[c&0x0f]
	}
	return string(out)
}
