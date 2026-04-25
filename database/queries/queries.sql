-- name: GetPaymentByReference :one
SELECT
    amount,
    currency,
    reference,
    status,
    created_at
FROM payments
WHERE reference = ? LIMIT 1;

-- name: CreatePayment :one
INSERT INTO payments (
  amount, currency, reference, status
) VALUES (
  ?, ?, ?, ?
)
RETURNING amount, currency, reference, status, created_at;
