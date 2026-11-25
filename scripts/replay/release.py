#!/usr/bin/env python3
"""Build a native Replay distribution or collect the verified SDK tag assets."""

import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile


ROOT = Path(__file__).resolve().parents[2]
REPLAY = ROOT / "replay"
BUILTIN_MANIFEST = REPLAY / "data/builtin/manifest.json"
DRIVERS = REPLAY / "drivers"
RUST_DRIVER = DRIVERS / "codex/rust"
GO = os.environ.get("GO", "go")
TOOL = "agent-replay"
CODEX = "agent-sdk-testify-codex"
NPM_DRIVERS = {
    "claude": DRIVERS / "claude/typescript",
    "pi": DRIVERS / "pi/javascript",
}
TARGETS = {
    "linux-amd64": "x86_64-unknown-linux-gnu",
    "linux-arm64": "aarch64-unknown-linux-gnu",
    "darwin-amd64": "x86_64-apple-darwin",
    "darwin-arm64": "aarch64-apple-darwin",
}
ARCHIVE_DOCS = {
    "LICENSE": ROOT / "LICENSE",
    "NOTICE": ROOT / "NOTICE",
    "README.md": REPLAY / "README.md",
    "docs/using-mock.md": REPLAY / "docs/using-mock.md",
    "drivers/README.md": DRIVERS / "README.md",
    "builtin-manifest.json": BUILTIN_MANIFEST,
}


def capture(*args):
    return subprocess.check_output(args, cwd=ROOT, text=True).strip()


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def resolve_version(tag, revision, allow_untagged):
    if tag is None:
        result = subprocess.run(
            ["git", "describe", "--tags", "--exact-match", "--match", "v*.*.*", "HEAD"],
            cwd=ROOT, capture_output=True, text=True,
        )
        if result.returncode == 0:
            tag = result.stdout.strip()
        elif allow_untagged:
            return "devel"
        else:
            raise SystemExit("release requires an exact SDK Git tag")
    if not re.fullmatch(r"v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?", tag):
        raise SystemExit(f"invalid SDK release tag: {tag}")
    if capture("git", "rev-parse", "--verify", f"refs/tags/{tag}^{{commit}}") != revision:
        raise SystemExit(f"SDK release tag {tag} does not point to HEAD")
    return tag


def package_filename(source):
    package = json.loads((source / "package.json").read_text())
    return f"{package['name']}-{package['version']}.tgz"


def build_bundle(output, version):
    system, arch, goos, goarch = capture(GO, "env", "GOHOSTOS", "GOHOSTARCH", "GOOS", "GOARCH").splitlines()
    target = f"{system}-{arch}"
    rust_info = capture("rustc", "+1.99.1", "-vV").splitlines()
    rust_target = next(line.removeprefix("host: ") for line in rust_info if line.startswith("host: "))
    if target not in TARGETS or (goos, goarch) != (system, arch) or rust_target != TARGETS[target]:
        raise SystemExit(f"expected matching native Go and Rust targets, got {goos}-{goarch} and {rust_target}")

    env = dict(os.environ, CGO_ENABLED="0", GOWORK="off", GOPROXY="off", GOSUMDB="off", GOTOOLCHAIN="local",
               CARGO_NET_OFFLINE="true", npm_config_offline="true", npm_config_update_notifier="false")
    subprocess.run(["bash", ROOT / "scripts/replay/build.sh", output / TOOL, version], cwd=ROOT, env=env, check=True)
    with tempfile.TemporaryDirectory(prefix="agent-replay-codex-") as temporary:
        work = Path(temporary).resolve()
        subprocess.run(["bash", RUST_DRIVER / "install.sh", work / "build", work / "prefix"], env=env, check=True)
        shutil.copy2(work / "prefix/bin" / CODEX, output / CODEX)

    claude = NPM_DRIVERS["claude"]
    subprocess.run(["npm", "ci", "--offline", "--ignore-scripts", "--omit=optional", "--no-audit", "--no-fund"],
                   cwd=claude, env=env, check=True)
    subprocess.run(["npm", "test", "--offline"], cwd=claude, env=env, check=True)
    for source in NPM_DRIVERS.values():
        subprocess.run(["npm", "pack", "--offline", "--loglevel=error", "--pack-destination", output],
                       cwd=source, env=env, check=True)
    return target


