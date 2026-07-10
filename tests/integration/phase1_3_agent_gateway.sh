#!/usr/bin/env bash
# Phase 1.3: Go Agent gateway verification
set -euo pipefail

AGENT="http://localhost:8080"
KEY="${AGENT_INTERNAL_KEY:-dev-key-123}"

echo "=== Phase 1.3: Agent Gateway Verification ==="

# 1. Health check
echo "--- /health ---"
HEALTH=$(curl -s "$AGENT/health")
if [ "$(echo "$HEALTH" | python3 -c "import json,sys; print(json.load(sys.stdin)['status'])")" != "ok" ]; then
  echo "FAIL: health check"
  exit 1
fi
echo "PASS: health = ok"

# 2. No auth -> 401
echo "--- auth: no token ---"
STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$AGENT/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -d '{"model":"gemma4:12b","messages":[{"role":"user","content":"hi"}]}')
if [ "$STATUS" != "401" ]; then
  echo "FAIL: expected 401, got $STATUS"
  exit 1
fi
echo "PASS: auth rejection = 401"

# 3. Correct auth -> 200
echo "--- auth: valid token ---"
STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$AGENT/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $KEY" \
  -d '{"model":"gemma4:12b","messages":[{"role":"user","content":"hello"}]}')
if [ "$STATUS" != "200" ]; then
  echo "FAIL: expected 200, got $STATUS"
  exit 1
fi
echo "PASS: authenticated chat = 200"

# 4. Vault status
echo "--- /internal/vault/status ---"
VAULT=$(curl -s "$AGENT/internal/vault/status")
echo "$VAULT" | python3 -c "import json,sys; d=json.load(sys.stdin); assert 'Personal' in d; assert 'Agent' in d"
echo "PASS: vault status returned"

echo "=== Phase 1.3 PASSED ==="
