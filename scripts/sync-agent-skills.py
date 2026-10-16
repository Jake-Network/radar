#!/usr/bin/env python3
"""Synchronize the canonical Radar skill into distributable and embedded copies."""
import argparse
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
COPIES = (
    "internal/onboarding/assets/radar.md",
    "integrations/codex/SKILL.md",
    "integrations/claude-code/skills/radar/SKILL.md",
    "integrations/plugins/claude/skills/radar/SKILL.md",
    "integrations/plugins/codex/skills/radar/SKILL.md",
)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="report drift without writing")
    args = parser.parse_args()
    canonical = (ROOT / "integrations/skills/radar/SKILL.md").read_bytes()
    mismatches = []
    for relative in COPIES:
        path = ROOT / relative
        if not path.exists() or path.read_bytes() != canonical:
            mismatches.append(relative)
            if not args.check:
                path.write_bytes(canonical)
    if args.check and mismatches:
        parser.exit(1, "Skill copies differ: " + ", ".join(mismatches) + "\n")
    print("Radar skill copies are synchronized")


if __name__ == "__main__":
    main()
