//go:build devstack

// Command devstack brings up a fully-working, dependency-free data plane for
// local manual testing (curl / SDK). It is NOT part of the normal build or CI:
// it is guarded by the `devstack` build tag and pulls in test-only helpers
// (embedded PostgreSQL, the mock upstream). Run it with:
//
//	make devstack          # pre-built binary + clean PG shutdown on Ctrl-C
//	./scripts/devstack.sh   # same, directly
//
// Do NOT launch it via `go run ./cmd/devstack` for manual testing: `go run`
// forks a child binary that orphans the embedded PostgreSQL when interrupted.
// `make devstack-test` / `make sdk-chat-e2e` use the same pre-built-binary
// pattern via scripts/devstack-test.sh and scripts/devstack-sdk-e2e.sh.
//
// It assembles the exact same data-plane chain as cmd/gateway (auth → billing →
// dispatcher → forwarder) but replaces the two external dependencies with
// in-process fakes so nothing else needs to be running:
//
//   - PostgreSQL       → embedded-postgres (downloaded & cached on first run)
//   - admin plane      → skipped; config is fed directly as a static
//     config.Dynamic via WithDispatcherProvider (no poller, no snapshot HTTP)
//   - upstream provider → an in-process OpenAI-compatible mock upstream
//
// It then seeds a tenant/group/API key and funds its quota, and prints the
// ready-to-use API key + gateway URL. Ctrl-C tears everything down.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"voxeltoad/internal/app"
	"voxeltoad/internal/auth"
	"voxeltoad/internal/billing"
	"voxeltoad/internal/config"
	"voxeltoad/internal/plugin"
	"voxeltoad/internal/proxy"
	"voxeltoad/internal/store"
	"voxeltoad/test/testsupport"
)

// Fixed local settings so the printed curl commands are stable. The PostgreSQL
// port is NOT fixed — it is picked from a free port at startup so repeated runs
// (or a lingering PG from a hard-killed prior run) never collide. See freePort.
const (
	gatewayAddr = "127.0.0.1:8080"
	clientKey   = "sk-devstack-client"
	tenant      = "devstack"
	group       = "team-a"
	keyID       = "key_devstack"
	modelAlias  = "chat"        // the model clients request
	upstreamKey = "sk-upstream" // what the mock upstream expects
	quotaScope  = "tenant:devstack"
)

// --- mock state (driven by vitest via control endpoint) ---

// mockState holds the mock upstream's injectable behavior. It is read/written
// atomically so the control HTTP endpoint (for vitest) and the upstream handler
// (for gateway) can share it without a lock.
type mockState struct {
	Content        string
	Chunks         []string
	ChunkDelay     time.Duration
	Usage          mockUsage
	ErrorStatus    int  // 0 = no error; non-zero = respond with this status
	FirstByteError bool // error before any chunk in streaming
	MidStreamBreak bool // send only first chunk then stop
}

type mockUsage struct {
	Prompt     int `json:"prompt_tokens"`
	Completion int `json:"completion_tokens"`
	Total      int `json:"total_tokens"`
}

var mockStatePtr atomic.Pointer[mockState]

func defaultMockState() mockState {
	return mockState{
		Content: "hello from the devstack mock upstream",
		Chunks:  []string{"hello ", "from stream"},
		Usage: mockUsage{
			Prompt:     11,
			Completion: 7,
			Total:      18,
		},
		ChunkDelay: 150 * time.Millisecond,
	}
}

func init() {
	d := defaultMockState()
	mockStatePtr.Store(&d)
}

