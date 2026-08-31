#!/usr/bin/env python3
"""Reliable GitOps deployment of sys0 to jp09 via Portainer."""
import json, os, subprocess, sys, tempfile, time, urllib.parse
URL=os.environ.get('PORTAINER_URL','').rstrip('/')
HOST=urllib.parse.urlparse(URL).hostname or ''
ORIGIN=os.environ.get('PORTAINER_ORIGIN_IP','')
KEY=os.environ.get('PORTAINER_API_KEY',''); PAT=os.environ.get('DEPLOY_REPO_TOKEN','')
EP=os.environ.get('ENDPOINT','4'); STACK=os.environ.get('STACK','48'); IMAGE=os.environ.get('IMAGE','ghcr.io/fakecrowd/sys0')
if not URL or not HOST or not ORIGIN or not KEY or not PAT: print('::error::missing deployment configuration');sys.exit(1)
fd,cfg=tempfile.mkstemp(prefix='portainer-',text=True);os.close(fd);os.chmod(cfg,0o600);open(cfg,'w').write('header = "X-API-Key: '+KEY+'"\n')
def req(path,method='GET',body=None,raw=False,ok=(200,201,204),timeout=300):
 c=['curl','--config',cfg,'--noproxy','*','--resolve',f'{HOST}:443:{ORIGIN}','-sS','--max-time',str(timeout),'-X',method,'-w','\n%{http_code}']
 if body is not None:c+=['-H','Content-Type: application/json','--data',json.dumps(body)]
 c+=[URL+path];out=subprocess.check_output(c);payload,status=out.rsplit(b'\n',1);status=int(status)
 if status not in ok:raise RuntimeError(f'{method} {path} -> HTTP {status}: {payload[:500].decode(errors="replace")}')
 if raw:return payload
 return json.loads(payload) if payload else {}
try:
 imgq=urllib.parse.quote(IMAGE,safe='');pulled=False
 for i in range(1,6):
  try:
   out=req(f'/api/endpoints/{EP}/docker/images/create?fromImage={imgq}&tag=latest','POST',body={},raw=True,timeout=300)
   print(f'pull attempt {i}: HTTP 200');pulled=True;break
  except Exception as e:
   print(f'pull attempt {i} failed: {e}');time.sleep(i*8)
 if not pulled:raise RuntimeError('image pull failed after 5 attempts')
 stack=req(f'/api/stacks/{STACK}');env=stack.get('Env') or []
 body={'repositoryReferenceName':'refs/heads/main','repositoryAuthentication':True,'repositoryUsername':'fakecrowd','repositoryPassword':PAT,'env':env,'prune':False,'pullImage':True}
 req(f'/api/stacks/{STACK}/git/redeploy?endpointId={EP}','PUT',body,timeout=300);print('git redeploy: HTTP 200')
 time.sleep(8)
 images=req(f'/api/endpoints/{EP}/docker/images/json');latest=next(x['Id'] for x in images if IMAGE+':latest' in (x.get('RepoTags') or []))
 containers=req(f'/api/endpoints/{EP}/docker/containers/json?all=1');cur=next((x for x in containers if '/sys0' in (x.get('Names') or [])),None)
 if not cur or cur.get('State')!='running':raise RuntimeError('sys0 missing or not running after redeploy')
 if cur.get('ImageID')!=latest:raise RuntimeError(f'running ImageID {cur.get("ImageID")} != latest {latest}')
 print('sys0 running',cur.get('Status'),'ImageID',latest[:24])
 public=subprocess.check_output(['curl','-fsS','--max-time','30','https://sys0.facrd.xyz/api/v1/setup/status'])
 status=json.loads(public)
 if status.get('needsSetup') is not False:raise RuntimeError('public setup status is not initialized')
 print('public verification OK');print('deploy OK')
finally:
 try:os.unlink(cfg)
 except FileNotFoundError:pass
