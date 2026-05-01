import http from 'k6/http';
import { check, sleep } from 'k6';

// Load test: steady-state traffic to observe baseline behavior.
// Watch Grafana for HTTP latency, active processing, and payment throughput.

export const options = {
  stages: [
    { duration: '30s', target: 10 },   // ramp up
    { duration: '4m', target: 10 },    // steady state
    { duration: '30s', target: 0 },    // ramp down
  ],
};

export default function () {
  const id = `load_${__VU}_${__ITER}_${Date.now()}`;

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

  sleep(1);
}
