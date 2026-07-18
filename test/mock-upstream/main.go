// Command mock-upstream is a standalone OpenAI-shaped fake provider for manual
// desktop-gateway testing (scripts/desktop-test.sh). It responds to
// /chat/completions (the path the openai adapter builds from base_url) with
// either a fixed non-streaming JSON completion or an SSE stream, mirroring
// test/e2e/mock_helpers_test.go shape. Not built by default —
// scripts/desktop-test.sh builds it on demand.
//
// This is the shell-test analogue of test/testsupport.MockUpstream (which is
// in-process and Go-test-only). Keeping it standalone lets the manual test
// script spin it up as a sibling process without dragging in test/testsupport.
//
// NOTE on path: the openai adapter (internal/adapter/openai/adapter.go:121)
// builds the upstream URL as `base_url + "/chat/completions"` — so with
// base_url=http://127.0.0.1:PORT the adapter hits /chat/completions, NOT
// /v1/chat/completions. This mock registers both for robustness.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"strings"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8099", "listen address")
	flag.Parse()

	const nonStream = `{"id":"chatcmpl-mock","object":"chat.completion","model":"mock-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello from mock-upstream"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":4,"total_tokens":9}}`

	handler := func(w http.ResponseWriter, r *http.Request) {
		var body strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			if n > 0 {
				body.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		if strings.Contains(body.String(), `"stream":true`) || strings.Contains(body.String(), `"stream": true`) {
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			write := func(s string) {
				fmt.Fprintf(w, "data: %s\n\n", s)
				if fl != nil {
					fl.Flush()
				}
			}
			write(`{"id":"s","object":"chat.completion.chunk","model":"mock-model","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`)
			write(`{"id":"s","object":"chat.completion.chunk","model":"mock-model","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":4,"total_tokens":9}}`)
			write("[DONE]")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(nonStream))
	}

	mux := http.NewServeMux()
	// The adapter builds base_url + "/chat/completions"; register that path.
	// Also register /v1/chat/completions in case a future caller uses a
	// /v1-suffixed base_url. Both map to the same handler.
	mux.HandleFunc("/chat/completions", handler)
	mux.HandleFunc("/v1/chat/completions", handler)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	fmt.Printf("mock-upstream listening on http://%s\n", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		panic(err)
	}
}
