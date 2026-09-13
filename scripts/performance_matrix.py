"""Bounded same-process mock throughput sweep, with explicit measurement scope."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import time
try:import psutil
except ImportError:psutil=None

root=Path(__file__).resolve().parents[1]
binary=root/'.clash-tokens'/('cot-bench.exe' if os.name=='nt' else 'cot-bench')
out=root/'.clash-tokens/release-evidence'
if os.name!='nt':out=out/'linux'
out.mkdir(parents=True,exist_ok=True)
cases=[(n,0,max(n*2,256)) for n in (32,128,512,1000)]+[(1,m<<20,4) for m in (1,8,16)]
report={'scope':'Short loopback sweep. CPU/RSS/handles include mock, client and Go gateway in one benchmark process; browser/device costs and 24-hour mixed stability are NOT measured. Peaks sampled every 25 ms.','artifact_sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'cases':[]}
if '--resume' in sys.argv:
    prior=json.loads((out/'performance.json').read_text(encoding='utf-8'))
    if prior['artifact_sha256']!=report['artifact_sha256']:raise SystemExit('Benchmark binary changed; run a fresh matrix')
    report=prior
for concurrency,size,count in cases:
    name=f'c{concurrency}-b{size}'
    if any(c['name']==name for c in report['cases']):continue
    with (out/(name+'.json')).open('w',encoding='utf-8') as stdout,(out/(name+'.stderr')).open('w',encoding='utf-8') as stderr:
        process=subprocess.Popen([str(binary),'-concurrency',str(concurrency),'-requests',str(count),'-body-bytes',str(size),'-delay','10ms'],cwd=root,stdout=stdout,stderr=stderr,creationflags=subprocess.CREATE_NO_WINDOW if os.name=='nt' else 0)
        observed=psutil.Process(process.pid) if psutil else None
        peak_rss=peak_handles=0
        cpu_seconds=0.0
        start=time.monotonic()
        while process.poll() is None:
            try:
                if observed:
                    peak_rss=max(peak_rss,observed.memory_info().rss)
                    peak_handles=max(peak_handles,observed.num_handles() if os.name=='nt' else observed.num_fds())
                    cpu=observed.cpu_times();cpu_seconds=cpu.user+cpu.system
                else:
                    proc=Path('/proc')/str(process.pid)
                    fields=(proc/'stat').read_text().rsplit(')',1)[1].split()
                    cpu_seconds=(int(fields[11])+int(fields[12]))/os.sysconf('SC_CLK_TCK')
                    rss=next((int(line.split()[1])*1024 for line in (proc/'status').read_text().splitlines() if line.startswith('VmRSS:')),0)
                    peak_rss=max(peak_rss,rss);peak_handles=max(peak_handles,len(list((proc/'fd').iterdir())))
            except (OSError,ProcessLookupError):break
            if time.monotonic()-start>120:
                process.kill();process.wait();raise RuntimeError('bounded benchmark timed out')
            time.sleep(.025)
        code=process.wait()
    result=json.loads((out/(name+'.json')).read_text(encoding='utf-8'))
    report['cases'].append({'name':name,'exit_code':code,'peak_process_rss_bytes':peak_rss,'peak_handles_or_fds':peak_handles,'descriptor_metric':'handles' if os.name=='nt' else 'file_descriptors','sampled_cpu_seconds':cpu_seconds,'benchmark':result})
    (out/'performance.json').write_text(json.dumps(report,indent=2)+'\n',encoding='utf-8')
    gateway=result['results'][1]
    print(json.dumps({'case':name,'errors':gateway['errors'],'rps':gateway['requests_per_second'],'ttft_p99_ms':gateway['ttft_p99_ms'],'peak_rss_mib':round(peak_rss/1048576,2)}),flush=True)
if any(c['exit_code'] for c in report['cases']):raise SystemExit('Matrix recorded with failed cases; inspect durable result')
