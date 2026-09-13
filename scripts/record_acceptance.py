"""Run bounded offline release checks and retain exact local evidence."""
from datetime import datetime,timezone
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import time

root=Path(__file__).resolve().parents[1]
out=root/'.clash-tokens/release-evidence'
out.mkdir(parents=True,exist_ok=True)
suffix='.exe' if os.name=='nt' else ''
artifact='dist/clash-tokens'+suffix
tracked=subprocess.check_output(['git','ls-files','-z','--cached','--others','--exclude-standard'],cwd=root).decode().split('\0')
digest=hashlib.sha256()
for name in sorted(set(tracked)):
    path=root/name
    if path.is_file() and (path.suffix in ('.go','.js','.html','.py','.yml','.sh') or name in ('go.mod','go.sum')):
        digest.update(name.encode()+b'\0'+hashlib.sha256(path.read_bytes()).digest())
report={'started_at':datetime.now(timezone.utc).isoformat(),'git_head':subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),'code_snapshot_sha256':digest.hexdigest(),'working_tree_dirty':bool(subprocess.check_output(['git','status','--porcelain'],cwd=root)),'platform':sys.platform,'scope':'Local offline acceptance only. Browser sites/upstreams are synthetic. Hosted CI, live accounts/devices, all browser brands and 24-hour mixed stability remain separate gates.','checks':[]}
checks=[
 ('go-tests',['go','test','./...','-count=1','-timeout','180s'],240),
 ('go-vet',['go','vet','./...'],120),
 ('redaction',['python','scripts/redaction_audit.py'],30),
 ('browser-engines',['python','scripts/browser_engine_tests.py'],180),
 ('browser-providers',['python','scripts/browser_provider_tests.py'],240),
 ('ui-build',['go','build','-o','.clash-tokens/ui-test.exe','./cmd/clash-tokens'],120),
 ('ui',['python','scripts/ui_fixture.py','--test'],240),
 ('distribution-build',['go','build','-trimpath','-ldflags=-s -w','-o',artifact,'./cmd/clash-tokens'],120),
 ('distribution-vault',['python','scripts/artifact_vault_smoke.py',artifact],90),
 ('distribution-metadata',['python','scripts/release_metadata.py',artifact],60),
 ('vulnerabilities',['go','run','golang.org/x/vuln/cmd/govulncheck@v1.1.4','./...'],180),
]
for name,command,timeout in checks:
    print('Checking '+name,flush=True)
    start=time.monotonic()
    with (out/(name+'.log')).open('w',encoding='utf-8') as log:
        try: result=subprocess.run(command,cwd=root,stdout=log,stderr=subprocess.STDOUT,timeout=timeout);code=result.returncode
        except subprocess.TimeoutExpired:code=124
    report['checks'].append({'name':name,'command':command,'exit_code':code,'seconds':round(time.monotonic()-start,3),'log':name+'.log'})
    report['updated_at']=datetime.now(timezone.utc).isoformat()
    (out/'acceptance.json').write_text(json.dumps(report,indent=2)+'\n',encoding='utf-8')
    print(name+': '+('passed' if code==0 else 'FAILED'),flush=True)
    if code:raise SystemExit('Check failed; see '+str(out/(name+'.log')))
print('Offline acceptance recorded: '+str(out/'acceptance.json'),flush=True)
