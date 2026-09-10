"""Isolated gateway for browser regression tests; no user configuration."""
import json
import os
from pathlib import Path
import subprocess
import socket
import sys
import tempfile
import time

root = Path(__file__).resolve().parents[1]
binary = root / ".clash-tokens" / "ui-test.exe"
with tempfile.TemporaryDirectory(prefix="cot-ui-") as directory:
    path = Path(directory) / "config.json"
    subprocess.run([str(binary), "init", "-config", str(path)], check=True)
    config = json.loads(path.read_text(encoding="utf-8"))
    config["listen"] = "127.0.0.1:18317"
    config["browser"]["state_file"] = str(Path(directory) / "sessions.json")
    path.write_text(json.dumps(config), encoding="utf-8")
    env = os.environ.copy()
    env["COT_API_KEY"] = "ui-test-data-key-123456789"
    env["COT_ADMIN_KEY"] = "ui-test-admin-key-123456789"
    process = subprocess.Popen([str(binary), "serve", "-config", str(path)], env=env)
    try:
        if "--test" in sys.argv:
            deadline = time.monotonic() + 10
            while True:
                try:
                    with socket.create_connection(("127.0.0.1", 18317), timeout=0.3):
                        break
                except OSError:
                    if process.poll() is not None or time.monotonic() > deadline:
                        raise RuntimeError("Isolated gateway did not start")
                    time.sleep(0.1)
            subprocess.run([sys.executable, str(root / "scripts" / "ui_regression.py")], check=True)
        else:
            process.wait()
    finally:
        process.terminate()
        process.wait(timeout=10)
