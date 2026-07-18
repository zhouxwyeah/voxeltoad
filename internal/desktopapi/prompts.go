package desktopapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"voxeltoad/internal/desktopstore"
)

// promptPayload is the POST/PUT body for /api/v1/prompts.
type promptPayload struct {
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
	Note    string   `json:"note"`
	// Optional provenance when favorited from a trace row.
	SessionID        string `json:"session_id,omitempty"`
	SourceTraceRowID *int64 `json:"source_trace_row_id,omitempty"`
}

// promptView is the wire shape: tags as a parsed array (stored as JSON text).
type promptView struct {
	ID               uint     `json:"id"`
	Title            string   `json:"title"`
	Content          string   `json:"content"`
	Tags             []string `json:"tags"`
	SessionID        string   `json:"session_id,omitempty"`
	SourceTraceRowID *int64   `json:"source_trace_row_id,omitempty"`
	Note             string   `json:"note"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
}

func toPromptView(row *desktopstore.PromptTemplateRow) promptView {
	var tags []string
	if row.Tags != "" {
		_ = json.Unmarshal([]byte(row.Tags), &tags)
	}
	if tags == nil {
		tags = []string{}
	}
	return promptView{
		ID:               row.ID,
		Title:            row.Title,
		Content:          row.Content,
		Tags:             tags,
		SessionID:        row.SessionID,
		SourceTraceRowID: row.SourceTraceRowID,
		Note:             row.Note,
		CreatedAt:        row.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:        row.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func (s *Server) handleListPrompts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rows, total, err := s.prompts.List(r.Context(), q.Get("q"), q.Get("tag"), parsePage(q), parsePageSize(q))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	views := make([]promptView, 0, len(rows))
	for i := range rows {
		views = append(views, toPromptView(&rows[i]))
	}
	writeJSON(w, http.StatusOK, offsetEnvelope(views, total, parsePage(q), parsePageSize(q)))
}

func (s *Server) handleCreatePrompt(w http.ResponseWriter, r *http.Request) {
	var p promptPayload
	if !readJSON(w, r, &p) {
		return
	}
	if err := validatePrompt(p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tagsJSON, _ := json.Marshal(p.Tags)
	row := desktopstore.PromptTemplateRow{
		Title:            strings.TrimSpace(p.Title),
		Content:          p.Content,
		Tags:             string(tagsJSON),
		SessionID:        p.SessionID,
		SourceTraceRowID: p.SourceTraceRowID,
		Note:             p.Note,
	}
	if err := s.prompts.Create(r.Context(), &row); err != nil {
		writeError(w, http.StatusInternalServerError, "create failed")
		return
	}
	writeJSON(w, http.StatusCreated, envelope{Data: toPromptView(&row)})
}

func (s *Server) handleGetPrompt(w http.ResponseWriter, r *http.Request) {
	id, ok := s.promptID(w, r)
	if !ok {
		return
	}
	row, found, err := s.prompts.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "prompt not found")
		return
	}
	writeJSON(w, http.StatusOK, toPromptView(row))
}

func (s *Server) handleUpdatePrompt(w http.ResponseWriter, r *http.Request) {
	id, ok := s.promptID(w, r)
	if !ok {
		return
	}
	var p promptPayload
	if !readJSON(w, r, &p) {
		return
	}
	if err := validatePrompt(p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tagsJSON, _ := json.Marshal(p.Tags)
	updated, err := s.prompts.Update(r.Context(), id, strings.TrimSpace(p.Title), p.Content, string(tagsJSON), p.Note)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	if !updated {
		writeError(w, http.StatusNotFound, "prompt not found")
		return
	}
	row, found, err := s.prompts.Get(r.Context(), id)
	if err != nil || !found {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, envelope{Data: toPromptView(row)})
}

func (s *Server) handleDeletePrompt(w http.ResponseWriter, r *http.Request) {
	id, ok := s.promptID(w, r)
	if !ok {
		return
	}
	deleted, err := s.prompts.Delete(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "prompt not found")
		return
	}
	writeJSON(w, http.StatusOK, envelope{Data: map[string]string{"status": "deleted"}})
}

// promptID parses the {id} path value, writing a 400 on malformed input.
func (s *Server) promptID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id must be a positive integer")
		return 0, false
	}
	return id, true
}

func validatePrompt(p promptPayload) error {
	if strings.TrimSpace(p.Title) == "" {
		return errStr("title is required")
	}
	if strings.TrimSpace(p.Content) == "" {
		return errStr("content is required")
	}
	if len(p.Title) > 200 {
		return errStr("title too long (max 200 chars)")
	}
	for _, t := range p.Tags {
		if t == "" || len(t) > 50 {
			return errStr("tags must be non-empty and at most 50 chars each")
		}
	}
	return nil
}
