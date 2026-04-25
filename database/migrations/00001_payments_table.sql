-- +goose Up
-- +goose StatementBegin
CREATE TABLE payments (
    id   INTEGER PRIMARY KEY AUTOINCREMENT,
    amount BIGINT NOT NULL,
    currency TEXT NOT NULL DEFAULT 'USD',
    reference TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS payments;
-- +goose StatementEnd
