"""Exercise the real gates against isolated committed Git fixtures."""
import json
import os
import re
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class WorkflowCoverage(unittest.TestCase):
    def workflow_jobs(self, workflow=None):
        if workflow is None:
            workflow = (ROOT.parent / ".github/workflows/ci.yml").read_text()
        workflow = workflow.split("\njobs:\n", 1)[1]
        return dict(re.findall(
            r"(?ms)^  ([a-z0-9_]+):\n(.*?)(?=^  [a-z0-9_]+:\n|\Z)", workflow))

    def test_jobs_use_linux_only(self):
        jobs = self.workflow_jobs()
        self.assertCountEqual(jobs, ["checks", "race", "e2e", "consumer"])
        for name, job in jobs.items():
            with self.subTest(job=name):
                self.assertIn("    runs-on: ubuntu-24.04", job)
                self.assertNotIn("macos-", job)
                self.assertNotIn("    matrix:", job)

    def test_checks_job_installs_tools_and_runs_offline_checks(self):
        checks = self.workflow_jobs()["checks"]
        for dependency in ("ripgrep", "apt-get", "brew install", "go install"):
            self.assertNotIn(dependency, checks)
        self.assertIn(
            "      - name: Install pinned golangci-lint\n",
            checks,
        )
        self.assertIn("GOLANGCI_LINT_VERSION: '2.12.2'", checks)
        self.assertIn('$(go env GOOS)-$(go env GOARCH).tar.gz', checks)
        self.assertIn('golangci-lint-${GOLANGCI_LINT_VERSION}-checksums.txt', checks)
        self.assertIn('test -s "$archive.sha256"', checks)
        self.assertIn('sha256sum -c "$archive.sha256"', checks)
        self.assertIn('./golangci-lint version', checks)
        self.assertIn("python3 codex/scripts/check_whitespace.py", checks)
        self.assertIn("make fmt-check", checks)
        self.assertIn("make lint", checks)
        self.assertIn("bash scripts/check-offline.sh checks", checks)
        self.assertNotIn("check-offline.sh race", checks)
        self.assertNotIn("check-offline.sh e2e", checks)

    def test_race_and_simulation_gate_run_as_separate_jobs(self):
        jobs = self.workflow_jobs()
        for name, command in [("race", "race"), ("e2e", "e2e")]:
            with self.subTest(job=name):
                self.assertIn(f"bash scripts/check-offline.sh {command}", jobs[name])
                self.assertNotIn("golangci-lint", jobs[name])
                self.assertNotIn("    needs:", jobs[name])
        self.assertIn("name: simulation-ubuntu-24.04", jobs["e2e"])
        self.assertNotIn("simulation-ubuntu-24.04", jobs["checks"])
        self.assertNotIn("simulation-ubuntu-24.04", jobs["race"])

    def test_offline_script_exposes_each_ci_task(self):
        script = (ROOT.parent / "scripts/check-offline.sh").read_text()
        self.assertIn("task=${1:-all}", script)
        for task in ("parallel", "checks", "quality", "test", "race", "e2e", "build"):
            with self.subTest(task=task):
                self.assertIn(f"  {task})\n", script)
        self.assertIn(
            "  parallel)\n    quality_checks\n    parallel_checks\n    ;;\n",
            script,
        )
        self.assertIn("local parallel_tasks=(test build)", script)
        self.assertIn("local serial_tasks=(race e2e)", script)
        self.assertIn("local all_tasks=(test race e2e build)", script)
        self.assertIn(
            'AGENT_SDK_TESTIFY_BUILD_DIR="$work/$task/simulation"',
            script,
        )
        self.assertIn('wait "$pid"', script)
        self.assertIn('if ! wait "$pid"; then', script)
        self.assertIn('for task in "${serial_tasks[@]}"; do', script)
        self.assertIn('if ! run_task_lane "$work" "$task"; then', script)
        self.assertIn(
            "  checks)\n    quality_checks\n    test_checks\n    build_checks\n    ;;\n",
            script,
        )

    def test_same_repository_pr_consumers_fetch_published_head(self):
        consumer = self.workflow_jobs()["consumer"]
        self.assertIn("    name: External consumer (pushed commit)", consumer)
        self.assertIn("    if: github.event_name != 'pull_request' || "
                      "github.event.pull_request.head.repo.full_name == github.repository", consumer)
        revision = "${{ github.event.pull_request.head.sha || github.sha }}"
        self.assertIn("      SDK_CONSUMER_SHA: " + revision, consumer)
        self.assertIn("          ref: " + revision, consumer)
        self.assertIn('bash scripts/check-consumers.sh "$SDK_CONSUMER_SHA"', consumer)
        self.assertIn('bash claude/scripts/check-managed-consumer.sh "$SDK_CONSUMER_SHA"', consumer)

    def test_external_consumer_retrieves_exact_commit_through_configured_proxy(self):
        script = (ROOT.parent / "scripts/check-consumers.sh").read_text()
        self.assertIn(
            'export GOPROXY="${SDK_CONSUMER_GOPROXY:-https://proxy.golang.org,direct}"',
            script,
        )
        self.assertIn(
            'go mod download -json "github.com/zoumo/agent-sdk-go@$revision"',
            script,
        )
        self.assertNotIn("GOPROXY=direct", script)
        self.assertIn("module.get('Origin', {}).get('Hash') == sys.argv[2]", script)


