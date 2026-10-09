#!/usr/bin/env bash
# Phase 1.5: End-to-end full chain verification
set -euo pipefail

AGENT="${AGENT_URL:-http://localhost:8080}"
KEY="${AGENT_INTERNAL_KEY:?Set AGENT_INTERNAL_KEY before running live chain checks}"
PYTHON="${PYTHON:-python3}"
export RAG_TEST_QUERY="${RAG_TEST_QUERY:?Set RAG_TEST_QUERY to a known vault fact query}"
export RAG_EXPECTED_SOURCE_PATH="${RAG_EXPECTED_SOURCE_PATH:?Set RAG_EXPECTED_SOURCE_PATH to its vault-relative source path}"
export RAG_EXPECTED_TEXT="${RAG_EXPECTED_TEXT:?Set RAG_EXPECTED_TEXT to a unique fact in that source}"

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
REQUEST=$("$PYTHON" -c 'import json,os; print(json.dumps({"model":"auto","messages":[{"role":"user","content":os.environ["RAG_TEST_QUERY"]}]}))')
RESP=$(curl -fsS --max-time 180 -X POST "$AGENT/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $KEY" \
  -d "$REQUEST")
printf '%s' "$RESP" | "$PYTHON" -c '
import json,os,sys
d = json.load(sys.stdin)
content = d["choices"][0]["message"]["content"]
if os.environ["RAG_EXPECTED_TEXT"] not in content:
    sys.exit("FAIL: expected unique vault fact absent from answer")
sources = d.get("sources", [])
if not isinstance(sources, list) or not any(isinstance(s, dict) and s.get("path") == os.environ["RAG_EXPECTED_SOURCE_PATH"] for s in sources):
    sys.exit("FAIL: expected vault source absent from response")
'
echo "PASS: RAG fact and source verified"

echo "--- models list ---"
MODELS=$(curl -fsS --max-time 30 "$AGENT/v1/models" -H "Authorization: Bearer $KEY")
echo "$MODELS" | "$PYTHON" -c "import json,sys; d=json.load(sys.stdin); assert len(d['data'])>0"
echo "PASS: models list returned"

echo "=== Phase 1.5 PASSED ==="
