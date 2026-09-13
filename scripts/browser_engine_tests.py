"""Run real installed Playwright engines against local synthetic provider sites."""
import os
import subprocess
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    # Complete the driver handshake before disposing its process. Reading
    # executable_path alone does not advance Playwright's async transport.
    request = p.request.new_context()
    request.dispose()
    env = dict(os.environ, COT_TEST_CHROME_BIN=p.chromium.executable_path,
               COT_TEST_FIREFOX_BIN=p.firefox.executable_path)
for engine in ('chromium', 'firefox'):
    selected = dict(env, COT_TEST_BROWSER_ENGINE=engine)
    subprocess.run(['go', 'test', './internal/chatgptweb', '-count=1', '-timeout=120s'], env=selected, check=True)
subprocess.run(['go', 'test', './internal/browserbidi', '-count=1', '-timeout=120s'], env=env, check=True)
