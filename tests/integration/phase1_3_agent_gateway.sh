#!/usr/bin/env bash
set -euo pipefail
AGENT="${AGENT_URL:-http://localhost:8080}"
KEY="${AGENT_INTERNAL_KEY:?Set AGENT_INTERNAL_KEY before running live gateway checks}"
PYTHON="${PYTHON:-python3}"
echo "=== Phase 1.3: Agent Gateway Verification ==="
HEALTH=$(curl -fsS --max-time 30 "$AGENT/health")
printf '%s' "$HEALTH" | "$PYTHON" -c 'import json,sys; assert json.load(sys.stdin)["status"] == "ok"'
echo "PASS: health = ok"
STATUS=$(curl -sS --max-time 30 -o /dev/null -w "%{http_code}" -X POST "$AGENT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d '{"model":"auto","messages":[{"role":"user","content":"hi"}]}')
if [ "$STATUS" != "401" ]; then
    echo "FAIL: expected anonymous 401, got $STATUS"
    exit 1
fi
echo "PASS: auth rejection = 401"
STATUS=$(curl -sS --max-time 180 -o /dev/null -w "%{http_code}" -X POST "$AGENT/v1/chat/completions" \
    -H "Content-Type: application/json" -H "Authorization: Bearer $KEY" \
    -d '{"model":"auto","messages":[{"role":"user","content":"hello"}]}')
if [ "$STATUS" != "200" ]; then
    echo "FAIL: expected authenticated 200, got $STATUS"
    exit 1
fi
echo "PASS: authenticated chat = 200"
VAULT=$(curl -fsS --max-time 30 -H "Authorization: Bearer $KEY" "$AGENT/internal/vault/status")
printf '%s' "$VAULT" | "$PYTHON" -c 'import json,sys; d=json.load(sys.stdin); assert "Personal" in d and "Agent" in d'
echo "PASS: authenticated vault status returned"
echo "=== Phase 1.3 PASSED ==="
