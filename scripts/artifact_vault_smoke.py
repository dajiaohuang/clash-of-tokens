"""Exercise native vault lifecycle through the exact distributed gateway."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import threading

parser = argparse.ArgumentParser()
parser.add_argument("binary", type=Path)
args = parser.parse_args()
binary = args.binary.resolve()
expected = ["artifact-synthetic-canary-1"]

class Upstream(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass
    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length", "0")))
        if self.headers.get("Authorization") != "Bearer " + expected[0]:
            self.send_error(401)
            return
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        self.wfile.write(b'data: {"choices":[{"delta":{"content":"OK"}}]}\n\ndata: [DONE]\n\n')

upstream = ThreadingHTTPServer(("127.0.0.1", 0), Upstream)
threading.Thread(target=upstream.serve_forever, daemon=True).start()
with tempfile.TemporaryDirectory(prefix="cot-artifact-vault-") as folder:
    root = Path(folder)
    config_path = root / "config.json"
    subprocess.run([str(binary), "init", "-config", str(config_path)], check=True, capture_output=True)
    config = json.loads(config_path.read_text(encoding="utf-8"))
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    config["listen"] = f"127.0.0.1:{port}"
    config["browser"]["state_file"] = str(root / "sessions.json")
    config_path.write_text(json.dumps(config), encoding="utf-8")
    env = dict(os.environ, COT_API_KEY="artifact-model-key-1234567890", COT_ADMIN_KEY="artifact-admin-key-1234567890")
    base = f"http://127.0.0.1:{port}"
    process = None
    def request(path, method="GET", body=None, model=False):
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(base + path, data=data, method=method, headers={
            "Authorization": "Bearer " + env["COT_API_KEY" if model else "COT_ADMIN_KEY"],
            "Content-Type": "application/json",
        })
        with urllib.request.urlopen(req, timeout=5) as response:
            raw = response.read()
            for secret in ["artifact-synthetic-canary-1", "artifact-synthetic-canary-2"]:
                assert secret.encode() not in raw, "secret appeared in API response"
            return json.loads(raw)
    def start():
        global process
        process = subprocess.Popen([str(binary), "serve", "-config", str(config_path)], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        deadline = time.monotonic() + 15
        while True:
            try:
                request("/admin/status")
                return
            except Exception:
                if process.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError("artifact gateway did not start")
                time.sleep(.1)
    def stop():
        if process is not None:
            process.terminate()
            process.wait(timeout=10)
    def apply(config):
        current = request("/admin/config")
        request("/admin/config", "PATCH", {"revision": current["revision"], "config": config, "summary": "artifact fixture"})
    try:
        start()
        request("/admin/credentials/artifact-fixture", "PUT", {"kind": "api_key", "source": "synthetic", "value": expected[0]})
        config["sources"] = [{"id": "mock", "provider": "p", "adapter": "openai", "base_url": f"http://127.0.0.1:{upstream.server_port}/v1",
            "local": True, "enabled": True, "max_inflight": 1, "quota_domain": "fixture", "quota_max_inflight": 1,
            "credential_ref": "cred://artifact-fixture", "credential_mode": "api_key",
            "models": [{"id": "m", "upstream": "m", "protocols": ["chat"], "tier": "unrated", "tools": "none", "max_input_bytes": 1024}]}]
        apply(config)
        def verify():
            result = request("/admin/sources/mock/validate", "POST", {"model": "m", "protocol": "chat"})
            assert result["verified"] and result["history_recorded"], "artifact did not resolve credential"
        verify()
        stop()
        start()
        verify()
        expected[0] = "artifact-synthetic-canary-2"
        request("/admin/credentials/artifact-fixture", "PUT", {"kind": "api_key", "source": "synthetic", "value": expected[0]})
        verify()
        stop()
        start()
        verify()
        config["sources"] = []
        apply(config)
        request("/admin/credentials/artifact-fixture", "DELETE")
        stop()
        start()
        assert request("/admin/credentials") == [], "deleted credential returned after restart"
        for path in root.rglob("*"):
            if path.is_file():
                raw = path.read_bytes()
                assert all(s.encode() not in raw for s in ["artifact-synthetic-canary-1", "artifact-synthetic-canary-2"]), "plaintext persisted"
        print(json.dumps({"artifact_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(), "platform": os.name,
                          "vault_lifecycle": "passed", "checks": ["create", "adapter_call", "restart_read", "rotate", "restart_rotate", "delete", "restart_delete", "canary"]}))
    finally:
        stop()
        upstream.shutdown()
        upstream.server_close()
