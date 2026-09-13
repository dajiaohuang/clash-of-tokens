"""Exercise provider browser fixtures on isolated real Chromium and Firefox."""
import os
import argparse
from pathlib import Path
import socket
import subprocess
import tempfile
import time
from contextlib import contextmanager
from playwright.sync_api import sync_playwright

parser=argparse.ArgumentParser()
parser.add_argument('--engine',choices=['chromium','firefox'])
parser.add_argument('--package',action='append',choices=['majorweb','enterpriseweb','chinanext','chinaremaining','businessweb','playground'])
options=parser.parse_args()

with sync_playwright() as p:
    request=p.request.new_context()
    request.dispose()
    binaries={'chromium':p.chromium.executable_path,'firefox':p.firefox.executable_path}

@contextmanager
def isolated_profile():
    directory=tempfile.TemporaryDirectory(prefix='cot-provider-engine-')
    # Recursive cleanup is restricted to this exact newly created temp root.
    assert Path(directory.name).resolve().parent==Path(tempfile.gettempdir()).resolve()
    try:
        yield directory.name
    finally:
        for attempt in range(25):
            try:
                directory.cleanup()
                break
            except PermissionError:
                if attempt==24: raise
                time.sleep(.2)

for engine,binary in binaries.items():
    if options.engine and engine!=options.engine: continue
    with isolated_profile() as profile:
        with socket.socket() as listener:
            listener.bind(('127.0.0.1',0))
            port=listener.getsockname()[1]
        if engine=='firefox':
            Path(profile,'user.js').write_text('user_pref("datareporting.policy.dataSubmissionEnabled",false);\nuser_pref("browser.shell.checkDefaultBrowser",false);\n')
            args=['--headless','--no-remote','--profile',profile,'--remote-debugging-port',str(port),'about:blank']
        else:
            args=['--headless=new','--no-sandbox','--no-first-run',f'--user-data-dir={profile}',f'--remote-debugging-port={port}','about:blank']
        process=subprocess.Popen([binary,*args],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        try:
            deadline=time.monotonic()+15
            while True:
                try:
                    with socket.create_connection(('127.0.0.1',port),timeout=.3): break
                except OSError:
                    if process.poll() is not None or time.monotonic()>deadline: raise RuntimeError(f'{engine} did not start')
                    time.sleep(.1)
            env=dict(os.environ,COT_TEST_CDP=f'http://127.0.0.1:{port}',COT_TEST_BROWSER_ENGINE=engine)
            packages=['./internal/providers/'+name for name in (options.package or ['majorweb','enterpriseweb','chinanext','chinaremaining','businessweb','playground'])]
            subprocess.run(['go','test','-p=1','./internal/browserexec',*packages,'-run','BrowserFixture|BrowserSigningContract|Google.*Browser','-count=1','-timeout=120s'],env=env,check=True)
        finally:
            if os.name=='nt' and process.poll() is None:
                # Descendants can exit during taskkill's tree walk, producing
                # a nonzero result even after the owned root was terminated.
                stopped=subprocess.run(['taskkill','/PID',str(process.pid),'/T','/F'],stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
                if stopped.returncode and process.poll() is None:
                    try: process.wait(timeout=2)
                    except subprocess.TimeoutExpired: raise RuntimeError('owned browser tree did not stop')
            elif process.poll() is None:
                process.terminate()
            process.wait(timeout=10)
