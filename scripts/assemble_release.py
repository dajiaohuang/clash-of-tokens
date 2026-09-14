"""Assemble six native build outputs, verifying checksums before publication."""
import hashlib
from pathlib import Path
import shutil
import sys

source, output = map(Path, sys.argv[1:])
output.mkdir(parents=True, exist_ok=False)
for platform in ('windows', 'linux', 'darwin'):
    for arch in ('amd64', 'arm64'):
        key = f'{platform}-{arch}'
        package = source / f'release-{key}'
        name = f'clash-tokens-{key}' + ('.exe' if platform == 'windows' else '')
        binary = package / name
        expected = hashlib.sha256(binary.read_bytes()).hexdigest()
        if (package / (name + '.sha256')).read_text().strip() != f'{expected}  {name}':
            raise SystemExit(f'Invalid checksum: {name}')
        shutil.copyfile(binary, output / name)
        shutil.copyfile(package / (name + '.sha256'), output / (name + '.sha256'))
        for filename in ('release-metadata.json', 'THIRD_PARTY_NOTICES.txt'):
            shutil.copyfile(package / filename, output / f'{key}-{filename}')
root = Path(__file__).resolve().parents[1]
for filename in ('install.ps1', 'install.sh'):
    shutil.copyfile(root / filename, output / filename)
print(f'Assembled six verified native binaries and installers: {output}')
