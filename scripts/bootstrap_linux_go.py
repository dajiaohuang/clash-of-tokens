"""Fetch a hash-verified Go toolchain archive for Windows/WSL test runs.

No installation or environment changes are performed. Extract the printed
archive with WSL tar into a temporary directory to run race tests with GCC.
"""
import hashlib
import json
import pathlib
import urllib.request

VERSION = "go1.27.1"
FILENAME = f"{VERSION}.linux-amd64.tar.gz"
ROOT = pathlib.Path(__file__).resolve().parents[1]
target = ROOT / ".clash-tokens" / "toolchains" / FILENAME
releases = json.load(urllib.request.urlopen("https://go.dev/dl/?mode=json&include=all", timeout=30))
record = next(f for r in releases if r["version"] == VERSION for f in r["files"] if f["filename"] == FILENAME)
target.parent.mkdir(parents=True, exist_ok=True)
if not target.exists():
    with urllib.request.urlopen(f"https://go.dev/dl/{FILENAME}", timeout=60) as source, target.open("wb") as out:
        while chunk := source.read(1024 * 1024):
            out.write(chunk)
actual = hashlib.file_digest(target.open("rb"), "sha256").hexdigest()
if actual != record["sha256"]:
    raise SystemExit("Go archive checksum mismatch; do not extract")
print(json.dumps({"archive": str(target), "sha256": actual, "verified": True}))
