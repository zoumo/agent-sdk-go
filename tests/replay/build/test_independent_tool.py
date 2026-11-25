"""Check that Replay can build without the SDK clients, adapters, or test hosts."""

import os
from pathlib import Path
import subprocess
import unittest


ROOT = Path(__file__).resolve().parents[3]
MODULE = "github.com/zoumo/agent-sdk-go"


class IndependentToolTests(unittest.TestCase):
    def test_replay_does_not_link_sdk_clients_adapters_or_test_hosts(self):
        env = dict(os.environ, GOENV="off", GOTOOLCHAIN="local", GOPROXY="off", GOSUMDB="off", GOWORK="off")
        go = env.get("GO", "go")
        output = subprocess.check_output([go, "list", "-deps", "./replay", "./cmd/agent-replay"],
                                         cwd=ROOT, env=env, text=True, timeout=120)
        packages = output.splitlines()
        self.assertIn(f"{MODULE}/replay", packages)
        self.assertIn(f"{MODULE}/cmd/agent-replay", packages)
        disallowed = (
            f"{MODULE}/agent", f"{MODULE}/client", f"{MODULE}/internal/testsupport", f"{MODULE}/tests",
            "github.com/zoumo/agent-sdk-testify",
        )
        self.assertEqual([], [package for package in packages
                              if any(package == root or package.startswith(root + "/") for root in disallowed)])
