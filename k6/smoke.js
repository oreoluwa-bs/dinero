import http from 'k6/http';
import { check, sleep } from 'k6';

// Smoke test: low volume, fast feedback.
// Verifies the entire stack (API -> outbox -> queue -> worker) is healthy.

export const options = {
  vus: 1,
  iterations: 10,
};

export default function () {
  const id = `smoke_${__VU}_${__ITER}_${Date.now()}`;

  const res = http.post('http://localhost:8080/charges', JSON.stringify({
    amount: 5000,
    currency: 'USD',
    reference: id,
  }), {
    headers: {
      'Content-Type': 'application/json',
      'idempotency_key': id,
    },
  });

  check(res, {
    'status is 202': (r) => r.status === 202,
    'response time < 500ms': (r) => r.timings.duration < 500,
  });

  sleep(0.5);
}
