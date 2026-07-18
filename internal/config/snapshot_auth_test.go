package config

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPoller_SendsInternalToken(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get(InternalTokenHeader)
		w.Header().Set("ETag", "v1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":"v1"}`))
	}))
	defer srv.Close()

	store := NewStore()
	p := NewPoller(srv.URL, time.Second, store, WithInternalToken("s3cret"))
	if err := p.fetch(context.Background()); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotToken != "s3cret" {
		t.Errorf("sent token = %q, want s3cret", gotToken)
	}
}

func TestPoller_NoTokenWhenUnset(t *testing.T) {
	var hadHeader bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadHeader = r.Header[InternalTokenHeader]
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":"v1"}`))
	}))
	defer srv.Close()

	p := NewPoller(srv.URL, time.Second, NewStore())
	if err := p.fetch(context.Background()); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if hadHeader {
		t.Error("no internal token configured; header must not be sent")
	}
}
