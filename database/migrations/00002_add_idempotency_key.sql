-- +goose Up
-- +goose StatementBegin
ALTER TABLE payments
ADD COLUMN idempotency_key TEXT;

CREATE UNIQUE INDEX idx_payments_idempotency_key ON payments(idempotency_key);
-- CREATE UNIQUE INDEX idx_payments_reference ON payments(reference);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- DROP INDEX idx_payments_reference;
DROP INDEX idx_payments_idempotency_key;
ALTER TABLE payments
DROP COLUMN idempotency_key;
-- +goose StatementEnd
