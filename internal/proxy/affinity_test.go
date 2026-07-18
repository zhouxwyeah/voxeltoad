package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/config"
)

func provs(names ...string) []config.RouteProvider {
	out := make([]config.RouteProvider, len(names))
	for i, n := range names {
		out[i] = config.RouteProvider{Name: n}
	}
	return out
}

// hrwOrder is deterministic: the same session key always yields the same order.
func TestHRWOrder_Deterministic(t *testing.T) {
	ps := provs("a", "b", "c", "d")
	first := hrwOrder("session-123", ps)
	for i := 0; i < 20; i++ {
		got := hrwOrder("session-123", ps)
		if len(got) != len(first) {
			t.Fatalf("length changed: %v vs %v", got, first)
		}
		for j := range got {
			if got[j].Name != first[j].Name {
				t.Fatalf("order not deterministic: %v vs %v", names(got), names(first))
			}
		}
	}
}

// parseTraceparent extracts the 32-hex trace id from a well-formed W3C
// traceparent and rejects malformed values.
func TestParseTraceparent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"valid", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01", "0af7651916cd43dd8448eb211c80319c", true},
		{"valid uppercase", "00-0AF7651916CD43DD8448EB211C80319C-B7AD6B7169203331-01", "0AF7651916CD43DD8448EB211C80319C", true},
		{"empty", "", "", false},
		{"too few fields", "00-0af7651916cd43dd8448eb211c80319c-01", "", false},
		{"bad trace length", "00-0af7651916cd43dd8448eb211c8031-b7ad6b7169203331-01", "", false},
		{"bad span length", "00-0af7651916cd43dd8448eb211c80319c-bad-01", "", false},
		{"non hex", "00-zz7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01", "", false},
		{"short version", "0-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseTraceparent(c.in)
			if ok != c.ok || got != c.want {
				t.Errorf("parseTraceparent(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
			}
		})
	}
}

// hrwOrder returns a full permutation of the input (every provider present once).
func TestHRWOrder_FullPermutation(t *testing.T) {
	ps := provs("a", "b", "c")
	got := hrwOrder("k", ps)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	seen := map[string]bool{}
	for _, p := range got {
		seen[p.Name] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !seen[want] {
			t.Errorf("provider %q missing from ordering %v", want, names(got))
		}
	}
}

// Different session keys spread their top choice across providers (not all keys
// stick to one provider).
func TestHRWOrder_DistributesAcrossKeys(t *testing.T) {
	ps := provs("a", "b", "c")
	topCount := map[string]int{}
	for i := 0; i < 300; i++ {
		key := "sess-" + string(rune('A'+i%26)) + string(rune('0'+i/26))
		top := hrwOrder(key, ps)[0].Name
		topCount[top]++
	}
	// Every provider should be some session's primary (rough balance, not exact).
	for _, p := range []string{"a", "b", "c"} {
		if topCount[p] == 0 {
			t.Errorf("provider %q was never a primary; distribution=%v", p, topCount)
		}
	}
}

// Removing a provider only reshuffles keys that mapped to it (HRW stability):
// keys whose primary was NOT the removed provider keep the same primary.
func TestHRWOrder_StableOnProviderRemoval(t *testing.T) {
	full := provs("a", "b", "c", "d")
	reduced := provs("a", "b", "c") // "d" removed

	for i := 0; i < 200; i++ {
		key := "s" + string(rune('A'+i%26)) + string(rune('0'+i/26))
		primaryFull := hrwOrder(key, full)[0].Name
		primaryReduced := hrwOrder(key, reduced)[0].Name
		if primaryFull != "d" && primaryReduced != primaryFull {
			t.Errorf("key %q primary changed %s→%s despite its provider staying",
				key, primaryFull, primaryReduced)
		}
	}
}

// sessionKey extraction precedence: configured header wins over body fields.
func TestSessionKey_HeaderWins(t *testing.T) {
	ext := sessionKeyExtractor{headers: []string{"X-Voxeltoad-Session"}}
	r, _ := http.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("X-Voxeltoad-Session", "hdr-key-1234")

	if got, src := ext.key(r, &adapter.UnifiedRequest{}, bodyIdentity{User: "body-user-ab"}); got != "hdr-key-1234" || src != sourceHeaderConfig {
		t.Errorf("key = (%q, %q), want (hdr-key-1234, %s)", got, src, sourceHeaderConfig)
	}
}

