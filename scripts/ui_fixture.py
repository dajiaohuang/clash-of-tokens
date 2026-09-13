"""Isolated gateway for browser regression tests; no user configuration."""
import json
import os
from pathlib import Path
import subprocess
import socket
import sys
import tempfile
import time
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

class SyntheticUpstream(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"data":[{"id":"test-model"},{"id":"discovered-model"}]}')

    def do_POST(self):
        body=json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        if self.path.endswith('/responses'):
            assert 'input' in body and 'messages' not in body
            output='event: response.output_text.delta\ndata: {"type":"response.output_text.delta","delta":"OK"}\n\nevent: response.completed\ndata: {"type":"response.completed","response":{"status":"completed"}}\n\n'
        elif self.path.endswith('/messages'):
            assert body['max_tokens']==1024 and self.headers.get('anthropic-version')
            output='event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"OK"}}\n\nevent: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}\n\nevent: message_stop\ndata: {"type":"message_stop"}\n\n'
        elif ':streamGenerateContent' in self.path:
            assert 'contents' in body and 'messages' not in body
            output='data: {"candidates":[{"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":"STOP"}]}\n\n'
        else:
            output='data: {"choices":[{"delta":{"content":"OK"}}]}\n\ndata: {"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}\n\ndata: [DONE]\n\n'
        self.wfile.write(output.encode())

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
    env["COT_OPENAI_KEY"] = "synthetic-env-token-not-for-display"
    upstream = ThreadingHTTPServer(("127.0.0.1", 0), SyntheticUpstream)
    threading.Thread(target=upstream.serve_forever, daemon=True).start()
    env["COT_UI_UPSTREAM"] = f"http://127.0.0.1:{upstream.server_port}/v1"
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
            if '--protocols-only' not in sys.argv:
                subprocess.run([sys.executable, str(root / "scripts" / "ui_regression.py")], check=True, env=env)
            subprocess.run([sys.executable, str(root / "scripts" / "ui_protocols.py")], check=True, env=env)
        else:
            process.wait()
    finally:
        process.terminate()
        process.wait(timeout=10)
        upstream.shutdown()
        upstream.server_close()
