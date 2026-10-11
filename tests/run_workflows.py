"""Manage isolated API/UI servers and always stop them after browser checks."""
import argparse
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
from urllib.request import urlopen

def main():
    parser=argparse.ArgumentParser();parser.add_argument('--channel');parser.add_argument('--api-port',type=int,default=8096);parser.add_argument('--web-port',type=int,default=5186);args=parser.parse_args()
    root=Path(__file__).resolve().parents[1];go=shutil.which('go');npm=shutil.which('npm')
    if not go or not npm:raise RuntimeError('Go and npm must be on PATH.')
    for port in (args.api_port,args.web_port):
        with socket.socket() as s:
            if s.connect_ex(('127.0.0.1',port))==0:raise RuntimeError(f'Test port {port} is already in use.')
    with tempfile.TemporaryDirectory(prefix='gridpulse-test-') as tmp:
        api=f'http://127.0.0.1:{args.api_port}';web=f'http://127.0.0.1:{args.web_port}'
        env={**os.environ,'GRIDPULSE_DB_PATH':str(Path(tmp)/'test.db'),'GRIDPULSE_ADDR':f'127.0.0.1:{args.api_port}','GRIDPULSE_ALLOWED_ORIGIN':web,'VITE_API_URL':api}
        processes=[];logs=[]
        try:
            for cmd,cwd,url in [([go,'run','.'],root/'backend',api+'/api/health'),([npm,'run','dev','--','--port',str(args.web_port),'--strictPort'],root/'frontend',web)]:
                log=open(Path(tmp)/f'{len(logs)}.log','w+');logs.append(log)
                process=subprocess.Popen(cmd,cwd=cwd,env=env,stdout=log,stderr=subprocess.STDOUT,start_new_session=True);processes.append(process)
                deadline=time.monotonic()+45
                while time.monotonic()<deadline:
                    if process.poll() is not None:raise RuntimeError('A test server exited before becoming ready.')
                    try:
                        with urlopen(url,timeout=1) as response:
                            if response.status==200:break
                    except OSError:time.sleep(.2)
                else:raise RuntimeError('Test server startup timed out.')
            cmd=[sys.executable,str(root/'tests/test_workflows.py'),'--api-url',api,'--base-url',web]
            if args.channel:cmd+=['--channel',args.channel]
            result=subprocess.run(cmd,cwd=root,env=env)
            if result.returncode:
                for log in logs:log.flush();log.seek(0);print(log.read(),file=sys.stderr)
            return result.returncode
        finally:
            for process in processes:
                try:os.killpg(process.pid,signal.SIGTERM);process.wait(timeout=5)
                except (ProcessLookupError,subprocess.TimeoutExpired):
                    try:os.killpg(process.pid,signal.SIGKILL)
                    except ProcessLookupError:pass
            for log in logs:log.close()

if __name__=='__main__':raise SystemExit(main())