def native_distribution(output, version):
    with tempfile.TemporaryDirectory(prefix="agent-replay-bundle-") as temporary:
        bundle = Path(temporary).resolve()
        target = build_bundle(bundle, version)
        archive = output / archive_name(version, target)
        members = {TOOL: (bundle / TOOL, 0o755), CODEX: (bundle / CODEX, 0o755)}
        members.update({name: (path, 0o644) for name, path in ARCHIVE_DOCS.items()})
        with archive.open("wb") as stream, gzip.GzipFile(filename="", fileobj=stream, mode="wb", mtime=0) as gz:
            with tarfile.open(fileobj=gz, mode="w") as tar:
                for name, (path, mode) in members.items():
                    info = tarfile.TarInfo(name)
                    info.size = path.stat().st_size
                    info.mode = mode
                    with path.open("rb") as payload:
                        tar.addfile(info, payload)
        files = [{"path": archive.name, "sha256": digest(archive), "target": target.replace("-", "/"),
                  "binarySHA256": digest(bundle / TOOL), "codexSHA256": digest(bundle / CODEX)}]
        for source in NPM_DRIVERS.values():
            package = package_filename(source)
            shutil.copyfile(bundle / package, output / package)
            files.append({"path": package, "sha256": digest(output / package)})
    return files


def archive_name(version, target):
    return f"{TOOL}_{version}_{target.replace('-', '_')}.tar.gz"


def collect(output, inputs, metadata):
    files = []
    for target in TARGETS:
        root = inputs / target
        manifest = json.loads((root / "manifest.json").read_text())
        if any(manifest.get(key) != value for key, value in metadata.items()):
            raise SystemExit(f"{target} release metadata differs from checkout")
        archive, claude, pi = manifest["files"]
        expected = [archive_name(metadata["toolVersion"], target), *map(package_filename, NPM_DRIVERS.values())]
        if [entry["path"] for entry in (archive, claude, pi)] != expected or archive["target"] != target.replace("-", "/"):
            raise SystemExit(f"{target} artifacts do not match the expected filenames and target")
        for entry in (archive, claude, pi):
            path = root / entry["path"]
            if digest(path) != entry["sha256"]:
                raise SystemExit(f"{target} invalid artifact checksum: {path}")
            # The npm packages are portable. Publish the pair tested by the Linux amd64 job.
            if entry is archive or target == "linux-amd64":
                shutil.copyfile(path, output / path.name)
                files.append(entry)
    return files


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, type=Path)
    modes = parser.add_mutually_exclusive_group()
    modes.add_argument("--bundle", action="store_true", help="build an unpacked local bundle")
    modes.add_argument("--collect", type=Path, help="directory of verified OS-ARCH distributions")
    parser.add_argument("--tag", help="exact SDK release tag to build")
    parser.add_argument("--allow-untagged", action="store_true", help="allow a local devel build")
    args = parser.parse_args()
    revision = capture("git", "rev-parse", "HEAD")
    version = resolve_version(args.tag, revision, args.allow_untagged)
    if capture("git", "status", "--porcelain"):
        raise SystemExit("package a clean SDK checkout")

    output = args.output.resolve()
    if (ROOT / ".git").is_file() and not args.collect:
        command = ["bash", ROOT / "scripts/replay/with-build-checkout.sh", sys.executable,
                   "scripts/replay/release.py", "--output", output]
        command += ["--allow-untagged"] if version == "devel" else ["--tag", version]
        if args.bundle:
            command.append("--bundle")
        return subprocess.call(command, cwd=ROOT)

    output.mkdir(parents=True, exist_ok=False)
    if args.bundle:
        build_bundle(output, version)
        return 0

    builtin = json.loads(BUILTIN_MANIFEST.read_text())
    metadata = dict(schemaVersion=1, toolVersion=version, sourceRevision=revision, sourceModified=False,
                    bundleSourceRevision=builtin["sourceRevision"], bundleManifestSHA256=digest(BUILTIN_MANIFEST),
                    bundleArchiveSHA256=builtin["archiveSHA256"], materials=len(builtin["materials"]),
                    coverage=len(builtin["coverage"]), goVersion=capture(GO, "env", "GOVERSION"),
                    codexRevision=(RUST_DRIVER / "upstream-revision").read_text().strip(),
                    publication="agent-sdk-go Git tag release")
    files = collect(output, args.collect.resolve(), metadata) if args.collect else native_distribution(output, version)
    if capture("git", "status", "--porcelain") or capture("git", "rev-parse", "HEAD") != revision:
        raise SystemExit("SDK checkout changed while packaging")
    manifest = output / "manifest.json"
    manifest.write_text(json.dumps(dict(metadata, files=files), indent=2, sort_keys=True) + "\n")
    checks = {entry["path"]: entry["sha256"] for entry in files}
    checks[manifest.name] = digest(manifest)
    (output / "SHA256SUMS").write_text("".join(f"{checks[name]}  {name}\n" for name in sorted(checks)))
    print(manifest.read_text(), end="")
    return 0


if __name__ == "__main__":
    sys.exit(main())
