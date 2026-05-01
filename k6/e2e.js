import http from 'k6/http';
import { check, sleep } from 'k6';

// End-to-end test: measure full async latency.
// POST /charges, then poll GET /charges/{ref} until completed or failed.
// This measures API -> outbox -> queue -> worker -> provider -> DB.

export const options = {
  vus: 5,
  iterations: 20,
};

export default function () {
  const id = `e2e_${__VU}_${__ITER}_${Date.now()}`;

  // 1. Create charge
  const createRes = http.post('http://localhost:8080/charges', JSON.stringify({
    amount: 5000,
    currency: 'USD',
    reference: id,
  }), {
    headers: {
      'Content-Type': 'application/json',
      'idempotency_key': id,
    },
  });

  check(createRes, {
    'create status is 202': (r) => r.status === 202,
  });

  if (createRes.status !== 202) {
    return;
  }

  // 2. Poll for completion (max 30s)
  let status = 'pending';
  let attempts = 0;
  const maxAttempts = 60;

  while (status === 'pending' && attempts < maxAttempts) {
    sleep(0.5);
    const getRes = http.get(`http://localhost:8080/charges/${id}`);

    if (getRes.status === 200) {
      const body = JSON.parse(getRes.body);
      status = body.data?.status || 'unknown';
    }

    attempts++;
  }

  check(null, {
    'payment completed or failed': () => status === 'completed' || status === 'failed',
    'payment resolved within 30s': () => attempts < maxAttempts,
  });
}
