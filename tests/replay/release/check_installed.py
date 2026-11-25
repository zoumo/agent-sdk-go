#!/usr/bin/env python3
"""Install a native distribution and exercise its CLI and drivers offline."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile


ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "scripts/replay"))
from release import ARCHIVE_DOCS, CODEX, GO, NPM_DRIVERS, TOOL, archive_name, digest, package_filename


def run(root, environment, *args):
    return subprocess.run(args, cwd=root, env=environment, capture_output=True, check=True, timeout=240, text=True)


def install_native(root, dist, manifest):
    target = manifest["files"][0]["target"].replace("/", "-")
    archive = dist / archive_name(manifest["toolVersion"], target)
    paths = {name: root / name for name in (TOOL, CODEX)}
    with tarfile.open(archive) as tar:
        assert sorted(tar.getnames()) == sorted([*paths, *ARCHIVE_DOCS]), tar.getnames()
        for name, path in paths.items():
            entry = tar.getmember(name)
            assert entry.isfile() and entry.mode == 0o755, entry
            path.write_bytes(tar.extractfile(entry).read())
            path.chmod(entry.mode)
        for name, source in ARCHIVE_DOCS.items():
            entry = tar.getmember(name)
            assert entry.isfile() and entry.mode == 0o644, entry
            assert tar.extractfile(entry).read() == source.read_bytes(), name
    assert digest(paths[TOOL]) == manifest["files"][0]["binarySHA256"]
    assert digest(paths[CODEX]) == manifest["files"][0]["codexSHA256"]
    return paths[TOOL], paths[CODEX]


def check_cli(root, env, binary, manifest):
    version = manifest["toolVersion"]
    assert run(root, env, binary, "version").stdout.strip() == f"{TOOL} {version}"
    assert run(root, env, binary, "--version").stdout.strip() == f"{TOOL} {version}"
    assert TOOL in run(root, env, binary, "--help").stdout
    identity = run(root, os.environ, GO, "version", "-m", binary).stdout
    assert "path\tgithub.com/zoumo/agent-sdk-go/cmd/agent-replay\n" in identity, identity
    assert f"vcs.revision={manifest['sourceRevision']}\n" in identity, identity
    assert "vcs.modified=false\n" in identity, identity
    system, arch = manifest["files"][0]["target"].split("/")
    assert f"GOOS={system}\n" in identity and f"GOARCH={arch}\n" in identity, identity
    # Reuse the public lifecycle suites with the extracted binary, without rebuilding the CLI.
    go_env = dict(os.environ, AGENT_REPLAY_BIN=str(binary), AGENT_REPLAY_VERSION=version,
                  GOPROXY="off", GOSUMDB="off", GOWORK="off", GOTOOLCHAIN="local")
    result = run(ROOT, go_env, GO, "test", "-count=1", "-v", "./tests/replay/e2e", "./tests/replay/e2e/mock",
                 "-run", "^Test(PackageLifecycleInstalledCLI|BuiltinAndExternalStrictReplay)$")
    for case in ("TestPackageLifecycleInstalledCLI", "TestBuiltinAndExternalStrictReplay"):
        assert f"--- PASS: {case} " in result.stdout, result.stdout
    print(result.stdout, end="", flush=True)


def verify(root, env, binary, driver, row):
    with tempfile.TemporaryDirectory(dir=root) as work:
        fixture = Path(work) / "fixture"
        run(work, env, binary, "prepare", "--agent", row["provider"], "--case", row["caseId"], "--fixture-dir", fixture)
        run(work, env, binary, "official", "verify", "--fixture-dir", fixture, "--timeout", "60s")
        verification, = fixture.glob("packages/*/verify/*/verification.json")
        result = json.loads(verification.read_text())
        assert result["status"] == "passed" and result["cleanupComplete"], result
        assert result["driver"] == {"path": str(driver.resolve()), "sha256": digest(driver)}, result["driver"]
        assert result["material"]["sha256"] == row["materialSHA256"], result["material"]
        report = json.loads(verification.with_name("replay-report.json").read_text())
        outcome = json.loads(verification.with_name("driver-result.json").read_text())
        assert report["officialSDK"] == outcome["sdk"] == row["officialSDK"], outcome
    print(f"{row['provider']}/{row['caseId']} passed with {driver.name}", flush=True)


def install_npm(root, dist, provider, entrypoint, node, npm):
    prefix = root / provider
    source = NPM_DRIVERS[provider]
    name = json.loads((source / "package.json").read_text())["name"]
    env = {"PATH": str(node.parent) + os.pathsep + os.defpath, "HOME": str(root),
           "npm_config_cache": str(root / "npm-cache"), "npm_config_update_notifier": "false"}
    run(root, env, npm, "install", "--offline", "--ignore-scripts", "--omit=optional", "--no-audit", "--no-fund",
        "--prefix", prefix, dist / package_filename(source))
    bin_dir = prefix / "node_modules/.bin"
    driver = bin_dir / name
    package = prefix / "node_modules" / name
    assert driver.resolve(strict=True) == package / entrypoint, driver
    env["PATH"] = str(bin_dir) + os.pathsep + str(node.parent)
    assert shutil.which(name, path=env["PATH"]) == str(driver)
    return driver, package, env


def check_drivers(root, dist, binary, codex):
    builtin = json.loads(ARCHIVE_DOCS["builtin-manifest.json"].read_text())
    available = {(row["provider"], row["caseId"]): row for row in builtin["coverage"] if row["available"]}
    codex_cases = [row for (provider, _), row in available.items() if provider == "codex"]
    assert codex_cases, "no builtin Codex cases"
    for row in codex_cases:
        verify(root, {"PATH": str(root), "HOME": str(root)}, binary, codex, row)

    node = shutil.which("node")
    npm = shutil.which("npm")
    assert node and npm, "Node.js and npm are required"
    tools = root / "tools"
    tools.mkdir()
    isolated_node = tools / "node"
    isolated_node.symlink_to(Path(node).resolve(strict=True))
    claude, package, env = install_npm(root, dist, "claude", "dist/cli.js", isolated_node, npm)
    sdk = json.loads((package / "node_modules/@anthropic-ai/claude-agent-sdk/package.json").read_text())
    case = available["claude", "SDK-CASE-042"]
    assert case["officialSDK"] == {"package": sdk["name"], "version": sdk["version"]}, sdk
    verify(root, env, binary, claude, case)

    pi, package, env = install_npm(root, dist, "pi", "cli.mjs", isolated_node, npm)
    for name in ("base.mjs", "continuation.mjs", "extension.mjs", "peer.mjs", "wire.mjs", "evidence.mjs", "LICENSE",
                 "pi-extension/SOURCE.md", "pi-extension/NOTICE"):
        assert (package / name).is_file(), name
    extension = package / "pi-extension/session.ts"
    assert extension.read_bytes() == (ROOT / "agent/pi/extensions/session.ts").read_bytes()
    launched = subprocess.run([pi], cwd=root, env=env, capture_output=True, timeout=10, text=True)
    assert launched.returncode == 2 and f"usage: {pi.name} --request FILE" in launched.stderr, launched.stderr
    for case_id in ("SDK-CASE-013", "SDK-CASE-014", "SDK-CASE-037", "SDK-CASE-038"):
        case = available["pi", case_id]
        assert digest(extension) == case["officialSDK"]["extensionSHA256"]
        verify(root, env, binary, pi, case)


def main():
    if len(sys.argv) != 2:
        raise SystemExit("usage: check_installed.py DISTRIBUTION_DIRECTORY")
    dist = Path(sys.argv[1]).resolve(strict=True)
    manifest_path = dist / "manifest.json"
    manifest = json.loads(manifest_path.read_text())
    expected = {entry["path"]: entry["sha256"] for entry in manifest["files"]}
    expected[manifest_path.name] = digest(manifest_path)
    assert sorted(path.name for path in dist.iterdir()) == sorted([*expected, "SHA256SUMS"])
    assert (dist / "SHA256SUMS").read_text() == "".join(f"{expected[name]}  {name}\n" for name in sorted(expected))
    for name, sha in expected.items():
        assert digest(dist / name) == sha, name

    with tempfile.TemporaryDirectory(prefix="agent-replay-installed-") as temporary:
        root = Path(temporary).resolve()
        binary, codex = install_native(root, dist, manifest)
        check_cli(root, {"PATH": str(root), "HOME": str(root)}, binary, manifest)
        check_drivers(root, dist, binary, codex)


if __name__ == "__main__":
    try:
        main()
    except subprocess.CalledProcessError as error:
        raise SystemExit(f"{error}\n{error.stdout}{error.stderr}") from error
