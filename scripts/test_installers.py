"""Real native binary installs plus deterministic GitHub download fixtures.

No external network, user PATH edits, service starts, or real release publication.
Run with the native artifact to test, using Python 3.9+.
"""
import hashlib
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tempfile

root = Path(__file__).resolve().parents[1]
binary = Path(sys.argv[1]).resolve()
digest = hashlib.sha256(binary.read_bytes()).hexdigest()
windows = os.name == 'nt'
system = 'windows' if windows else ('darwin' if sys.platform == 'darwin' else 'linux')
arch = 'arm64' if platform.machine().lower() in ('arm64', 'aarch64') else 'amd64'
asset = f'clash-tokens-{system}-{arch}' + ('.exe' if windows else '')
shell = (shutil.which('powershell') or shutil.which('pwsh')) if windows else shutil.which('bash')
if not shell:
    raise SystemExit('Required shell unavailable')

# Explicitly own this freshly created directory before recursive test cleanup.
with tempfile.TemporaryDirectory(prefix='cot-installer-tests-') as directory:
    workspace = Path(directory).resolve()
    assert workspace.parent == Path(tempfile.gettempdir()).resolve()
    destination = workspace / 'installed bin'
    target = destination / ('clash-tokens.exe' if windows else 'clash-tokens')
    sentinel = workspace / 'config.json'
    sentinel.write_text('configuration must survive')
    env = dict(os.environ, COT_FIXTURE_BINARY=str(binary), COT_FIXTURE_ASSET=asset,
               COT_FIXTURE_HASH=digest, COT_FIXTURE_DEST=str(destination),
               COT_INSTALL_SCRIPT=str(root / 'install.ps1'))
    if windows:
        prefix = [shell, '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', str(root / 'install.ps1'),
                  '-NoPath', '-InstallDir', str(destination)]
        local = ['-Binary', str(binary), '-Sha256', digest]
        bad = ['-Binary', str(binary), '-Sha256', '0' * 64]
        wrapper = workspace / 'release-fixture.ps1'
        wrapper.write_text(r'''
$ErrorActionPreference = 'Stop'
function Invoke-RestMethod {
    param($Uri, $TimeoutSec, $Headers)
    if ($env:COT_FIXTURE_MODE -eq 'missing') { throw 'fixture release missing' }
    if ($Uri -notmatch '/releases/(latest|tags/v1.2.3)$') { throw 'unexpected metadata URL' }
    return @{draft=$false; tag_name='v1.2.3'; assets=@(@{name=$env:COT_FIXTURE_ASSET}, @{name="$env:COT_FIXTURE_ASSET.sha256"})}
}
function Invoke-WebRequest {
    param([switch]$UseBasicParsing, $Uri, $OutFile, $TimeoutSec)
    if ($Uri -notlike 'https://github.com/dajiaohuang/clash-of-tokens/releases/download/v1.2.3/*') { throw 'unexpected download URL' }
    if ($Uri.EndsWith('.sha256')) {
        $checksum = $env:COT_FIXTURE_HASH
        if ($env:COT_FIXTURE_MODE -eq 'corrupt') { $checksum = '0' * 64 }
        return @{Content="$checksum  $env:COT_FIXTURE_ASSET`n"}
    }
    [IO.File]::Copy($env:COT_FIXTURE_BINARY, $OutFile)
}
& $env:COT_INSTALL_SCRIPT -NoPath -InstallDir $env:COT_FIXTURE_DEST -Version $env:COT_FIXTURE_VERSION
''', encoding='utf-8')
        release_command = [shell, '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', str(wrapper)]
    else:
        prefix = [shell, str(root / 'install.sh'), '--install-dir', str(destination)]
        local = ['--binary', str(binary), '--sha256', digest]
        bad = ['--binary', str(binary), '--sha256', '0' * 64]
        shim = workspace / 'tools'
        shim.mkdir()
        curl = shim / 'curl'
        curl.write_text('#!' + sys.executable + '\n' + r'''
import os, pathlib, shutil, sys
args=sys.argv[1:]
url=next(a for a in args if a.startswith('https://'))
if os.environ['COT_FIXTURE_MODE']=='missing':sys.exit(22)
if url.endswith('/releases/latest'):
    print('https://github.com/dajiaohuang/clash-of-tokens/releases/tag/v1.2.3',end='');sys.exit()
if '/releases/download/v1.2.3/' not in url:sys.exit('unexpected URL')
out=pathlib.Path(args[args.index('--output')+1])
if url.endswith('.sha256'):
    checksum=os.environ['COT_FIXTURE_HASH'] if os.environ['COT_FIXTURE_MODE']!='corrupt' else '0'*64
    out.write_text(checksum+'  '+os.environ['COT_FIXTURE_ASSET']+'\n')
else:shutil.copyfile(os.environ['COT_FIXTURE_BINARY'],out)
''', encoding='utf-8')
        curl.chmod(0o755)
        env['PATH'] = str(shim) + os.pathsep + env['PATH']
        release_command = prefix

    def run(command, success=True, **overrides):
        result = subprocess.run(command, env=dict(env, **overrides), text=True,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=90)
        if (result.returncode == 0) != success:
            raise AssertionError(f'Unexpected installer outcome ({result.returncode}):\n{result.stdout}')
        if target.exists() and hashlib.sha256(target.read_bytes()).hexdigest() != digest:
            raise AssertionError('Installed binary changed unexpectedly')
        assert sentinel.read_text() == 'configuration must survive'

    run(prefix + local)
    run(prefix + local)  # existing target must be replaced successfully
    run(prefix + bad, success=False)
    run(prefix + (['-Binary', str(binary)] if windows else ['--binary', str(binary)]), success=False)
    invalid = workspace / 'invalid.exe'
    invalid.write_text('this_is_not_a_valid_gateway_fixture\n')
    invalid_hash = hashlib.sha256(invalid.read_bytes()).hexdigest()
    run(prefix + (['-Binary', str(invalid), '-Sha256', invalid_hash] if windows else
                  ['--binary', str(invalid), '--sha256', invalid_hash]), success=False)
    for version in ('latest', 'v1.2.3'):
        command = release_command + ([] if windows else ['--version', version])
        run(command, COT_FIXTURE_MODE='ok', COT_FIXTURE_VERSION=version)
    for mode in ('missing', 'corrupt'):
        run(release_command, success=False, COT_FIXTURE_MODE=mode, COT_FIXTURE_VERSION='latest')
    run([str(target), 'version'])
    assert not list(destination.glob('.cot-install*')), 'staging files leaked'
    print(f'Installer passed: {system}/{arch}; install, upgrade, spaced path, checksum required/mismatch, latest, pinned, missing release, config preservation, cleanup')
