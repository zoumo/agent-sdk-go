#!/usr/bin/env python3
"""Check current Story inventories, local evidence links and public suite names offline."""
from collections import Counter
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1]


def inventory_errors(text, prefix, expected):
    counts = Counter(re.findall(r"^\| (" + re.escape(prefix) + r"-\d{3}) \|", text, re.M))
    actual = set(counts)
    errors = [f"{prefix}: missing {item}" for item in sorted(expected - actual)]
    errors += [f"{prefix}: unexpected {item}" for item in sorted(actual - expected)]
    errors += [f"{prefix}: duplicate {item}" for item in sorted(counts) if counts[item] != 1]
    return errors


def heading_ids(text):
    counts = Counter()
    anchors = set()
    for title in re.findall(r"^#{1,6} (.+)$", text, re.M):
        title = re.sub(r"\[([^\]]*)\]\([^)]+\)", r"\1", title)
        slug = re.sub(r"[^\w\- ]", "", title.lower()).replace(" ", "-")
        suffix = f"-{counts[slug]}" if counts[slug] else ""
        anchors.add(slug + suffix)
        counts[slug] += 1
    return anchors


def link_errors(path, text):
    errors = []
    for target in re.findall(r"\[[^\]]*\]\(([^)]+)\)", text):
        if re.match(r"[a-z]+://", target):
            continue
        destination, _, fragment = target.partition("#")
        resolved = path.parent / destination if destination else path
        if not resolved.exists():
            errors.append(f"{path.name}: missing link {target}")
        elif fragment and resolved.suffix == ".md":
            if fragment not in heading_ids(resolved.read_text()):
                errors.append(f"{path.name}: missing heading {target}")
    return errors


def check(root):
    coverage = (root / "docs/validation/story-coverage.md").read_text()
    common = {f"SDK-US-{n:03}" for n in range(1, 20)}
    scenarios = {f"US-{n:03}" for n in range(1, 15)}
    errors = inventory_errors(coverage, "SDK-US", common)
    errors += inventory_errors(coverage, "US", scenarios)
    errors += inventory_errors((root / "harness/sdk/user-stories.md").read_text(), "SDK-US", common)
    errors += inventory_errors((root / "harness/contracts/stories/index.md").read_text(), "US", scenarios)
    native = (root / "harness/sdk/native-stories.md").read_text()
    for prefix, size in [("CODEX-US", 5), ("CLAUDE-US", 7)]:
        expected = {f"{prefix}-{n:03}" for n in range(1, size + 1)}
        errors += inventory_errors(native, prefix, expected)
        errors += inventory_errors(coverage, prefix, expected)
    suite = (root / "tests/stories/stories_test.go").read_text()
    targets = suite.split("func (s *CommonStoriesSuite) TestSessionTargets()", 1)[1].split("func ", 1)[0]
    if set(re.findall(r'"(SDK-US-\d{3})"', targets)) != common - {"SDK-US-019"}:
        errors.append("SessionTargets must expose exactly SDK-US-001–018")
    history = (root / "tests/stories/history_query_test.go").read_text()
    if not all(name in history for name in ["TestIndependentHistoryBrowser", '"US-014/codex"', '"US-014/claude"']):
        errors.append("IndependentHistoryBrowser must expose US-014 for both native adapters")
    for relative in ["docs/validation/story-coverage.md", "docs/capture-plan.md", "harness/sdk/native-stories.md",
                     "docs/index.md", "docs/recordings.md", "harness/sdk/index.md"]:
        path = root / relative
        errors += link_errors(path, path.read_text())
    return errors


if __name__ == "__main__":
    failures = check(ROOT)
    for failure in failures:
        print(failure, file=sys.stderr)
    if failures:
        sys.exit(1)
    print("Story evidence: 19 goals, 14 scenarios, 12 native supplements; links and public suite inventory valid (offline).")
