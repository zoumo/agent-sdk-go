"""Absolute-path subprocess entry point; works from an isolated synthetic cwd."""
import sys
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
from agent_recording.cli import main
if __name__ == '__main__':
    sys.exit(main())
