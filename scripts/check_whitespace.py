#!/usr/bin/env python3
"""Check event changes, not the clean checkout's empty working-tree diff."""
import json
import os
import re
import subprocess


def git(*args):
    return subprocess.check_output(["git", *args], text=True, input="").strip()


def commit(value):
    if not re.fullmatch(r"[0-9a-f]{40}", value):
        raise ValueError("Expected a full commit SHA")
    return git("rev-parse", "--verify", value + "^{commit}")


def comparison(event_name, event):
    head = git("rev-parse", "HEAD")
    if event_name == "pull_request":
        head = commit(event["pull_request"]["head"]["sha"])
        base = git("merge-base", commit(event["pull_request"]["base"]["sha"]), head)
    elif event_name == "push" and event["before"] != "0" * 40:
        base = commit(event["before"])
    elif event_name == "push":
        base = git("hash-object", "-t", "tree", "--stdin")
    elif event_name == "workflow_dispatch":
        parents = git("rev-list", "--parents", "-n", "1", head).split()
        base = parents[1] if len(parents) > 1 else git("hash-object", "-t", "tree", "--stdin")
    else:
        raise ValueError("Unsupported event: " + event_name)
    return base, head


def main():
    with open(os.environ["GITHUB_EVENT_PATH"], encoding="utf-8") as stream:
        event = json.load(stream)
    base, head = comparison(os.environ["GITHUB_EVENT_NAME"], event)
    print(f"Checking whitespace: {base} -> {head}", flush=True)
    subprocess.run(["git", "-c", "core.whitespace=blank-at-eol,blank-at-eof,space-before-tab",
                    "diff", "--check", base, head, "--"], check=True)


if __name__ == "__main__":
    main()
