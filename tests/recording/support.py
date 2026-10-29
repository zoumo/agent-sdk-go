"""Configure repository-owned scenarios for SDK integration tests only."""
import os
from pathlib import Path
import sys
ROOT = Path(__file__).resolve().parents[2]
PACKAGE = ROOT / 'tools/record-user-story'
sys.path.insert(0, str(PACKAGE))
os.environ['AGENT_MOCK_SCENARIOS'] = str(Path(__file__).parent / 'scenarios')
BOOTSTRAP = Path(__file__).parent / 'recording_bootstrap.py'
