"""Configure repository-owned scenarios for SDK integration tests only."""
import os
from pathlib import Path
import sys
ROOT = Path(__file__).resolve().parents[4]
PACKAGE = ROOT / 'tools/official-sdk-drivers/common'
sys.path.insert(0, str(PACKAGE))
sys.path[:0] = [str(PACKAGE.parent / agent) for agent in ('codex', 'claude')]
os.environ['AGENT_MOCK_SCENARIOS'] = str(ROOT / 'tests/simulation/scenarios')
BOOTSTRAP = Path(__file__).parent / 'recording_bootstrap.py'
