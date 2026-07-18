-- +goose Up
-- +goose StatementBegin

-- response_raw carries the verbatim upstream response body. For streaming
-- requests this is the reassembled SSE transcript, which is not valid JSON and
-- therefore cannot live in a JSONB column. Convert it to TEXT so the exact
-- bytes are preserved (mirrors error_raw, which is already TEXT for the same
-- reason). Existing JSON bodies are left as text (they remain valid JSON).
ALTER TABLE trace_payloads
    ALTER COLUMN response_raw TYPE TEXT,
    ALTER COLUMN response_raw SET DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Rolling back requires that every response_raw value is valid JSON. If any
-- streaming SSE transcripts have been inserted, this will fail until those
-- rows are removed or their response_raw is converted to valid JSON.
ALTER TABLE trace_payloads
    ALTER COLUMN response_raw TYPE JSONB USING response_raw::jsonb,
    ALTER COLUMN response_raw SET DEFAULT '{}';

-- +goose StatementEnd
