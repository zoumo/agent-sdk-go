"""Exercise the real gates against isolated committed Git fixtures."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


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
                 "protocol/schema.json", "protocol/types_generated.go",
                 "protocol/schema_generated_test.go"]
        for rel in paths:
            dest = self.repo / rel
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / rel, dest)
        self.commit()
        result = self.run_cmd("bash", "scripts/check_generated.sh")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        # Commit the drift: generation must change HEAD's file and fail git diff.
        for rel in ["protocol/types_generated.go", "protocol/schema_generated_test.go"]:
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
