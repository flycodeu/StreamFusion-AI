"""Real serve/stop lifecycle test using isolated state and a random loopback port."""
import argparse
import json
from pathlib import Path
import secrets
import socket
import sqlite3
import subprocess
import time
import urllib.request

p=argparse.ArgumentParser()
p.add_argument('--binary',required=True)
p.add_argument('--output',required=True)
args=p.parse_args()
binary=str(Path(args.binary).resolve())
output=Path(args.output).resolve()
output.mkdir(parents=True,exist_ok=True)
state=output/'state'
password_file=output/'.password.secret'
password_file.write_text('Stop-'+secrets.token_hex(12),encoding='utf-8')
password_file.chmod(0o600)
subprocess.run([binary,'init-admin','--state',str(state),'--password-file',str(password_file)],check=True,timeout=20)
password_file.unlink()
with sqlite3.connect(state/'controller.sqlite') as db:
    identity=db.execute('SELECT controller_id FROM controller_state').fetchone()[0]
with socket.socket() as sock:
    sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
params=[binary,'serve','--state',str(state),'--listen',f'127.0.0.1:{port}']
log=(output/'runtime.log').open('w',encoding='utf-8')
process=None
checks=[]
try:
    for attempt in range(2):
        process=subprocess.Popen(params,stdout=log,stderr=log)
        for _ in range(80):
            try:
                urllib.request.urlopen(f'http://127.0.0.1:{port}/ops/api/v1/health',timeout=1).close()
                break
            except OSError:
                assert process.poll() is None,'serve exited before ready'
                time.sleep(.1)
        else:raise AssertionError('serve did not become ready')
        duplicate=subprocess.run(params,capture_output=True,text=True,encoding='utf-8',errors='replace',timeout=10)
        assert duplicate.returncode!=0 and 'STATE_LOCKED' in duplicate.stderr and ' stop ' in duplicate.stderr
        stopped=subprocess.run([binary,'stop','--state',str(state),'--timeout','10s'],capture_output=True,text=True,encoding='utf-8',errors='replace',timeout=15)
        assert stopped.returncode==0,stopped.stderr
        assert process.wait(timeout=5)==0
        assert not (state/'controller.runtime.json').exists()
        assert not (state/'controller.stop.json').exists()
        assert (state/'master.key').exists() and (state/'controller.sqlite').exists()
        with sqlite3.connect(state/'controller.sqlite') as db:
            assert db.execute('SELECT controller_id FROM controller_state').fetchone()[0]==identity
        checks.append('graceful stop releases state lock and preserves identity '+str(attempt+1))
    assert subprocess.run([binary,'stop','--state',str(state)],capture_output=True,encoding='utf-8',errors='replace',timeout=10).returncode==0
    result={'status':'PASS','checks':checks+['duplicate-start actionable error','restart after stop','repeat stop idempotent'],'scope':'isolated real binary and state'}
    (output/'result.json').write_text(json.dumps(result,ensure_ascii=False,indent=2),encoding='utf-8')
    print(json.dumps(result,ensure_ascii=False))
finally:
    if process and process.poll() is None:
        process.terminate();process.wait(timeout=10)
    log.close()
