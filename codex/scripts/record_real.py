#!/usr/bin/env python3
"""Explicit maintenance preflight. Never discovers config/auth or starts a model.

The historical marker-based launcher is retired. The Go command validates a
reviewed nonsecret isolated profile, then fails closed on the financial gate.
Actual #11 recording needs a separately reviewed enforcement implementation.
"""
import argparse
import os
import pathlib
import subprocess


def guard(record, environ):
    if not record or environ.get('SDK_REAL_AGENT_E2E') != '1':
        raise ValueError('requires --record and SDK_REAL_AGENT_E2E=1')
    if environ.get('CI'):
        raise ValueError('real recording refused in CI')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--record', action='store_true')
    parser.add_argument('--runner', required=True, type=pathlib.Path)
    parser.add_argument('--profile', required=True, type=pathlib.Path)
    args = parser.parse_args()
    guard(args.record, os.environ)
    if not args.runner.is_absolute() or not args.profile.is_absolute():
        raise ValueError('absolute reviewed runner/profile paths required')
    # Only the explicitly approved maintenance binary is invoked. Do not inherit
    # HOME, CODEX_HOME, proxy settings, account keys or platform configuration.
    result = subprocess.run([str(args.runner), '--record', str(args.profile)],
                            env={'SDK_REAL_AGENT_E2E': '1', 'PATH': '/usr/bin:/bin'},
                            timeout=10, check=False)
    raise SystemExit(result.returncode)


if __name__ == '__main__':
    main()