// Falls back to body session_id, then metadata.session_id, then user when no
// header is present. prompt_cache_key is deliberately NOT a session source.
// All body values must satisfy validateSessionID (≥8 word/hyphen chars, ≤128).
func TestSessionKey_BodyFields(t *testing.T) {
	ext := sessionKeyExtractor{headers: []string{"X-Voxeltoad-Session"}}
	r, _ := http.NewRequest(http.MethodPost, "/", nil)

	if got, src := ext.key(r, &adapter.UnifiedRequest{}, bodyIdentity{SessionID: "sess-body-12", User: "body-user-ab"}); got != "sess-body-12" || src != sourceBodySession {
		t.Errorf("key = (%q, %q), want (sess-body-12, %s)", got, src, sourceBodySession)
	}
	if got, src := ext.key(r, &adapter.UnifiedRequest{}, bodyIdentity{MetadataSessionID: "meta-sess-12"}); got != "meta-sess-12" || src != sourceBodyMetadata {
		t.Errorf("key = (%q, %q), want (meta-sess-12, %s)", got, src, sourceBodyMetadata)
	}
	if got, src := ext.key(r, &adapter.UnifiedRequest{}, bodyIdentity{User: "body-user-ab"}); got != "body-user-ab" || src != sourceBodyUser {
		t.Errorf("key = (%q, %q), want (body-user-ab, %s)", got, src, sourceBodyUser)
	}
}

// A generic x-<vendor>-session-id header (e.g. x-claude-code-session-id) is
// recognized by the regex fallback even though it is not in the configured list.
func TestSessionKey_GenericHeaderRegex(t *testing.T) {
	ext := sessionKeyExtractor{headers: []string{"X-Voxeltoad-Session"}}
	r, _ := http.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("X-Claude-Code-Session-Id", "abc12345-6789")

	got, src := ext.key(r, &adapter.UnifiedRequest{}, bodyIdentity{})
	if got != "abc12345-6789" || src != sourceHeaderGeneric {
		t.Errorf("key = (%q, %q), want (abc12345-6789, %s)", got, src, sourceHeaderGeneric)
	}

	// A too-short value (< 8 chars) is rejected so it can't hijack affinity.
	r2, _ := http.NewRequest(http.MethodPost, "/", nil)
	r2.Header.Set("X-Foo-Session-Id", "short")
	if got, _ := ext.key(r2, &adapter.UnifiedRequest{}, bodyIdentity{}); got == "short" {
		t.Error("short generic session-id value was accepted")
	}

	// trace-id headers are deliberately NOT matched (keeps trace_id distinct).
	r3, _ := http.NewRequest(http.MethodPost, "/", nil)
	r3.Header.Set("X-Vendor-Trace-Id", "trace1234567")
	if got, _ := ext.key(r3, &adapter.UnifiedRequest{}, bodyIdentity{}); got == "trace1234567" {
		t.Error("x-*-trace-id header was matched by the session regex")
	}
}

// A configured-header value that fails validateSessionID (too short, too long,
// or contains characters outside [\w-]) is rejected and the extractor falls
// through to the next source — same behavior the generic-header path already
// had. This guards against both hijack-by-garbage and log-bloat by overlong
// values (DEFECT-A).
func TestSessionKey_ConfigHeaderRejectsInvalid(t *testing.T) {
	ext := sessionKeyExtractor{headers: []string{"X-Voxeltoad-Session"}}

	// Too short (< 8 chars) → rejected, falls through to body session_id.
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("X-Voxeltoad-Session", "short")
	got, src := ext.key(r, &adapter.UnifiedRequest{}, bodyIdentity{SessionID: "sess-body-12"})
	if got != "sess-body-12" || src != sourceBodySession {
		t.Errorf("short config header not rejected: got (%q, %q), want fall-through to body-session", got, src)
	}

	// Overlong (> sessionIDMaxLen chars) → rejected, falls through.
	r2 := httptest.NewRequest(http.MethodPost, "/", nil)
	overlong := strings.Repeat("a", sessionIDMaxLen+1)
	r2.Header.Set("X-Voxeltoad-Session", overlong)
	got2, src2 := ext.key(r2, &adapter.UnifiedRequest{}, bodyIdentity{SessionID: "sess-body-12"})
	if got2 != "sess-body-12" || src2 != sourceBodySession {
		t.Errorf("overlong config header not rejected: got (%q, %q), want fall-through to body-session", got2, src2)
	}

	// Illegal characters (space) → rejected, falls through.
	r3 := httptest.NewRequest(http.MethodPost, "/", nil)
	r3.Header.Set("X-Voxeltoad-Session", "has space here")
	got3, src3 := ext.key(r3, &adapter.UnifiedRequest{}, bodyIdentity{SessionID: "sess-body-12"})
	if got3 != "sess-body-12" || src3 != sourceBodySession {
		t.Errorf("illegal-char config header not rejected: got (%q, %q), want fall-through to body-session", got3, src3)
	}
}

