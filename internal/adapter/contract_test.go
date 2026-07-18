package adapter

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// echoAdapter is a minimal Adapter exercising the values-in, values-out
// contract: BuildRequest returns a transport-neutral UpstreamRequest,
// ParseResponse takes bytes, ParseStream takes an io.Reader. It validates the
// interface SHAPE only — not real-provider protocol correctness, which is
// covered by per-adapter testdata tests (e.g. internal/adapter/openai). Do not
// treat a green here as "OpenAI parsing works".
type echoAdapter struct{}

func (echoAdapter) Name() string { return "echo" }

func (echoAdapter) BuildRequest(_ context.Context, req *UnifiedRequest) (*UpstreamRequest, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return &UpstreamRequest{
		Method: "POST",
		URL:    "https://example.test/v1/chat/completions",
		Body:   body,
	}, nil
}

func (echoAdapter) ParseResponse(body []byte) (*UnifiedResponse, error) {
	var r UnifiedResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (echoAdapter) ParseStream(body io.Reader) (StreamReader, error) {
	b, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	return &echoStream{chunks: []Chunk{{DeltaContent: string(b)}}}, nil
}

func (echoAdapter) ExtractUsage(resp *UnifiedResponse) (*Usage, error) {
	if resp.Usage == nil {
		return nil, errNoUsage
	}
	return resp.Usage, nil
}

var errNoUsage = errStr("usage unavailable")

type errStr string

func (e errStr) Error() string { return string(e) }

type echoStream struct {
	chunks []Chunk
	i      int
}

func (s *echoStream) Recv() (Chunk, error) {
	if s.i >= len(s.chunks) {
		return Chunk{}, io.EOF
	}
	c := s.chunks[s.i]
	s.i++
	return c, nil
}
func (s *echoStream) Close() error { return nil }

func TestAdapterContract_BuildRequestIsTransportNeutral(t *testing.T) {
	var a Adapter = echoAdapter{}
	ur, err := a.BuildRequest(context.Background(), &UnifiedRequest{Model: "m"})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	// The point of #1: assert plain fields, no *http.Request to fabricate.
	if ur.Method != "POST" {
		t.Errorf("Method = %q, want POST", ur.Method)
	}
	if ur.URL == "" {
		t.Error("URL should be set")
	}
	var rt UnifiedRequest
	if err := json.Unmarshal(ur.Body, &rt); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if rt.Model != "m" {
		t.Errorf("round-tripped Model = %q, want m", rt.Model)
	}
}

func TestAdapterContract_ParseResponseTakesBytes(t *testing.T) {
	var a Adapter = echoAdapter{}
	got, err := a.ParseResponse([]byte(`{"id":"x","model":"m","usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}
	if got.ID != "x" || got.Model != "m" {
		t.Errorf("parsed = %+v, want id=x model=m", got)
	}
	u, err := a.ExtractUsage(got)
	if err != nil {
		t.Fatalf("ExtractUsage: %v", err)
	}
	if u.TotalTokens != 3 {
		t.Errorf("TotalTokens = %d, want 3", u.TotalTokens)
	}
}

func TestAdapterContract_ParseStreamTakesReader(t *testing.T) {
	var a Adapter = echoAdapter{}
	sr, err := a.ParseStream(strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("ParseStream: %v", err)
	}
	defer func() { _ = sr.Close() }()

	c, err := sr.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if c.DeltaContent != "hello" {
		t.Errorf("DeltaContent = %q, want hello", c.DeltaContent)
	}
	if c.Usage != nil {
		t.Error("intermediate chunk must have nil Usage per contract")
	}
	if _, err := sr.Recv(); err != io.EOF {
		t.Errorf("second Recv err = %v, want io.EOF", err)
	}
}

func TestAdapterContract_ExtractUsageErrorsWhenAbsent(t *testing.T) {
	var a Adapter = echoAdapter{}
	if _, err := a.ExtractUsage(&UnifiedResponse{}); err == nil {
		t.Error("expected error when usage absent")
	}
}
