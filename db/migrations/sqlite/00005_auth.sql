-- +goose Up
CREATE TABLE api_tokens (
    id TEXT PRIMARY KEY,
    admin_id TEXT NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
    label TEXT NOT NULL CHECK (length(label) BETWEEN 1 AND 120),
    token_hash BLOB NOT NULL UNIQUE,
    scopes_json BLOB NOT NULL CHECK (json_valid(scopes_json) AND json_type(scopes_json) = 'array'),
    created_at TEXT NOT NULL,
    expires_at TEXT,
    last_used_at TEXT,
    revoked_at TEXT
);

CREATE INDEX api_tokens_created_id ON api_tokens(created_at, id);

-- +goose Down
DROP TABLE api_tokens;
