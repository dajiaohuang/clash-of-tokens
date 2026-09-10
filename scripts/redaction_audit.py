"""Reject direct production logging that could bypass the gateway redaction boundary.

Adapters may return detailed errors to the in-process caller, but production
code has no logger sink today. This small dependency-free audit keeps a future
log import or Printf-style logger from silently becoming a secret leak. Tests
and deliberately user-facing CLI output are excluded from this check.
"""

from pathlib import Path
import re
import sys


ROOT = Path(__file__).resolve().parents[1]
IMPORT = re.compile(r'^\s*"(?:log|log/slog|go\.uber\.org/zap|github\.com/rs/zerolog)"\s*$', re.MULTILINE)
CALL = re.compile(r"\b(?:log|slog|zap|zerolog)\.(?:Print|Printf|Println|Info|Infof|Warn|Warnf|Error|Errorf|Debug|Debugf|Fatal|Fatalf|Panic|Panicf)\s*\(")


def main() -> int:
    violations: list[str] = []
    for base in (ROOT / "internal", ROOT / "cmd"):
        for path in base.rglob("*.go"):
            if path.name.endswith("_test.go"):
                continue
            text = path.read_text(encoding="utf-8")
            if IMPORT.search(text) or CALL.search(text):
                violations.append(str(path.relative_to(ROOT)))
    if violations:
        print("direct production logging requires an explicit redaction review:")
        print("\n".join(sorted(violations)))
        return 1
    print("redaction audit passed: no direct production logger sink")
    return 0


if __name__ == "__main__":
    sys.exit(main())
