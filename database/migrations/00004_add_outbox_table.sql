-- +goose Up
-- +goose StatementBegin
CREATE TABLE outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    topic TEXT NOT NULL,
    payload BLOB NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    sent_at TEXT,
    error_count INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_outbox_sent_at ON outbox(sent_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_outbox_sent_at;
DROP TABLE outbox;
-- +goose StatementEnd
