#!/usr/bin/env bash
# Phase 1.4: Python Inference Service verification
set -euo pipefail

INFER="http://localhost:8000"

echo "=== Phase 1.4: Inference Service Verification ==="

# 1. Health
echo "--- /health ---"
curl -s "$INFER/health" | python3 -c "import json,sys; assert json.load(sys.stdin)['status']=='ok'"
echo "PASS: health = ok"

# 2. Chat
echo "--- /v1/chat/completions ---"
CHAT=$(curl -s -X POST "$INFER/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -d '{"model":"auto","messages":[{"role":"user","content":"hi"}]}')
echo "$CHAT" | python3 -c "import json,sys; d=json.load(sys.stdin); assert 'choices' in d"
echo "PASS: chat returned choices"

# 3. Embedding
echo "--- /v1/embeddings ---"
EMBED=$(curl -s -X POST "$INFER/v1/embeddings" \
  -H "Content-Type: application/json" \
  -d '{"model":"bge-m3","input":"test"}')
echo "$EMBED" | python3 -c "import json,sys; d=json.load(sys.stdin); assert len(d['data'][0]['embedding'])==1024"
echo "PASS: embedding dim = 1024"

echo "=== Phase 1.4 PASSED ==="
