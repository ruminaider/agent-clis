"""Test configuration for local src imports."""

from __future__ import annotations

import sys
from pathlib import Path

# Keep tests runnable without an editable install. Comment by Claude.
ROOT = Path(__file__).resolve().parents[1]
SRC = ROOT / "src"
if str(SRC) not in sys.path:
    sys.path.insert(0, str(SRC))
