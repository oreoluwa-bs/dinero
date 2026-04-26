-- name: GetPaymentByReference :one
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
RETURNING amount, currency, reference, idempotency_key, status, created_at;

-- name: UpdatePaymentStatus :exec
UPDATE payments
SET status = ?,
attempts = ?,
next_retry_at = ?
WHERE idempotency_key = ?;
