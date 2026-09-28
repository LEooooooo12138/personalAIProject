#!/usr/bin/env bash
# Explicit live integration suite. CI runs test_scripts.py with offline fixtures.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
TESTS=(
    "tests/integration/phase1_1_vault_bootstrap.sh"
    "tests/integration/phase1_2_ollama.sh"
    "tests/integration/phase1_3_agent_gateway.sh"
    "tests/integration/phase1_5_full_chain.sh"
    "go-agent/tests/integration/phase4_smarthome.sh"
)
PASS=0
FAIL=0
SKIP=1
echo "=== Integration Test Regression Suite ==="
echo "SKIP: Phase 1.4 (Python service replaced by Go native client)"
for script in "${TESTS[@]}"; do
    script_path="$ROOT_DIR/$script"
    echo ">>> $script"
    if [ ! -f "$script_path" ]; then
        echo "    FAIL: required script not found"
        FAIL=$((FAIL + 1))
    elif bash "$script_path" 2>&1 | sed 's/^/    /'; then
        echo "    PASS: $script"
        PASS=$((PASS + 1))
    else
        echo "    FAIL: $script"
        FAIL=$((FAIL + 1))
    fi
done
echo "=== Results ==="
echo "  PASS: $PASS"
echo "  FAIL: $FAIL"
echo "  SKIP: $SKIP (phase1_4 deprecated)"
if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
echo "All selected integration tests passed."