func main() {
	fail := func(msg string, err error) {
		if err != nil {
			fmt.Fprintf(os.Stderr, "devstack: %s: %v\n", msg, err)
			os.Exit(1)
		}
	}

	// --- 1. PostgreSQL ---
	// Default: embedded PostgreSQL (no docker). Use a free port + a per-port
	// runtime dir, cleaned first, so a lingering embedded-postgres from a
	// previously hard-killed run can never make us fail with "process already
	// listening on port ...". Belt and suspenders: the launcher script also
	// runs us in our own process group and kills the group.
	//
	// Shared-PG mode: when GATEWAY_PG_DSN is set (scripts/stack-test-all.sh), skip
	// the embedded PG entirely and use that DSN. This lets the three stack
	// tests share ONE embedded PG (see cmd/testpg) instead of each paying the
	// initdb+boot cost.
	dsn := os.Getenv("GATEWAY_PG_DSN")
	if dsn == "" {
		pgPort := freePort()
		runtimeDir := filepath.Join(os.TempDir(), fmt.Sprintf("voxeltoad-devstack-pg-%d", pgPort))
		_ = os.RemoveAll(runtimeDir)
		// Registered before pg.Stop so, by defer LIFO, the dir is removed AFTER
		// PostgreSQL has stopped (never yanked out from under a running server).
		defer func() { _ = os.RemoveAll(runtimeDir) }()
		pg := embeddedpostgres.NewDatabase(
			embeddedpostgres.DefaultConfig().
				Port(uint32(pgPort)).
				Database("voxeltoad_devstack").
				RuntimePath(runtimeDir),
		)
		fmt.Println("devstack: starting embedded PostgreSQL (first run downloads a PG binary)...")
		fail("start embedded postgres", pg.Start())
		defer func() { _ = pg.Stop() }()
		dsn = fmt.Sprintf("postgres://postgres:postgres@localhost:%d/voxeltoad_devstack?sslmode=disable", pgPort)
	} else {
		fmt.Println("devstack: using shared PostgreSQL via GATEWAY_PG_DSN")
	}

	db, err := store.Open(dsn)
	fail("open db", err)
	fail("migrate", store.Migrate(db))

	// --- 2. in-process mock upstream (OpenAI-compatible) ---
	mock := testsupport.NewMockUpstream(mockHandler)
	defer mock.Close()

	// Control endpoint — a separate HTTP server on a fixed port so vitest can
	// set/reset the mock state per-test via POST /__set and POST /__reset.
	ctrlMux := http.NewServeMux()
	ctrlMux.HandleFunc("/", controlHandler)
	ctrlSrv := &http.Server{
		Addr:         "127.0.0.1:8091",
		Handler:      ctrlMux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
	go func() {
		if err := ctrlSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "devstack: mock control: %v\n", err)
		}
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = ctrlSrv.Shutdown(ctx)
	}()

	// --- 3. static dynamic config (no admin plane, no poller) ---
	// This is exactly what the admin snapshot would have produced; we build it
	// in memory and feed it straight to the dispatcher.
	dyn := &config.Dynamic{
		Version: "devstack-v1",
		Providers: []config.Provider{{
			Name: "mock-openai", Type: "openai", Adapter: "openai",
			BaseURL: mock.URL(), APIKeyRef: "plain://" + upstreamKey,
			Timeouts: config.ProviderTimeouts{
				Connect: 2 * time.Second, FirstByte: 5 * time.Second, Overall: 30 * time.Second,
			},
		}},
		Models: []config.Model{{
			Alias: modelAlias,
			Upstreams: []config.ModelUpstream{{
				Provider: "mock-openai", UpstreamModel: "gpt-4o",
				Pricing: config.Pricing{PromptPer1M: 1_000_000, CompletionPer1M: 2_000_000, Currency: "usd"},
			}},
		}},
		Routes: []config.Route{{
			ModelAlias: modelAlias, Strategy: "priority",
			Providers: []config.RouteProvider{{Name: "mock-openai"}},
		}},
	}

	// --- 4. data-plane stores + governance chain (wired like cmd/gateway) ---
	stores, err := app.OpenStores(dsn, app.StoreOptions{UsageBuffer: 64})
	fail("open stores", err)
	defer func() { _ = stores.Close() }()

	// Seed a tenant/group/API key and fund its quota so requests authenticate
	// and pass the quota pre-debit.
	fail("seed key", seedKey(db, clientKey, tenant, group, keyID))
	fail("seed quota", stores.SetQuota(context.Background(), quotaScope, 1_000_000_000, "usd"))

	authn := auth.NewAuthenticator(stores.KeyStore, auth.Options{})
	billingPlugin := billing.NewPlugin(func() *config.Dynamic { return dyn }, stores.Quota, stores.UsageRecorder)
	chain := plugin.NewChain(billingPlugin)

	dispWatcher := app.NewDispatcherWatcher(func() *config.Dynamic { return dyn }, proxy.DispatcherConfig{})
	fail("build dispatcher", dispWatcher.Build())

	// --- 5. real gateway HTTP server on a fixed port ---
	srv := &http.Server{
		Addr: gatewayAddr,
		Handler: proxy.Router(nil,
			proxy.WithAuth(authn),
			proxy.WithPlugins(chain),
			proxy.WithDispatcherProvider(dispWatcher.Current),
		),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // streaming: rely on per-stage timeouts
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fail("serve", err)
		}
	}()

	fmt.Printf(`
devstack ready ✅
  gateway      http://%s
  mock-control http://127.0.0.1:8091 (POST /__set /__reset)
  model        %q
  api key      %s
  upstream     %s (in-process mock)
  postgres     %s

Try it:
  ./scripts/devstack-test.sh
  # or:
  curl http://%s/v1/chat/completions \
    -H "Authorization: Bearer %s" \
    -H "Content-Type: application/json" \
    -d '{"model":"%s","messages":[{"role":"user","content":"hi"}]}'

Ctrl-C to stop.
`, gatewayAddr, modelAlias, clientKey, mock.URL(), dsn, gatewayAddr, clientKey, modelAlias)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	fmt.Println("\ndevstack: shutting down...")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	// pg.Stop and runtimeDir cleanup run via defer (LIFO) on return.
}

// freePort asks the OS for an unused TCP port (mirrors the e2e harness). Using a
// fresh port each run avoids collisions with a lingering embedded-postgres.
func freePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "devstack: free port: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// --- mock upstream + control endpoint ---

