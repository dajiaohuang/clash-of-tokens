"""Record exact artifact/module provenance and copy dependency license notices."""
import hashlib
import json
from pathlib import Path
import subprocess
import sys

artifact=Path(sys.argv[1]).resolve()
root=Path(__file__).resolve().parents[1]
out=artifact.parent
def run(*args):return subprocess.check_output(args,cwd=root,text=True,encoding='utf-8').strip()
decoder=json.JSONDecoder()
raw=run('go','list','-m','-json','all')
build_info=run('go','version','-m',str(artifact))
shipped={line.split()[1] for line in build_info.splitlines() if line.strip().startswith('dep\t')}
modules=[]
while raw:
    value,end=decoder.raw_decode(raw);modules.append(value);raw=raw[end:].lstrip()
notices=[]
for m in modules:
    if not m.get('Main') and m['Path'] not in shipped:continue
    directory=Path(m.get('Dir',root if m.get('Main') else ''))
    license_files=[]
    if directory.is_dir():
        for file in sorted(directory.iterdir()):
            if file.is_file() and file.name.upper().split('.')[0] in ('LICENSE','COPYING','NOTICE','COPYRIGHT'):
                if file.stat().st_size>256*1024:raise RuntimeError('License file exceeds review limit: '+m['Path'])
                text=file.read_text(encoding='utf-8',errors='replace')
                notices.append('## '+m['Path']+' '+m.get('Version','(workspace)')+' / '+file.name+'\n\n'+text)
                license_files.append(file.name)
    m['license_files']=license_files
    m.pop('Dir',None);m.pop('GoMod',None)
goroot=Path(run('go','env','GOROOT'))
notices.append('## Go standard library\n\n'+(goroot/'LICENSE').read_text(encoding='utf-8'))
digest=hashlib.sha256(artifact.read_bytes()).hexdigest()
modules=[m for m in modules if m.get('Main') or m['Path'] in shipped]
record={'artifact':artifact.name,'sha256':digest,'git_head':run('git','rev-parse','HEAD'),'working_tree_dirty':bool(run('git','status','--porcelain')),'go_version':run('go','version'),'build_info':build_info,'source_license_status':'declared' if any(m.get('Main') and m.get('license_files') for m in modules) else 'not_declared','modules':modules}
(out/'release-metadata.json').write_text(json.dumps(record,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
(out/'THIRD_PARTY_NOTICES.txt').write_text('\n\n'.join(notices),encoding='utf-8')
artifact.with_suffix(artifact.suffix+'.sha256').write_text(digest+'  '+artifact.name+'\n',encoding='utf-8')
missing=[m['Path'] for m in modules if not m.get('Main') and not m['license_files']]
print(json.dumps({'artifact':artifact.name,'sha256':digest,'modules':len(modules),'missing_root_license':missing}))
if missing:raise SystemExit('Dependency license metadata incomplete')
