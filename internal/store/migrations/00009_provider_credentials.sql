-- +goose Up
-- +goose StatementBegin

-- ADR-0030: encrypted upstream provider credentials stored separately from the
-- config snapshot. The config snapshot only carries a reference string
-- (db://provider/<name>, env://VAR, etc.), never the plaintext key.
CREATE TABLE provider_credentials (
    provider_name    VARCHAR PRIMARY KEY,
    ciphertext       BYTEA NOT NULL,
    nonce            BYTEA NOT NULL,
    algorithm        VARCHAR NOT NULL DEFAULT 'AES-256-GCM',
    key_version      VARCHAR NOT NULL DEFAULT 'v0',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT fk_provider_credentials_provider
        FOREIGN KEY (provider_name) REFERENCES providers(name) ON DELETE CASCADE
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS provider_credentials;

-- +goose StatementEnd