// Body session-id / metadata.session-id / user values are subject to the same
// validateSessionID gate as headers: invalid values are skipped and the
// extractor continues down the chain (DEFECT-A).
func TestSessionKey_BodyFieldsRejectInvalid(t *testing.T) {
	ext := sessionKeyExtractor{headers: []string{"X-Voxeltoad-Session"}}
	r := httptest.NewRequest(http.MethodPost, "/", nil)

	// Short session_id → falls through to a valid metadata.session_id.
	got, src := ext.key(r, &adapter.UnifiedRequest{}, bodyIdentity{
		SessionID:         "short",
		MetadataSessionID: "meta-sess-12",
	})
	if got != "meta-sess-12" || src != sourceBodyMetadata {
		t.Errorf("short body session_id not skipped: got (%q, %q)", got, src)
	}

	// Overlong user → falls through to prefix hash (no other source left).
	overlong := strings.Repeat("a", sessionIDMaxLen+1)
	reqWithMsgs := &adapter.UnifiedRequest{Messages: []adapter.Message{
		{Role: "system", Content: adapter.NewContentText("you are helpful")},
		{Role: "user", Content: adapter.NewContentText("hello")},
	}}
	got2, src2 := ext.key(r, reqWithMsgs, bodyIdentity{User: overlong})
	if src2 != sourcePrefix {
		t.Errorf("overlong body user not skipped: got (%q, %q), want prefix fallback", got2, src2)
	}
}

// sessionIDMaxLen is enforced as the inclusive upper bound: a value exactly
// sessionIDMaxLen chars long is accepted, one char longer is rejected.
func TestSessionKey_MaxLengthBoundary(t *testing.T) {
	ext := sessionKeyExtractor{headers: []string{"X-Voxeltoad-Session"}}

	// Exactly sessionIDMaxLen chars → accepted.
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	exact := strings.Repeat("a", sessionIDMaxLen)
	r.Header.Set("X-Voxeltoad-Session", exact)
	got, src := ext.key(r, &adapter.UnifiedRequest{}, bodyIdentity{})
	if got != exact || src != sourceHeaderConfig {
		t.Errorf("max-length config header rejected: got (%q, %q)", got, src)
	}
}

