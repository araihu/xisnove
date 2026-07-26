-- +goose Up
CREATE TABLE idempotency_records (
    principal_id TEXT NOT NULL,
    operation_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL CHECK (
        length(CAST(idempotency_key AS BLOB)) BETWEEN 1 AND 255
    ),
    request_hash TEXT NOT NULL,
    resource_kind TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    PRIMARY KEY (principal_id, operation_id, idempotency_key)
);

CREATE INDEX idempotency_records_expiry
ON idempotency_records(expires_at, principal_id, operation_id, idempotency_key);

-- +goose Down
DROP TABLE idempotency_records;
