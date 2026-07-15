#!/usr/bin/env python3
"""Verify that documentation harness references resolve.

Scope: the routing/architecture surface that must not develop dead links —
`AGENTS.md`, `ARCHITECTURE.md`, `README.md`, and everything under `docs/`. This
keeps the code -> owning design doc map in `ARCHITECTURE.md` honest as modules
and docs move or get renamed.

Only local relative links are checked. External (`http`, `mailto`) links,
pure in-page anchors (`#section`), and links inside fenced code blocks (which
are illustrative templates, not real references) are skipped. Module paths in
the `ARCHITECTURE.md` Core Modules table are also checked.

Exit code is non-zero when any link is broken so it can gate CI.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

# The image runs with the repository mounted at /work and sets it as cwd.
ROOT = Path.cwd().resolve()

# Markdown inline link: [text](target).  Title and angle brackets handled below.
LINK_RE = re.compile(r"\[[^\]]*\]\(([^)]+)\)")
FENCE_RE = re.compile(r"^\s*`{3,}")
MODULE_PATH_RE = re.compile(r"`([^`]+)`")
CORE_MODULES_HEADING = "## Core Modules"
CORE_MODULES_HEADER = "| Module | Responsibility | Owning design doc |"


def candidate_files() -> list[Path]:
    files: list[Path] = []
    for name in ("AGENTS.md", "ARCHITECTURE.md", "README.md"):
        path = ROOT / name
        if path.is_file() and not path.is_symlink():
            files.append(path)
    files.extend(p for p in ROOT.glob("docs/**/*.md") if not p.is_symlink())
    return sorted(set(files))


def link_targets(path: Path) -> list[tuple[int, str]]:
    """Return (line_number, raw_target) for each link outside code fences."""
    targets: list[tuple[int, str]] = []
    in_fence = False
    for lineno, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        if FENCE_RE.match(line):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        for match in LINK_RE.finditer(line):
            targets.append((lineno, match.group(1).strip()))
    return targets


def normalize(target: str) -> str | None:
    """Return a checkable relative path, or None to skip this target."""
    target = target.strip()
    if target.startswith("<") and target.endswith(">"):
        target = target[1:-1].strip()
    # Drop an optional link title: [t](path "title").
    target = target.split()[0] if target else target
    if not target or target.startswith(("http://", "https://", "mailto:", "#")):
        return None
    return target.split("#", 1)[0]  # strip anchor fragment


def is_under_root(path: Path) -> bool:
    try:
        path.relative_to(ROOT)
    except ValueError:
        return False
    return True


def unresolved_path(resolved: Path) -> bool:
    return not is_under_root(resolved) or not resolved.exists()


def architecture_module_paths() -> list[tuple[int, str]]:
    """Return module paths from the ARCHITECTURE.md Core Modules table."""
    path = ROOT / "ARCHITECTURE.md"
    if not path.is_file():
        return []

    paths: list[tuple[int, str]] = []
    in_section = False
    in_table = False

    for lineno, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        stripped = line.strip()
        if stripped == CORE_MODULES_HEADING:
            in_section = True
            continue
        if in_section and stripped.startswith("## "):
            break
        if not in_section:
            continue

        if stripped == CORE_MODULES_HEADER:
            in_table = True
            continue
        if not in_table or not stripped.startswith("|"):
            continue
        if set(stripped.replace("|", "").strip()) == {"-"}:
            continue

        cells = [cell.strip() for cell in stripped.strip("|").split("|")]
        if not cells:
            continue
        for match in MODULE_PATH_RE.finditer(cells[0]):
            paths.append((lineno, match.group(1)))

    return paths


def main() -> int:
    problems: list[str] = []
    for path in candidate_files():
        base = path.parent
        for lineno, raw in link_targets(path):
            rel = normalize(raw)
            if rel is None:
                continue
            resolved = (base / rel).resolve()
            if unresolved_path(resolved):
                rel_path = path.relative_to(ROOT)
                problems.append(f"{rel_path}:{lineno}: broken link -> {raw}")

    module_paths = architecture_module_paths()
    if not module_paths:
        problems.append("ARCHITECTURE.md: missing Core Modules table entries")
    for lineno, module_path in module_paths:
        resolved = (ROOT / module_path).resolve()
        if unresolved_path(resolved):
            problems.append(
                f"ARCHITECTURE.md:{lineno}: missing module path -> {module_path}"
            )

    if problems:
        print("Documentation harness problems:")
        for item in problems:
            print(f"  {item}")
        print(f"\n{len(problems)} problem(s) found.")
        return 1

    print("All documentation links and architecture module paths resolve.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
