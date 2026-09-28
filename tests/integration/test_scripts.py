"""Offline regression tests. All services and vaults are temporary fixtures."""
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
BASH = os.environ.get("INTEGRATION_BASH") or shutil.which("bash")
if os.name == "nt" and not os.environ.get("INTEGRATION_BASH"):
    BASH = "C:/Program Files/Git/bin/bash.exe"
PHASES = [
    "tests/integration/phase1_1_vault_bootstrap.sh",
    "tests/integration/phase1_2_ollama.sh",
    "tests/integration/phase1_3_agent_gateway.sh",
    "tests/integration/phase1_5_full_chain.sh",
    "go-agent/tests/integration/phase4_smarthome.sh",
]


class IntegrationScriptTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="agent-script-fixture-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.env = os.environ.copy()
        self.env.update(PYTHON=Path(sys.executable).as_posix())
        for key in ("AGENT_INTERNAL_KEY", "API_KEY", "PERSONAL_VAULT", "AGENT_VAULT"):
            self.env.pop(key, None)

    def run_script(self, script, env=None):
        # Git Bash may prepend its own programs during startup. Install and verify
        # the curl stub inside Bash before sourcing any script under test.
        launcher = '''if [ -n "${FIXTURE_BIN:-}" ]; then
  fixture_bin="$FIXTURE_BIN"
  if command -v cygpath >/dev/null 2>&1; then fixture_bin="$(cygpath -u "$fixture_bin")"; fi
  export PATH="$fixture_bin:$PATH"
  hash -r
  [ "$(command -v curl)" = "$fixture_bin/curl" ] || exit 99
fi
source "$1"'''
        return subprocess.run(
            [BASH, "-c", launcher, Path(script).as_posix(), Path(script).as_posix()], cwd=self.root,
            env=self.env | (env or {}), text=True, encoding="utf-8",
            errors="replace", stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
            timeout=20,
        )

    def write(self, path, content):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8", newline="\n")
        path.chmod(0o755)

    def runner_fixture(self, failing=None, missing=None):
        runner = self.root / "tests/integration/run_all.sh"
        self.write(runner, (ROOT / "tests/integration/run_all.sh").read_text(encoding="utf-8-sig"))
        trace = self.root / "trace.txt"
        self.env["TRACE"] = trace.as_posix()
        for phase in PHASES:
            if phase != missing:
                self.write(self.root / phase, '#!/usr/bin/env bash\nprintf "%s\\n" "' + phase + '" >> "$TRACE"\nexit ' + ("7" if phase == failing else "0") + "\n")
        return runner, trace

    def test_runner_executes_all_phases_in_order_and_counts_deprecated_skip(self):
        runner, trace = self.runner_fixture()
        result = self.run_script(runner)
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertEqual(trace.read_text().splitlines(), PHASES, result.stdout)
        self.assertIn("PASS: 5", result.stdout)
        self.assertIn("SKIP: 1", result.stdout)

    def test_runner_continues_after_failure_and_returns_nonzero(self):
        runner, trace = self.runner_fixture(failing=PHASES[0])
        result = self.run_script(runner)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertEqual(trace.read_text().splitlines(), PHASES, result.stdout)
        self.assertIn("PASS: 4", result.stdout)
        self.assertIn("FAIL: 1", result.stdout)

    def test_runner_missing_required_phase_is_failure(self):
        runner, _ = self.runner_fixture(missing=PHASES[-1])
        result = self.run_script(runner)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("FAIL: 1", result.stdout)

    def make_vault(self, name, valid=True):
        path = self.root / name
        for directory in ("concepts", "entities", "skills", "references", "synthesis", "journal", "projects", "_raw", "_meta"):
            (path / directory).mkdir(parents=True)
        self.write(path / "AGENTS.md", "fixture rules\n")
        self.write(path / ".manifest.json", "{}" if valid else "not json")
        if valid:
            for i in range(4):
                self.write(path / "concepts" / f"seed{i}.md", "fixture\n")
        subprocess.run(["git", "init", "-q", str(path)], check=True, capture_output=True)
        return path

    def test_bootstrap_supports_path_and_interpreter_overrides(self):
        personal = self.make_vault("personal vault's fixture")
        agent = self.make_vault("agent fixture")
        result = self.run_script(ROOT / PHASES[0], {"PERSONAL_VAULT": personal.as_posix(), "AGENT_VAULT": agent.as_posix()})
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertIn("30 passed, 0 failed", result.stdout)

    def test_bootstrap_aggregates_missing_seeds_and_invalid_json(self):
        personal = self.make_vault("personal invalid", valid=False)
        agent = self.make_vault("agent valid")
        result = self.run_script(ROOT / PHASES[0], {"PERSONAL_VAULT": personal.as_posix(), "AGENT_VAULT": agent.as_posix()})
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("=== Results:", result.stdout)
        self.assertIn("2 failed", result.stdout)

    def fake_services(self):
        bindir = self.root / "bin"
        self.env["FIXTURE_BIN"] = bindir.as_posix()
        self.env["CURL_LOG"] = (self.root / "curl.log").as_posix()
        # Every curl invocation is intercepted; no real network is reachable.
        self.write(bindir / "curl", r'''#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >> "$CURL_LOG"
url=""; auth=0; code=0
for arg in "$@"; do
  case "$arg" in
    http://*|https://*) url="$arg" ;;
    'Authorization: Bearer fixture-token') auth=1 ;;
    '%{http_code}') code=1 ;;
  esac
done
if [[ "$code" == 1 ]]; then
  if [[ "$auth" == 1 ]]; then printf '200'; else printf '401'; fi
  exit 0
fi
if [[ "$url" != */health && "$auth" != 1 ]]; then
  printf '{"error":"missing fixture authorization"}'
  exit 22
fi
if [[ -n "${FAIL_ENDPOINT:-}" && "$url" == *"$FAIL_ENDPOINT" ]]; then
  printf '{"wrong_response":true}'
  exit 0
fi
case "$url" in
  */health) printf '{"status":"ok"}' ;;
  */internal/vault/status) printf '{"Personal":{},"Agent":{}}' ;;
  */internal/smarthome/status) printf '{"enabled":true}' ;;
  */internal/smarthome/devices) printf '{"devices":[]}' ;;
  */internal/smarthome/suggestions) printf '{"suggestions":[]}' ;;
  */internal/smarthome/analyze) printf '{"report":{}}' ;;
  */v1/chat/completions) printf '{"choices":[{"message":{"content":"fixture answer"}}]}' ;;
  */v1/models) printf '{"data":[{"id":"fixture"}]}' ;;
  *) printf 'unexpected fixture URL' >&2; exit 98 ;;
esac
''')
        for name in ("python", "python3"):
            self.write(bindir / name, '#!/usr/bin/env bash\nexec "$PYTHON" "$@"\n')

    def test_gateway_authenticates_management_request(self):
        self.fake_services()
        result = self.run_script(ROOT / PHASES[2], {"AGENT_INTERNAL_KEY": "fixture-token", "AGENT_URL": "http://fixture.invalid"})
        self.assertEqual(result.returncode, 0, result.stdout)
        calls = Path(self.env["CURL_LOG"]).read_text()
        self.assertIn("http://fixture.invalid/internal/vault/status", calls)

    def test_live_scripts_require_explicit_key_before_network(self):
        self.fake_services()
        for phase in (PHASES[2], PHASES[3], PHASES[4]):
            with self.subTest(phase=phase):
                result = self.run_script(ROOT / phase)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertFalse(Path(self.env["CURL_LOG"]).exists(), "network attempted without explicit key")

    def test_phase4_rejects_invalid_payload_instead_of_echoing_success(self):
        self.fake_services()
        result = self.run_script(ROOT / PHASES[4], {"AGENT_INTERNAL_KEY": "fixture-token", "API_KEY": "fixture-token", "FAIL_ENDPOINT": "/internal/smarthome/devices"})
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertNotIn("Integration Test Complete", result.stdout)

    def test_phase4_passes_with_valid_offline_responses(self):
        self.fake_services()
        result = self.run_script(ROOT / PHASES[4], {"AGENT_INTERNAL_KEY": "fixture-token", "AGENT_URL": "http://fixture.invalid"})
        self.assertEqual(result.returncode, 0, result.stdout)
        calls = Path(self.env["CURL_LOG"]).read_text()
        self.assertNotIn("/confirm", calls)
        self.assertNotIn("/api/services", calls)


if __name__ == "__main__":
    unittest.main()
