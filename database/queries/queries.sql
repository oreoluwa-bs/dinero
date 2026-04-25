-- name: GetPaymentByReference :one
SELECT
    amount,
    currency,
    idempotency_key,
    reference,
    status,
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
