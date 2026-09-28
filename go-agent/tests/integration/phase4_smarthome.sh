#!/usr/bin/env bash
# Live endpoint smoke checks only. Does not confirm rules or control devices.
# Successful empty responses do not prove HA connectivity or hardware operation.
set -euo pipefail
AGENT_URL="${AGENT_URL:-http://localhost:8080}"
API_KEY="${AGENT_INTERNAL_KEY:-${API_KEY:-}}"
: "${API_KEY:?Set AGENT_INTERNAL_KEY (or API_KEY) before running live HA checks}"
PYTHON="${PYTHON:-python3}"
echo "=== Phase 4 Integration Test: Smart Home Endpoints ==="
HEALTH=$(curl -fsS --max-time 30 "$AGENT_URL/health")
printf '%s' "$HEALTH" | "$PYTHON" -c 'import sys,json; assert json.load(sys.stdin)["status"] == "ok"'
echo "PASS: health"
STATUS=$(curl -fsS --max-time 30 -H "Authorization: Bearer $API_KEY" "$AGENT_URL/internal/smarthome/status")
printf '%s' "$STATUS" | "$PYTHON" -c 'import sys,json; assert json.load(sys.stdin)["enabled"] is True'
echo "PASS: smart home enabled"
DEVICES=$(curl -fsS --max-time 30 -H "Authorization: Bearer $API_KEY" "$AGENT_URL/internal/smarthome/devices")
printf '%s' "$DEVICES" | "$PYTHON" -c 'import sys,json; d=json.load(sys.stdin); assert "devices" in d and (d["devices"] is None or isinstance(d["devices"], list))'
echo "PASS: devices endpoint"
SUGGESTIONS=$(curl -fsS --max-time 30 -H "Authorization: Bearer $API_KEY" "$AGENT_URL/internal/smarthome/suggestions")
printf '%s' "$SUGGESTIONS" | "$PYTHON" -c 'import sys,json; d=json.load(sys.stdin); assert "suggestions" in d and (d["suggestions"] is None or isinstance(d["suggestions"], list))'
echo "PASS: suggestions endpoint"
ANALYZE=$(curl -fsS --max-time 90 -X POST -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" -d '{"days":7}' "$AGENT_URL/internal/smarthome/analyze")
printf '%s' "$ANALYZE" | "$PYTHON" -c 'import sys,json; assert isinstance(json.load(sys.stdin)["report"], dict)'
echo "PASS: analysis endpoint"
VAULT=$(curl -fsS --max-time 30 -H "Authorization: Bearer $API_KEY" "$AGENT_URL/internal/vault/status")
printf '%s' "$VAULT" | "$PYTHON" -c 'import sys,json; d=json.load(sys.stdin); assert "Personal" in d and "Agent" in d'
echo "PASS: vault status"
CHAT=$(curl -fsS --max-time 180 -X POST -H "Authorization: Bearer $API_KEY" -H "Content-Type: application/json" \
    -d '{"model":"auto","messages":[{"role":"user","content":"hello"}]}' "$AGENT_URL/v1/chat/completions")
printf '%s' "$CHAT" | "$PYTHON" -c 'import sys,json; assert json.load(sys.stdin)["choices"][0]["message"]["content"].strip()'
echo "PASS: chat endpoint"
echo "=== Phase 4 endpoint smoke checks PASSED (hardware not verified) ==="
