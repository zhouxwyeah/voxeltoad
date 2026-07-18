package desktopstore

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// PromptTemplateRow is a user-saved prompt (design/desktop.md §10.3-7): the
// "learn from good prompts" loop — favorite a trace's messages, tag them,
// find them later. Tags is a JSON array string (SQLite has no native array;
// personal-scale filtering by LIKE is fine).
type PromptTemplateRow struct {
	ID      uint   `gorm:"primaryKey;autoIncrement"`
	Title   string `gorm:"column:title"`
	Content string `gorm:"column:content;type:text"`
	Tags    string `gorm:"column:tags;type:text"` // JSON array of strings
	// SessionID / SourceTraceRowID link back to the capture the prompt was
	// favorited from (nullable — manual entries have neither).
	SessionID        string    `gorm:"column:session_id;index"`
	SourceTraceRowID *int64    `gorm:"column:source_trace_row_id"`
	Note             string    `gorm:"column:note;type:text"`
	CreatedAt        time.Time `gorm:"column:created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at"`
}

func (*PromptTemplateRow) TableName() string { return "prompt_templates" }

// PromptRepo is the CRUD surface for prompt_templates.
type PromptRepo struct {
	db *DB
}

// NewPromptRepo builds the repo over the desktop SQLite connection.
func NewPromptRepo(db *DB) *PromptRepo { return &PromptRepo{db: db} }

// List returns an offset page of prompt templates, newest-updated first.
// q matches title/content (substring, case-insensitive via LIKE); tag matches
// an exact array element by its JSON-quoted form ("\"tag\"") so "art" does
// not match "artistic".
func (r *PromptRepo) List(ctx context.Context, q, tag string, page, pageSize int) ([]PromptTemplateRow, int64, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	if page < 1 {
		page = 1
	}

	tx := r.db.WithContext(ctx).Model(&PromptTemplateRow{})
	if q != "" {
		like := "%" + escapeLike(q) + "%"
		tx = tx.Where("title LIKE ? ESCAPE '\\' OR content LIKE ? ESCAPE '\\'", like, like)
	}
	if tag != "" {
		tx = tx.Where("tags LIKE ? ESCAPE '\\'", "%"+escapeLike(`"`+tag+`"`)+"%")
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []PromptTemplateRow
	if err := tx.Order("updated_at DESC, id DESC").
		Limit(pageSize).Offset((page - 1) * pageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	if rows == nil {
		rows = []PromptTemplateRow{}
	}
	return rows, total, nil
}

// Create inserts the row, populating ID and timestamps.
func (r *PromptRepo) Create(ctx context.Context, row *PromptTemplateRow) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// Get fetches one row; ok=false when absent.
func (r *PromptRepo) Get(ctx context.Context, id int64) (*PromptTemplateRow, bool, error) {
	var row PromptTemplateRow
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &row, true, nil
}

// Update replaces the editable fields of a row. ok=false when absent.
func (r *PromptRepo) Update(ctx context.Context, id int64, title, content, tagsJSON, note string) (bool, error) {
	res := r.db.WithContext(ctx).Model(&PromptTemplateRow{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"title":      title,
			"content":    content,
			"tags":       tagsJSON,
			"note":       note,
			"updated_at": time.Now(),
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// Delete removes a row. ok=false when absent.
func (r *PromptRepo) Delete(ctx context.Context, id int64) (bool, error) {
	res := r.db.WithContext(ctx).Where("id = ?", id).Delete(&PromptTemplateRow{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// escapeLike escapes LIKE wildcards so user input matches literally.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}
