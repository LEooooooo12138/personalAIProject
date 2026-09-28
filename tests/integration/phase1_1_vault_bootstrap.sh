#!/usr/bin/env bash
# Verify metadata only; callers can select temporary or deployed vaults.
set -euo pipefail
PERSONAL_VAULT="${PERSONAL_VAULT:-$HOME/vaults/personal}"
AGENT_VAULT="${AGENT_VAULT:-$HOME/vaults/agent}"
PYTHON="${PYTHON:-python3}"
if command -v cygpath >/dev/null 2>&1; then
    PERSONAL_VAULT="$(cygpath -u "$PERSONAL_VAULT")"
    AGENT_VAULT="$(cygpath -u "$AGENT_VAULT")"
fi
PASS=0
FAIL=0
check() {
    local desc="$1"
    shift
    if "$@"; then
        echo "  PASS: $desc"
        PASS=$((PASS + 1))
    else
        echo "  FAIL: $desc"
        FAIL=$((FAIL + 1))
    fi
}
is_git_repo() {
    git -C "$1" rev-parse --git-dir >/dev/null 2>&1
}
echo "=== Phase 1.1: Vault Bootstrap Verification ==="
REQUIRED_DIRS=(concepts entities skills references synthesis journal projects _raw _meta)
shopt -s nullglob
for vault_path in "$PERSONAL_VAULT" "$AGENT_VAULT"; do
    command_path="$vault_path"
    if command -v cygpath >/dev/null 2>&1; then
        command_path="$(cygpath -m "$vault_path")"
    fi
    echo "--- $vault_path ---"
    check "vault directory exists" test -d "$vault_path"
    for dir in "${REQUIRED_DIRS[@]}"; do
        check "subdirectory $dir exists" test -d "$vault_path/$dir"
    done
    check "AGENTS.md exists and is non-empty" test -s "$vault_path/AGENTS.md"
    check ".manifest.json exists" test -f "$vault_path/.manifest.json"
    check ".manifest.json is valid JSON" "$PYTHON" -c 'import json,sys; json.load(open(sys.argv[1], encoding="utf-8-sig"))' "$command_path/.manifest.json"
    seeds=("$vault_path/concepts/"*.md)
    check "seed count >= 4 (found ${#seeds[@]})" test "${#seeds[@]}" -ge 4
    check "vault is git repo" is_git_repo "$command_path"
done
echo "=== Results: $PASS passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
