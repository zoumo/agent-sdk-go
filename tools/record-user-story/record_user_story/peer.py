"""Identity and process boundary for the separately installed Go executable."""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

from .environment import read_regular


class IdentityError(ValueError):
    """A fixed public message that contains no paths or private configuration."""


def identity(value=None, expected=None):
    value = value or os.environ.get('AGENT_MOCK_BIN')
    if not value or not Path(value).is_absolute():
        raise IdentityError('set AGENT_MOCK_BIN to an absolute Go agent-mock executable')
    try:
        path = Path(value).resolve(strict=True)
        if not path.is_file() or not os.access(path, os.X_OK):
            raise IdentityError('Go agent-mock executable unavailable')
        digest = hashlib.sha256(read_regular(path)).hexdigest()
    except OSError as exc:
        raise IdentityError('Go agent-mock executable unavailable') from exc
    # Reject changed bytes before executing even the version probe.
    if expected is not None and digest != expected.get('sha256'):
        raise IdentityError('prepared Go agent-mock binary changed')
    try:
        result = subprocess.run([str(path), 'version'], capture_output=True, timeout=5,
                                env={'PATH': os.defpath}, check=True)
        version = result.stdout.decode().strip()
    except (OSError, subprocess.SubprocessError, UnicodeError) as exc:
        raise IdentityError('Go agent-mock version probe failed') from exc
    if not re.fullmatch(r'agent-mock [0-9][^\s]*', version) or result.stderr:
        raise IdentityError('unsupported Go agent-mock version response')
    actual = {'path': str(path), 'sha256': digest, 'version': version}
    if expected is not None and actual['version'] != expected.get('version'):
        raise IdentityError('prepared Go agent-mock version changed')
    return actual


def planned_identity(plan, value=None):
    expected = plan.get('agentMock')
    if not expected:
        raise IdentityError('current simulation plan requires a frozen Go identity')
    return identity(value or expected['path'], expected)


def verify_receipt(path, scenario, provider):
    result = json.loads(read_regular(path))
    if (result.get('version') != 1 or result.get('peer') != 'Go native mock' or
            result.get('provider') != provider or result.get('fullConsumption') is not True or
            result.get('nativeLaunched') is not False or result.get('clientEOF') is not True or
            result.get('recordingSHA256') != hashlib.sha256(read_regular(scenario)).hexdigest()):
        raise ValueError('Go mock receipt does not prove consumption of this recording')
    return result