class CIGates(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name)
        self.run_cmd("git", "init", "-q", check=True)
        self.run_cmd("git", "config", "user.name", "CI fixture")
        self.run_cmd("git", "config", "user.email", "fixture@example.invalid")
        self.run_cmd("git", "config", "commit.gpgsign", "false")
        (self.repo / "sample").write_text("clean\n")
        self.initial = self.commit()

    def run_cmd(self, *args, **kwargs):
        return subprocess.run(args, cwd=self.repo, text=True, input="",
                              capture_output=True, **kwargs)

    def commit(self):
        self.run_cmd("git", "add", ".", check=True)
        self.run_cmd("git", "-c", "core.hooksPath=/dev/null", "commit", "-qm", "fixture", check=True)
        return self.run_cmd("git", "rev-parse", "HEAD", check=True).stdout.strip()

    def whitespace(self, name, event):
        path = self.repo / ".git/event.json"
        path.write_text(json.dumps(event))
        return self.run_cmd(sys.executable, str(ROOT / "scripts/check_whitespace.py"),
                            env={**os.environ, "GITHUB_EVENT_NAME": name,
                                 "GITHUB_EVENT_PATH": str(path)})

    def test_push_manual_and_pr_detect_committed_whitespace(self):
        # A clean checkout must still reject a commit with trailing whitespace.
        (self.repo / "sample").write_text("bad trailing space \n")
        head = self.commit()
        self.assertEqual(self.run_cmd("git", "diff", "--check").returncode, 0)
        events = [("push", {"before": self.initial}),
                  ("workflow_dispatch", {}),
                  ("pull_request", {"pull_request": {
                      "base": {"sha": self.initial}, "head": {"sha": head}}})]
        for name, event in events:
            with self.subTest(event=name):
                result = self.whitespace(name, event)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn("trailing whitespace", result.stdout)

    def test_push_checks_the_entire_multi_commit_range(self):
        (self.repo / "sample").write_text("bad earlier commit \n")
        self.commit()
        (self.repo / "later").write_text("clean later commit\n")
        self.commit()
        # The latest commit is clean, but the push still introduces bad whitespace.
        self.assertEqual(self.whitespace("workflow_dispatch", {}).returncode, 0)
        result = self.whitespace("push", {"before": self.initial})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("trailing whitespace", result.stdout)

    def test_initial_push_and_root_manual(self):
        for name, event in [("push", {"before": "0" * 40}), ("workflow_dispatch", {})]:
            self.assertEqual(self.whitespace(name, event).returncode, 0)
        # Include bad whitespace in a newly created branch's whole tree.
        (self.repo / "sample").write_text("bad \n")
        self.commit()
        result = self.whitespace("push", {"before": "0" * 40})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("trailing whitespace", result.stdout)

    def test_pr_uses_merge_base_not_new_base_changes(self):
        self.run_cmd("git", "checkout", "-qb", "topic", check=True)
        (self.repo / "topic").write_text("clean topic\n")
        head = self.commit()
        self.run_cmd("git", "checkout", "--detach", self.initial, check=True)
        (self.repo / "base-only").write_text("existing base whitespace \n")
        base = self.commit()
        result = self.whitespace("pull_request", {"pull_request": {
            "base": {"sha": base}, "head": {"sha": head}}})
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(self.initial + " -> " + head, result.stdout)

    def test_missing_or_invalid_push_base_fails_closed(self):
        for base in ["f" * 40, "--all"]:
            self.assertNotEqual(self.whitespace("push", {"before": base}).returncode, 0)

    def test_generation_reproduces_and_rejects_committed_drift(self):
        paths = ["scripts/generate_protocol.py", "scripts/check_generated.sh",
                 "types/schema.json", "types/types_generated.go",
                 "types/schema_generated_test.go"]
        for rel in paths:
            dest = self.repo / rel
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / rel, dest)
        self.commit()
        result = self.run_cmd("bash", "scripts/check_generated.sh")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        # Commit the drift: generation must change HEAD's file and fail git diff.
        for rel in ["types/types_generated.go", "types/schema_generated_test.go"]:
            with self.subTest(output=rel):
                target = self.repo / rel
                target.write_text(target.read_text() + "// committed drift\n")
                self.commit()
                result = self.run_cmd("bash", "scripts/check_generated.sh")
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn("committed drift", result.stdout)
                self.commit()  # commit the regenerated baseline for the next case


if __name__ == "__main__":
    unittest.main()
