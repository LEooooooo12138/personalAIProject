#!/usr/bin/env bash
# Phase 1.2: Ollama model verification
set -euo pipefail

OLLAMA="http://localhost:11434"

echo "=== Phase 1.2: Ollama Verification ==="

# 1. Chat completion
echo "--- gemma4:12b chat ---"
CHAT_RESP=$(curl -s -X POST "$OLLAMA/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -d '{"model":"gemma4:12b","messages":[{"role":"user","content":"hi"}]}')
CHAT_CONTENT=$(echo "$CHAT_RESP" | python3 -c "import json,sys; print(json.load(sys.stdin)['choices'][0]['message']['content'])")
if [ -z "$CHAT_CONTENT" ]; then
  echo "FAIL: empty chat response"
  exit 1
fi
echo "PASS: chat returned content"

# 2. Embedding
echo "--- bge-m3 embedding ---"
EMBED_RESP=$(curl -s -X POST "$OLLAMA/v1/embeddings" \
  -H "Content-Type: application/json" \
  -d '{"model":"bge-m3","input":"test"}')
EMBED_DIM=$(echo "$EMBED_RESP" | python3 -c "import json,sys; print(len(json.load(sys.stdin)['data'][0]['embedding']))")
if [ "$EMBED_DIM" != "1024" ]; then
  echo "FAIL: embedding dim = $EMBED_DIM, want 1024"
  exit 1
fi
echo "PASS: embedding dimension = 1024"

echo "=== Phase 1.2 PASSED ==="
