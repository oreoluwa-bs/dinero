#!/usr/bin/env bash
set -euo pipefail

BASE="${BASE:-http://localhost:8080/charges}"
COUNT="${COUNT:-10}"
RUN_ID="run_$(date +%s)"

echo "=== Dinero Stress Test (${RUN_ID}) ==="
echo "Sending $COUNT charges..."
echo ""

for i in $(seq 1 "$COUNT"); do
  ref="${RUN_ID}_$i"
  key="${RUN_ID}_$i"

  echo "--- Request $i ---"
  curl -s -w "\nHTTP %{http_code}\n" -X POST "$BASE" \
    -H "Idempotency-Key: $key" \
    -H "Content-Type: application/json" \
    -d "{\"amount\":$((1000 + i)),\"currency\":\"USD\",\"reference\":\"$ref\"}"
  echo ""
done

echo ""
echo "All submitted. Polling for completion..."
echo ""

for i in $(seq 1 "$COUNT"); do
  ref="${RUN_ID}_$i"
  for attempt in $(seq 1 15); do
    resp=$(curl -s "$BASE/$ref")
    status=$(echo "$resp" | grep -o '"status":"[^"]*"' | cut -d'"' -f4)

    if [ "$status" = "completed" ] || [ "$status" = "failed" ]; then
      echo "[$ref] $status"
      break
    fi
    sleep 1
  done
  if [ -z "$status" ] || { [ "$status" != "completed" ] && [ "$status" != "failed" ]; }; then
    echo "[$ref] TIMEOUT"
  fi
done

echo ""
echo "=== Done ==="
