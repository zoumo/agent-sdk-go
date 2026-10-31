"""Run one Claude SDK case from a Go-prepared request."""
import sys
from pathlib import Path
root = Path(__file__).resolve().parent.parent
sys.path[:0] = [str(root / 'common'), str(root / 'claude')]
from claude_driver import main
if __name__ == '__main__':
    sys.exit(main())