// mockHandler is an OpenAI-compatible upstream. Its behavior is driven by
// mockStatePtr — vitest sets it via POST /__set on the control server, and the
// next request to the gateway reads the injected behavior.
func mockHandler(w http.ResponseWriter, r *http.Request) {
	st := mockStatePtr.Load()
	if st == nil {
		d := defaultMockState()
		st = &d
	}

	if got := r.Header.Get("Authorization"); got != "Bearer "+upstreamKey {
		http.Error(w, "mock upstream: bad upstream key "+got, http.StatusUnauthorized)
		return
	}
	var body struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	if st.ErrorStatus != 0 {
		writeMockError(w, st.ErrorStatus)
		return
	}

	if body.Stream {
		streamMockResponse(w, st)
		return
	}

	writeMockNonStream(w, st)
}

func writeMockNonStream(w http.ResponseWriter, st *mockState) {
	w.Header().Set("Content-Type", "application/json")
	c := st.Content
	u := st.Usage
	resp, _ := json.Marshal(map[string]any{
		"id":      "chatcmpl-devstack",
		"object":  "chat.completion",
		"model":   "gpt-4o",
		"created": time.Now().Unix(),
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": c,
			},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{
			"prompt_tokens":     u.Prompt,
			"completion_tokens": u.Completion,
			"total_tokens":      u.Total,
		},
	})
	_, _ = w.Write(resp)
}

func streamMockResponse(w http.ResponseWriter, st *mockState) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	if st.FirstByteError {
		writeMockError(w, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	fl.Flush()

	chunks := st.Chunks
	delay := st.ChunkDelay

	type streamChunkDelta struct {
		Index        int            `json:"index"`
		Delta        map[string]any `json:"delta"`
		FinishReason *string        `json:"finish_reason"`
	}
	type streamChunk struct {
		ID      string             `json:"id"`
		Object  string             `json:"object"`
		Model   string             `json:"model"`
		Created int64              `json:"created"`
		Choices []streamChunkDelta `json:"choices"`
		Usage   *mockUsage         `json:"usage,omitempty"`
	}

	finish := "stop"
	for i, content := range chunks {
		delta := map[string]any{"content": content}
		if i == 0 {
			delta["role"] = "assistant"
		}
		sc := streamChunk{
			ID:      "chatcmpl-devstack",
			Object:  "chat.completion.chunk",
			Model:   "gpt-4o",
			Created: time.Now().Unix(),
			Choices: []streamChunkDelta{{Index: 0, Delta: delta}},
		}
		data, _ := json.Marshal(sc)
		fmt.Fprintf(w, "data: %s\n\n", data)
		fl.Flush()
		if st.MidStreamBreak {
			return // truncated stream; gateway defers [DONE] (stream.go:79-84)
		}
		time.Sleep(delay)
	}

	// final chunk with usage
	fc := streamChunk{
		ID:      "chatcmpl-devstack",
		Object:  "chat.completion.chunk",
		Model:   "gpt-4o",
		Created: time.Now().Unix(),
		Choices: []streamChunkDelta{{Index: 0, Delta: map[string]any{}, FinishReason: &finish}},
		Usage:   &st.Usage,
	}
	data, _ := json.Marshal(fc)
	fmt.Fprintf(w, "data: %s\n\n", data)
	fl.Flush()
	fmt.Fprint(w, "data: [DONE]\n\n")
	fl.Flush()
}

func writeMockError(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"message":"injected mock error","type":"upstream_error"}}`))
}

// controlHandler is the HTTP endpoint vitest drives to set/reset mock behavior.
func controlHandler(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/__reset" && r.Method == http.MethodPost:
		d := defaultMockState()
		mockStatePtr.Store(&d)
		w.WriteHeader(http.StatusNoContent)

	case r.URL.Path == "/__set" && r.Method == http.MethodPost:
		var next mockState
		if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mockStatePtr.Store(&next)
		w.WriteHeader(http.StatusNoContent)

	default:
		http.NotFound(w, r)
	}
}

// seedKey inserts a tenant/group/api_key with the SHA-256 hash the auth layer
// expects (mirrors the e2e seed helper; admin key issuance is the real path).
func seedKey(db *store.DB, plaintext, tenant, group, keyID string) error {
	sum := sha256.Sum256([]byte(plaintext))
	hash := hex.EncodeToString(sum[:])

	var tenantID, groupID int64
	if err := db.Raw(
		`INSERT INTO tenants (name) VALUES (?) ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id`,
		tenant,
	).Scan(&tenantID).Error; err != nil {
		return err
	}
	if err := db.Raw(
		`INSERT INTO groups (tenant_id, name) VALUES (?, ?) RETURNING id`, tenantID, group,
	).Scan(&groupID).Error; err != nil {
		return err
	}
	return db.Exec(
		`INSERT INTO api_keys (key_id, hash, tenant_id, group_id, allowed_models)
		 VALUES (?, ?, ?, ?, '[]'::jsonb)
		 ON CONFLICT (key_id) DO NOTHING`,
		keyID, hash, tenantID, groupID,
	).Error
}
