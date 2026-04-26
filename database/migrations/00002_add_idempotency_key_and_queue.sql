-- +goose Up
-- +goose StatementBegin
ALTER TABLE payments
ADD COLUMN idempotency_key TEXT;

ALTER TABLE payments
ADD COLUMN next_retry_at TEXT;

ALTER TABLE payments
ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX idx_payments_idempotency_key ON payments(idempotency_key);
-- CREATE UNIQUE INDEX idx_payments_reference ON payments(reference);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- DROP INDEX idx_payments_reference;
DROP INDEX idx_payments_idempotency_key;
ALTER TABLE payments
DROP COLUMN idempotency_key;
ALTER TABLE payments
DROP COLUMN attempts;
ALTER TABLE payments
DROP COLUMN next_retry_at;
-- +goose StatementEnd
