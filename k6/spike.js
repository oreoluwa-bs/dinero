import http from 'k6/http';
import { check, sleep } from 'k6';

// Spike test: sudden burst to find the breaking point.
// SQLite lock contention and queue backlog should become visible.

export const options = {
  stages: [
    { duration: '10s', target: 50 },   // rapid ramp
    { duration: '30s', target: 50 },   // hold
    { duration: '10s', target: 0 },    // ramp down
  ],
};

export default function () {
  const id = `spike_${__VU}_${__ITER}_${Date.now()}`;

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
  });

  sleep(0.1);
}
