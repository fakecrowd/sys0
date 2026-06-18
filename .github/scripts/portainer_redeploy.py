#!/usr/bin/env python3
"""Pull ghcr.io/<repo>/sys0:latest and recreate the sys0 container on jp09 via
the Portainer-proxied Docker API.

Why not the stack webhook? sys0 is a NON-git (raw compose) Portainer stack.
Its webhook keys off the compose content hash, not the image digest, so a
code-only change (compose unchanged) never repulls — and in fact no webhook is
configured on the stack, so the old CI step got HTTP 404. Recreating the
container directly is the reliable path. See skill sys0-agent-hub-dev.

Auth: X-API-Key header (Portainer access token in $PORTAINER_API_KEY). The key
is read from the environment and only ever set via request.add_header at
runtime, so it never appears as a literal in any logged command.

Idempotent: if the running container is already on the freshly-pulled image
digest, it does nothing. Exit non-zero on any failure so CI goes red for real
problems (not the cosmetic 404 we just removed).
"""
import json
import os
import sys
import time
import urllib.error
import urllib.request

BASE = os.environ["PORTAINER_URL"].rstrip("/")
APIKEY = os.environ["PORTAINER_API_KEY"]
EP = os.environ.get("ENDPOINT", "22")
NAME = os.environ.get("CONTAINER", "sys0")
STACK = os.environ.get("STACK", "133")  # Portainer stack id (recreate fallback)
REPO = os.environ.get("GITHUB_REPOSITORY", "fakecrowd/sys0")
IMAGE = f"ghcr.io/{REPO}:latest"
DOCKER = f"/api/endpoints/{EP}/docker"


def req(path, method="GET", body=None, raw=False):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(BASE + path, data=data, method=method)
    r.add_header("Content-Type", "application/json")
    r.add_header("X-API-Key", APIKEY)  # built at runtime; never a literal arg
    try:
        resp = urllib.request.urlopen(r, timeout=300).read()
    except urllib.error.HTTPError as e:
        body = e.read().decode("utf-8", "replace")
        print(f"::error::Portainer {method} {path} -> HTTP {e.code}: {body[:300]}")
        raise
    return resp if raw else (json.loads(resp) if resp else None)


def find_sys0():
    conts = req(DOCKER + "/containers/json?all=1")
    for x in conts:
        if any(NAME in n.strip("/").split("/") or n.strip("/") == NAME
               for n in x.get("Names", [])):
            return x
    # looser fallback: substring match
    for x in conts:
        if any(NAME in n for n in x.get("Names", [])):
            return x
    return None


def redeploy_stack():
    """Recreate the sys0 container from its Portainer stack definition (used
    when the container is missing -- e.g. a prior run removed it then timed
    out). The named volume persists, so the DB/accounts survive."""
    f = req(f"/api/stacks/{STACK}/file")
    st = req(f"/api/stacks/{STACK}")
    req(f"/api/stacks/{STACK}?endpointId={EP}", "PUT", {
        "StackFileContent": f["StackFileContent"],
        "Env": st.get("Env") or [],
        "Prune": False,
        "PullImage": True,
    })


def main():
    print(f"==> pulling {IMAGE} on endpoint {EP} ...")
    # GHCR pulls from the remote node occasionally hit transient TLS-handshake
    # timeouts (Portainer surfaces them as HTTP 500). Retry with backoff so a
    # network blip doesn't red the pipeline.
    pull = DOCKER + f"/images/create?fromImage=ghcr.io%2F{REPO.replace('/', '%2F')}&tag=latest"
    for attempt in range(1, 5):
        try:
            req(pull, "POST", None, raw=True)
            break
        except urllib.error.HTTPError as e:
            if attempt == 4:
                raise
            print(f"==> pull attempt {attempt} failed (HTTP {e.code}); retrying in {attempt * 10}s ...")
            time.sleep(attempt * 10)

    imgs = req(DOCKER + "/images/json")
    newid = None
    for im in imgs:
        if IMAGE in (im.get("RepoTags") or []):
            newid = im["Id"]
            break
    if not newid:
        print(f"::error::pulled but {IMAGE} not found in image list")
        sys.exit(1)

    old = find_sys0()
    if old is None:
        # A previous run may have removed the old container then timed out
        # before recreating it (slow GHCR pull). The runtime config is gone,
        # so clone-from-inspect is impossible -- redeploy the Portainer stack,
        # which recreates it from the canonical compose. Self-heals reruns.
        print(f"==> no '{NAME}' container on endpoint {EP}; redeploying stack {STACK} ...")
        redeploy_stack()
        time.sleep(3)
        cur = find_sys0()
        if not cur or cur["State"] != "running":
            print(f"::error::stack redeploy did not bring up '{NAME}': {cur and cur.get('Status')}")
            sys.exit(1)
        print(f"==> sys0 is {cur['State']} ({cur['Status']}) via stack redeploy")
        return
    print(f"    running ImageID: {old['ImageID'][:19]}")
    print(f"    latest  ImageID: {newid[:19]}")

    if old["ImageID"] == newid:
        print("==> already on latest image — nothing to recreate.")
    else:
        insp = req(DOCKER + f"/containers/{old['Id']}/json")
        cfg, hostcfg = insp["Config"], insp["HostConfig"]
        netcfg = insp.get("NetworkSettings", {}).get("Networks", {})
        print("==> stopping + removing old container ...")
        req(DOCKER + f"/containers/{old['Id']}/stop?t=10", "POST", None, raw=True)
        req(DOCKER + f"/containers/{old['Id']}?force=true", "DELETE", None, raw=True)
        print("==> creating + starting new container ...")
        new = req(DOCKER + f"/containers/create?name={NAME}", "POST", {
            "Image": IMAGE,
            "Env": cfg.get("Env"),
            "Cmd": cfg.get("Cmd"),
            "Entrypoint": cfg.get("Entrypoint"),
            "Labels": cfg.get("Labels"),
            "ExposedPorts": cfg.get("ExposedPorts"),
            "HostConfig": hostcfg,
            "NetworkingConfig": {"EndpointsConfig": netcfg},
        })
        req(DOCKER + f"/containers/{new['Id']}/start", "POST", None, raw=True)
        print(f"    recreated: {new['Id'][:19]}")

    time.sleep(3)
    cur = find_sys0()
    if not cur or cur["State"] != "running":
        print(f"::error::sys0 not running after deploy: {cur and cur.get('Status')}")
        sys.exit(1)
    print(f"==> sys0 is {cur['State']} ({cur['Status']}), ImageID {cur['ImageID'][:19]}")
    if cur["ImageID"] != newid:
        print("::error::container did not pick up the new image")
        sys.exit(1)
    print("==> deploy OK")


if __name__ == "__main__":
    main()
