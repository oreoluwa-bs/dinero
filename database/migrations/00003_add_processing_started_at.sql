-- +goose Up
-- +goose StatementBegin
ALTER TABLE payments
ADD COLUMN processing_started_at TEXT;

CREATE INDEX idx_payments_processing_started_at ON payments(status, processing_started_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_payments_processing_started_at;
ALTER TABLE payments DROP COLUMN processing_started_at;
-- +goose StatementEnd