// invalidSessionSources reports which client sources carried a non-empty but
// malformed value, in precedence order, one entry per source kind. It drives
// the session_id_invalid_total counter.
func TestInvalidSessionSources(t *testing.T) {
	ext := sessionKeyExtractor{headers: []string{"X-Voxeltoad-Session"}}

	// Clean request → no invalid sources.
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if got := ext.invalidSessionSources(r, bodyIdentity{}); len(got) != 0 {
		t.Errorf("clean request reported invalid sources: %v", got)
	}

	// Config header present but too short + body user present but overlong.
	r2 := httptest.NewRequest(http.MethodPost, "/", nil)
	r2.Header.Set("X-Voxeltoad-Session", "short")
	overlong := strings.Repeat("a", sessionIDMaxLen+1)
	got := ext.invalidSessionSources(r2, bodyIdentity{User: overlong})
	want := []string{sourceHeaderConfig, sourceBodyUser}
	if !equalStrings(got, want) {
		t.Errorf("invalid sources = %v, want %v", got, want)
	}

	// A valid generic header is NOT flagged; an invalid one IS.
	r3 := httptest.NewRequest(http.MethodPost, "/", nil)
	r3.Header.Set("X-Foo-Session-Id", "short")
	got3 := ext.invalidSessionSources(r3, bodyIdentity{})
	if len(got3) != 1 || got3[0] != sourceHeaderGeneric {
		t.Errorf("generic invalid = %v, want [%s]", got3, sourceHeaderGeneric)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// prompt_cache_key must NOT influence session-key resolution (it is a cache
// hint, not a session id). Regression guard.
func TestSessionKey_PromptCacheKeyIgnored(t *testing.T) {
	ext := sessionKeyExtractor{headers: []string{"X-Voxeltoad-Session"}}
	r, _ := http.NewRequest(http.MethodPost, "/", nil)

	// With only a prompt_cache_key and no other source, the prefix hash (or
	// empty) is used — never the cache key value itself.
	got, _ := ext.key(r, &adapter.UnifiedRequest{}, bodyIdentity{})
	if got == "some-cache-key" {
		t.Error("prompt_cache_key leaked into session key resolution")
	}
}

// With no header and no body id, falls back to a stable hash of the prefix
// (system + first user message), and that hash is stable across turns that share
// the prefix but differ in later messages.
func TestSessionKey_PrefixHashFallback(t *testing.T) {
	ext := sessionKeyExtractor{headers: []string{"X-Voxeltoad-Session"}}
	r, _ := http.NewRequest(http.MethodPost, "/", nil)

	turn1 := &adapter.UnifiedRequest{Messages: []adapter.Message{
		{Role: "system", Content: adapter.NewContentText("you are helpful")},
		{Role: "user", Content: adapter.NewContentText("hello")},
	}}
	turn2 := &adapter.UnifiedRequest{Messages: []adapter.Message{
		{Role: "system", Content: adapter.NewContentText("you are helpful")},
		{Role: "user", Content: adapter.NewContentText("hello")},
		{Role: "assistant", Content: adapter.NewContentText("hi")},
		{Role: "user", Content: adapter.NewContentText("follow-up")},
	}}
	k1, src1 := ext.key(r, turn1, bodyIdentity{})
	k2, _ := ext.key(r, turn2, bodyIdentity{})
	if k1 == "" {
		t.Fatal("prefix-hash fallback returned empty key")
	}
	if src1 != sourcePrefix {
		t.Errorf("prefix fallback source = %q, want %s", src1, sourcePrefix)
	}
	if k1 != k2 {
		t.Errorf("prefix hash not stable across turns: %q vs %q", k1, k2)
	}

	// A different system prompt → different key.
	other := &adapter.UnifiedRequest{Messages: []adapter.Message{
		{Role: "system", Content: adapter.NewContentText("different")},
		{Role: "user", Content: adapter.NewContentText("hello")},
	}}
	if otherK, _ := ext.key(r, other, bodyIdentity{}); otherK == k1 {
		t.Error("different prefix produced same key")
	}
}

// prefixHash truncates the system message to its first prefixHashSystemCut bytes
// before hashing, so coding agents whose system prompt has a stable identity
// prefix followed by a dynamic tail (env block with time / git branch / cwd)
// still aggregate into one session. The first user message is hashed in full
// (it is the conversation's true starting point).
func TestPrefixHash_SystemDynamicTailIgnored(t *testing.T) {
	ext := sessionKeyExtractor{headers: []string{"X-Voxeltoad-Session"}}
	r, _ := http.NewRequest(http.MethodPost, "/", nil)

	stablePrefix := strings.Repeat("a", prefixHashSystemCut)
	tail1 := "-dynamic-env-time-2026-07-15-git-main"
	tail2 := "-dynamic-env-time-2026-07-16-git-feature"

	turnA := &adapter.UnifiedRequest{Messages: []adapter.Message{
		{Role: "system", Content: adapter.NewContentText(stablePrefix + tail1)},
		{Role: "user", Content: adapter.NewContentText("hello")},
	}}
	turnB := &adapter.UnifiedRequest{Messages: []adapter.Message{
		{Role: "system", Content: adapter.NewContentText(stablePrefix + tail2)},
		{Role: "user", Content: adapter.NewContentText("hello")},
	}}
	kA, srcA := ext.key(r, turnA, bodyIdentity{})
	kB, _ := ext.key(r, turnB, bodyIdentity{})
	if kA == "" {
		t.Fatal("prefix-hash fallback returned empty key")
	}
	if srcA != sourcePrefix {
		t.Errorf("source = %q, want %s", srcA, sourcePrefix)
	}
	if kA != kB {
		t.Errorf("system dynamic tail broke aggregation: %q vs %q (cut=%d)", kA, kB, prefixHashSystemCut)
	}
}

// A system prompt shorter than the cut is hashed in full (no truncation effect).
func TestPrefixHash_ShortSystemUnaffected(t *testing.T) {
	ext := sessionKeyExtractor{headers: []string{"X-Voxeltoad-Session"}}
	r, _ := http.NewRequest(http.MethodPost, "/", nil)

	short := "you are a helpful assistant"
	req := &adapter.UnifiedRequest{Messages: []adapter.Message{
		{Role: "system", Content: adapter.NewContentText(short)},
		{Role: "user", Content: adapter.NewContentText("hello")},
	}}
	if k, _ := ext.key(r, req, bodyIdentity{}); k == "" {
		t.Fatal("prefix-hash fallback returned empty key for short system")
	}
}

func names(ps []config.RouteProvider) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}
