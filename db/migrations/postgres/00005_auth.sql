-- +goose Up
CREATE TABLE api_tokens (
    id UUID PRIMARY KEY,
    admin_id UUID NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
    label TEXT NOT NULL CHECK (char_length(label) BETWEEN 1 AND 120),
    token_hash BYTEA NOT NULL UNIQUE,
    scopes_json JSONB NOT NULL CHECK (jsonb_typeof(scopes_json) = 'array'),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

CREATE INDEX api_tokens_created_id ON api_tokens(created_at, id);

-- +goose Down
DROP TABLE api_tokens;
