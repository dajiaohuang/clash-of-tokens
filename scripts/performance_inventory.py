"""Static performance review inventory; counts are investigation leads, not bugs."""
import json
import pathlib
import re

ROOT = pathlib.Path(__file__).resolve().parents[1]
PATTERNS = {
    "json_encode": r"json\.(?:Marshal|MarshalIndent|NewEncoder)",
    "json_decode_validate": r"json\.(?:Unmarshal|NewDecoder|Valid)",
    "allocation_copy": r"\b(?:make|append|copy)\(|bytes\.(?:Clone|NewBuffer)|strings\.Builder",
    "buffered_read": r"io\.ReadAll|ReadString\(",
    "timers_polling": r"time\.(?:After|Sleep|NewTimer|NewTicker)|chromedp\.Poll",
    "synchronization": r"\.(?:Lock|Unlock|Add|CompareAndSwap)\(|\bsync\.",
    "http_browser_process": r"http\.Transport|chromedp\.|exec\.Command|ws\.Dialer",
    "filesystem": r"os\.(?:ReadFile|WriteFile|CreateTemp|Open|Rename|MkdirAll)|\.Sync\(",
}
rows = []
for base in ("internal", "cmd", "catalog"):
    for path in sorted((ROOT / base).rglob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        lines = path.read_text(encoding="utf-8").splitlines()
        hits = {}
        for category, pattern in PATTERNS.items():
            numbers = [i for i, line in enumerate(lines, 1) if re.search(pattern, line)]
            if numbers:
                hits[category] = numbers
        rows.append({"file": path.relative_to(ROOT).as_posix(), "lines": len(lines), "review_leads": hits})
report = {
    "scope": "All production Go files in internal, cmd and catalog; reference clones and tests excluded.",
    "interpretation": "Static occurrence inventory, not proof of runtime cost or exhaustive semantic review. See PERFORMANCE_AUDIT.md for measured decisions.",
    "files": len(rows), "lines": sum(row["lines"] for row in rows), "entries": rows,
}
target = ROOT / "docs" / "performance-inventory.json"
target.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
print(json.dumps({"files": report["files"], "lines": report["lines"], "output": str(target)}))
