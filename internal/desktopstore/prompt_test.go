package desktopstore

import (
	"context"
	"testing"
)

func seedPrompts(t *testing.T, db *DB) {
	t.Helper()
	repo := NewPromptRepo(db)
	rows := []PromptTemplateRow{
		{Title: "代码审查", Content: "请审查这段代码……", Tags: `["review","go"]`, SessionID: "sess-a"},
		{Title: "SQL 优化", Content: "优化这个查询……", Tags: `["sql"]`, Note: "效果不错"},
		{Title: "翻译助手", Content: "把以下内容翻译成英文……", Tags: `["translate","daily"]`},
	}
	for _, r := range rows {
		if err := repo.Create(context.Background(), &r); err != nil {
			t.Fatalf("create prompt: %v", err)
		}
	}
}

func TestPromptRepo_CRUD(t *testing.T) {
	db := openTestDB(t)
	repo := NewPromptRepo(db)
	ctx := context.Background()

	row := PromptTemplateRow{Title: "t1", Content: "c1", Tags: `["a","b"]`, Note: "n"}
	if err := repo.Create(ctx, &row); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if row.ID == 0 {
		t.Fatal("Create should populate ID")
	}

	got, ok, err := repo.Get(ctx, int64(row.ID))
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Title != "t1" || got.Tags != `["a","b"]` {
		t.Errorf("Get = %+v", got)
	}

	ok, err = repo.Update(ctx, int64(row.ID), "t2", "c2", `["c"]`, "n2")
	if err != nil || !ok {
		t.Fatalf("Update: ok=%v err=%v", ok, err)
	}
	got, _, _ = repo.Get(ctx, int64(row.ID))
	if got.Title != "t2" || got.Content != "c2" || got.Tags != `["c"]` || got.Note != "n2" {
		t.Errorf("after Update = %+v", got)
	}

	ok, err = repo.Delete(ctx, int64(row.ID))
	if err != nil || !ok {
		t.Fatalf("Delete: ok=%v err=%v", ok, err)
	}
	if _, ok, _ := repo.Get(ctx, int64(row.ID)); ok {
		t.Error("row should be gone after Delete")
	}
	// Deleting again reports not-found, not an error.
	if ok, err := repo.Delete(ctx, int64(row.ID)); err != nil || ok {
		t.Errorf("second Delete = (ok=%v err=%v), want (false, nil)", ok, err)
	}
}

func TestPromptRepo_ListFilters(t *testing.T) {
	db := openTestDB(t)
	seedPrompts(t, db)
	repo := NewPromptRepo(db)
	ctx := context.Background()

	rows, total, err := repo.List(ctx, "", "", 1, 50)
	if err != nil || total != 3 || len(rows) != 3 {
		t.Fatalf("List all = (%d, %d, %v), want 3", len(rows), total, err)
	}

	// q matches title OR content, case-insensitively.
	_, total, _ = repo.List(ctx, "代码", "", 1, 50)
	if total != 1 {
		t.Errorf("q=代码 total = %d, want 1", total)
	}
	_, total, _ = repo.List(ctx, "查询", "", 1, 50)
	if total != 1 {
		t.Errorf("q=查询 total = %d, want 1", total)
	}

	// tag matches an exact array element: "sq" must NOT match ["sql"].
	_, total, _ = repo.List(ctx, "", "sql", 1, 50)
	if total != 1 {
		t.Errorf("tag=sql total = %d, want 1", total)
	}
	_, total, _ = repo.List(ctx, "", "sq", 1, 50)
	if total != 0 {
		t.Errorf("tag=sq total = %d, want 0 (no prefix matching)", total)
	}

	// LIKE metacharacters in q are literal, not wildcards.
	_, total, _ = repo.List(ctx, "100%", "", 1, 50)
	if total != 0 {
		t.Errorf("q=100%% total = %d, want 0 (escaped)", total)
	}

	// Pagination.
	rows, total, _ = repo.List(ctx, "", "", 2, 2)
	if total != 3 || len(rows) != 1 {
		t.Errorf("page 2 size 2 = (%d rows, total %d), want 1/3", len(rows), total)
	}
}
