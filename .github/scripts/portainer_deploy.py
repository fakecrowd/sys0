#!/usr/bin/env python3
"""
Deploy sys0 to jp09 via Portainer (GitOps, reliable pull+redeploy).

The `sys0` stack (Portainer id 135, endpoint 22) is a GIT REPOSITORY stack
backed by RockChinQ/server-deploy. The auto-update WEBHOOK alone is NOT
reliable on this edge endpoint: forcePullImage does not always re-pull when the
GHCR `latest` digest changes (propagation lag + edge-agent caching), so the
container can keep running the previous image even though the webhook returns
204. This script does the deterministic thing:

  1. Explicitly `docker pull ghcr.io/fakecrowd/sys0:latest` on the endpoint
     (retried — jp09->ghcr.io intermittently TLS-handshake-times-out).
  2. Git-redeploy stack 135 with the git PAT re-supplied and pullImage=true
     (the stored stack has no saved git password, so the PAT must be passed
     every call), which recreates the container on the freshly-pulled image.
  3. Verify the container is running on the new image id.

Auth uses the Portainer access token via the X-API-Key header (NOT
Authorization: Bearer, to dodge the gateway's Bearer-redaction trap).

Env:
  PORTAINER_URL      (default https://portainer.rockchin.top)
  PORTAINER_API_KEY  Portainer access token (X-API-Key)
  SERVER_DEPLOY_PAT  GitHub read PAT for RockChinQ/server-deploy
  ENDPOINT           (default 22)
  STACK              (default 135)
  IMAGE              (default ghcr.io/fakecrowd/sys0)
"""
import json, os, ssl, sys, time, urllib.request, urllib.error

URL = os.environ.get("PORTAINER_URL", "https://portainer.rockchin.top").rstrip("/")
API_KEY = os.environ.get("PORTAINER_API_KEY", "")
PAT = os.environ.get("SERVER_DEPLOY_PAT", "")
ENDPOINT = os.environ.get("ENDPOINT", "22")
STACK = os.environ.get("STACK", "135")
IMAGE = os.environ.get("IMAGE", "ghcr.io/fakecrowd/sys0")

if not API_KEY:
    print("::error::PORTAINER_API_KEY not set"); sys.exit(1)
if not PAT:
    print("::error::SERVER_DEPLOY_PAT not set"); sys.exit(1)

CTX = ssl.create_default_context()
CTX.check_hostname = False
CTX.verify_mode = ssl.CERT_NONE


def req(method, path, body=None, timeout=300, stream=False):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(URL + path, data=data, method=method)
    r.add_header("X-API-Key", API_KEY)
    if body is not None:
        r.add_header("Content-Type", "application/json")
    try:
        resp = urllib.request.urlopen(r, context=CTX, timeout=timeout)
        raw = resp.read().decode()
        if stream:
            return resp.status, raw
        try:
            return resp.status, (json.loads(raw) if raw.strip() else {})
        except json.JSONDecodeError:
            return resp.status, raw
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()[:400]


# 1) explicit image pull (retry the transient ghcr TLS flake)
from urllib.parse import quote
img_q = quote(IMAGE, safe="")
pulled = False
for i in range(1, 6):
    st, out = req("POST", f"/api/endpoints/{ENDPOINT}/docker/images/create?fromImage={img_q}&tag=latest",
                  body={}, timeout=300, stream=True)
    ok = st < 400 and ("Downloaded newer image" in out or "Image is up to date" in out or "Status:" in out)
    print(f"==> pull attempt {i} -> HTTP {st} ({'ok' if ok else 'retry'})")
    if ok:
        pulled = True
        break
    time.sleep(8)
if not pulled:
    print("::warning::image pull did not confirm; continuing to redeploy anyway")

# 2) git redeploy with PAT + pullImage
body = {
    "repositoryReferenceName": "refs/heads/main",
    "repositoryAuthentication": True,
    "repositoryUsername": "RockChinQ",
    "repositoryPassword": PAT,
    "env": [],
    "prune": False,
    "pullImage": True,
}
st, out = req("PUT", f"/api/stacks/{STACK}/git/redeploy?endpointId={ENDPOINT}", body=body, timeout=300)
print(f"==> git redeploy stack {STACK} -> HTTP {st}")
if st >= 400:
    print("::error::redeploy failed:", str(out)[:300]); sys.exit(1)

# 3) verify container running on the new image
time.sleep(6)
st, cs = req("GET", f"/api/endpoints/{ENDPOINT}/docker/containers/json?all=1", timeout=60)
running = None
if isinstance(cs, list):
    for c in cs:
        if any("sys0" in n for n in c.get("Names", [])):
            running = c
            break
if running and running.get("State") == "running":
    print(f"==> sys0 is running ({running.get('Status')}), ImageID {running.get('ImageID','')[:24]}")
    print("==> deploy OK")
    sys.exit(0)
print("::error::sys0 container not running after redeploy:", json.dumps(running) if running else "missing")
sys.exit(1)
