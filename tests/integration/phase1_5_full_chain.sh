#!/usr/bin/env bash
# Phase 1.5: End-to-end full chain verification
set -euo pipefail

AGENT="${AGENT_URL:-http://localhost:8080}"
KEY="${AGENT_INTERNAL_KEY:?Set AGENT_INTERNAL_KEY before running live chain checks}"
PYTHON="${PYTHON:-python3}"

echo "=== Phase 1.5: End-to-End Chain ==="

echo "--- full chain: curl -> Agent -> Inference -> Ollama ---"
RESP=$(curl -fsS --max-time 180 -X POST "$AGENT/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $KEY" \
  -d '{"model":"auto","messages":[{"role":"user","content":"say hello in chinese"}]}')

CONTENT=$(echo "$RESP" | "$PYTHON" -c "import json,sys; print(json.load(sys.stdin)['choices'][0]['message']['content'])")
if [ -z "$CONTENT" ]; then
  echo "FAIL: empty response"
  exit 1
fi
echo "PASS: got response ($(echo "$CONTENT" | wc -c) bytes)"

echo "--- vault context: knowledge query ---"
RESP=$(curl -fsS --max-time 180 -X POST "$AGENT/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $KEY" \
  -d '{"model":"auto","messages":[{"role":"user","content":"what is RAG?"}]}')
echo "$RESP" | "$PYTHON" -c "import json,sys; d=json.load(sys.stdin); assert len(d['choices'][0]['message']['content'])>0"
echo "PASS: RAG query returned content"

echo "--- models list ---"
MODELS=$(curl -fsS --max-time 30 "$AGENT/v1/models" -H "Authorization: Bearer $KEY")
echo "$MODELS" | "$PYTHON" -c "import json,sys; d=json.load(sys.stdin); assert len(d['data'])>0"
echo "PASS: models list returned"

echo "=== Phase 1.5 PASSED ==="
