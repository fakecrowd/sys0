#!/usr/bin/env python3
"""Reject tracked files containing blocked identity markers."""
from __future__ import annotations
import hashlib
import subprocess
import sys
from pathlib import Path

_BLOCKED = {
    4: {"350a770c0ec9f353e1a5629895f374fdaa299876c3870c03feb60eb4a3769d94"},
    19: {"e514ef79fca0e4c27eda5a626cae54c73b2fa96020f6f7e2fd3a913e4868390a"},
}

def blocked_offsets(text: str) -> list[int]:
    lowered = text.lower()
    hits: set[int] = set()
    for length, hashes in _BLOCKED.items():
        for i in range(max(0, len(lowered) - length + 1)):
            if hashlib.sha256(lowered[i:i + length].encode()).hexdigest() in hashes:
                hits.add(i)
    return sorted(hits)

def tracked_files(root: Path) -> list[Path]:
    raw = subprocess.check_output(["git", "ls-files", "-z"], cwd=root)
    return [root / p.decode() for p in raw.split(b"\0") if p]

def main() -> int:
    root = Path(subprocess.check_output(["git", "rev-parse", "--show-toplevel"], text=True).strip())
    files = tracked_files(root)
    failures: list[tuple[str, list[int]]] = []
    for path in files:
        try: text = path.read_text(encoding="utf-8")
        except (UnicodeDecodeError, OSError): continue
        offsets = blocked_offsets(text)
        if offsets: failures.append((str(path.relative_to(root)), offsets))
    if failures:
        for path, offsets in failures: print(f"identity marker found: {path} at {offsets}", file=sys.stderr)
        return 1
    print(f"identity scan OK ({len(files)} tracked files)")
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
