package desktopapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrompts_APIRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ts := httptest.NewServer(New(db, "", nil, nil, nil).Handler())
	defer ts.Close()

	// Create.
	code, b := reqBody(t, "POST", ts.URL, "/api/v1/prompts", map[string]any{
		"title": "好 prompt", "content": "请扮演……", "tags": []string{"roleplay", "zh"},
		"session_id": "sess-x", "note": "from trace",
	})
	if code != 201 {
		t.Fatalf("create: %d %s", code, b)
	}
	var created struct {
		Data struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(b, &created)
	if created.Data.ID == 0 {
		t.Fatalf("create response missing id: %s", b)
	}

	// List with tag filter.
	code, b = getBody(t, ts, "/api/v1/prompts?tag=zh")
	if code != 200 || !strings.Contains(string(b), `"total":1`) {
		t.Errorf("list by tag: %d %s", code, b)
	}

	// Update.
	code, b = reqBody(t, "PUT", ts.URL, "/api/v1/prompts/1", map[string]any{
		"title": "更好的 prompt", "content": "请扮演 v2……", "tags": []string{"v2"}, "note": "updated",
	})
	if code != 200 {
		t.Fatalf("update: %d %s", code, b)
	}
	if !strings.Contains(string(b), "更好的 prompt") {
		t.Errorf("update response: %s", b)
	}

	// Get one.
	code, b = getBody(t, ts, "/api/v1/prompts/1")
	if code != 200 || !strings.Contains(string(b), `"tags":["v2"]`) {
		t.Errorf("get after update: %d %s", code, b)
	}

	// Validation errors.
	code, _ = reqBody(t, "POST", ts.URL, "/api/v1/prompts", map[string]any{"title": "", "content": "x"})
	if code != 400 {
		t.Errorf("empty title: %d, want 400", code)
	}
	code, _ = reqBody(t, "POST", ts.URL, "/api/v1/prompts", map[string]any{"title": "x", "content": " "})
	if code != 400 {
		t.Errorf("blank content: %d, want 400", code)
	}

	// Delete + 404s.
	code, _ = reqBody(t, "DELETE", ts.URL, "/api/v1/prompts/1", nil)
	if code != 200 {
		t.Errorf("delete: %d, want 200", code)
	}
	if code, _ := getBody(t, ts, "/api/v1/prompts/1"); code != 404 {
		t.Errorf("get after delete: %d, want 404", code)
	}
	if code, _ := reqBody(t, "DELETE", ts.URL, "/api/v1/prompts/1", nil); code != 404 {
		t.Errorf("second delete: %d, want 404", code)
	}
}
