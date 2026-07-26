-- +goose Up
CREATE TABLE idempotency_records (
    principal_id TEXT NOT NULL,
    operation_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL CHECK (octet_length(idempotency_key) BETWEEN 1 AND 255),
    request_hash TEXT NOT NULL,
    resource_kind TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (principal_id, operation_id, idempotency_key)
);

CREATE INDEX idempotency_records_expiry
ON idempotency_records(expires_at, principal_id, operation_id, idempotency_key);

-- +goose Down
DROP TABLE idempotency_records;
