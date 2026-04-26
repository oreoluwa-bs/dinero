-- name: GetPaymentByReference :one
SELECT
    id,
    amount,
    currency,
    idempotency_key,
    reference,
    status,
    attempts,
    next_retry_at,
    created_at
FROM payments
WHERE reference = ? LIMIT 1;

-- name: GetPaymentByIdempotency :one
SELECT
    amount,
    currency,
    idempotency_key,
    reference,
    status,
    attempts,
    next_retry_at,
    created_at
FROM payments
WHERE idempotency_key = ? LIMIT 1;


-- name: CreatePayment :one
INSERT INTO payments (
  amount, currency, reference, status, idempotency_key
) VALUES (
  ?, ?, ?, ?, ?
)
RETURNING id, amount, currency, reference, idempotency_key, status, created_at;
-- name: GetFailedPaymentsForRetry :many
SELECT idempotency_key, reference
FROM payments
WHERE status = 'failed'
  AND next_retry_at IS NOT NULL
  AND next_retry_at <= datetime('now')
  AND attempts < 5
LIMIT 50;

-- name: UpdatePaymentStatus :exec
UPDATE payments
SET status = ?,
attempts = ?,
next_retry_at = ?
WHERE idempotency_key = ?;
