#!/usr/bin/env python3
"""Real hub+agent ownership regression fixture (stdlib, no production targets)."""
import argparse
import concurrent.futures
import json
import os
from pathlib import Path
import secrets
import signal
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--bin-dir', required=True)
    parser.add_argument('--serve', action='store_true')
    parser.add_argument('--state', default='/tmp/sys0-ownership-fixture.json')
    args = parser.parse_args()
    binaries = Path(args.bin_dir).resolve()
    processes, logs = [], []
    checks = []
    with tempfile.TemporaryDirectory(prefix='sys0-ownership-') as directory:
        base = Path(directory)
        port, tcp = free_port(), free_port()
        url = f'http://127.0.0.1:{port}'
        password, agent_key = secrets.token_urlsafe(18), secrets.token_urlsafe(18)
        def launch(name, argv, env=None):
            log = open(base / (name + '.log'), 'wb')
            logs.append(log)
            proc = subprocess.Popen(argv, stdout=log, stderr=subprocess.STDOUT, env=env)
            processes.append(proc)
            return proc
        def call(path, token=None, body=None, method=None):
            headers = {'Content-Type': 'application/json'}
            if token:
                headers['Authorization'] = 'Bearer ' + token
            request = urllib.request.Request(url + path, headers=headers,
                data=json.dumps(body).encode() if body is not None else None,
                method=method or ('POST' if body is not None else 'GET'))
            try:
                response = urllib.request.urlopen(request, timeout=15)
                return response.status, json.loads(response.read())
            except urllib.error.HTTPError as error:
                return error.code, json.loads(error.read())
        def success(path, token=None, body=None, method=None):
            status, result = call(path, token, body, method)
            assert status == 200 and result.get('ok', True), (path, status, result)
            return result
        def record(name):
            checks.append(name)
            print('PASS:', name, flush=True)
        def nodes(token):
            return success('/api/v1/nodes', token)['nodes']
        def dispatch(token, node, method='shell.run', params=None):
            return call('/api/v1/dispatch', token, {
                'select': {'nodes': [node]},
                'call': {'method': method, 'params': params or {'cmd': 'printf ownership-ok', 'timeout': 5}},
                'timeout': 8,
            })
        def allowed(token, node):
            status, response = dispatch(token, node)
            assert status == 200 and response.get('ok'), response
            item = response['items'][0]
            assert item['ok'] and item['value']['stdout'] == 'ownership-ok', response
        def denied(token, node):
            status, response = dispatch(token, node)
            assert status in (200, 401, 403, 404), response
            assert not any(item.get('ok') for item in response.get('items', [])), response
            if status == 200:
                assert response.get('ok') is False or response.get('items'), response
        env = dict(os.environ, SYS0_ADMIN_USER='instance_owner', SYS0_ADMIN_PASS=password)
        try:
            launch('hub', [str(binaries / 'sys0-hub'), '-http', f'127.0.0.1:{port}',
                           '-agent-tcp', f'127.0.0.1:{tcp}', '-db', str(base / 'hub.db'),
                           '-key', agent_key, '-jwt-secret', secrets.token_urlsafe(24)], env)
            deadline = time.monotonic() + 20
            while True:
                try:
                    success('/api/v1/setup/status')
                    break
                except (OSError, urllib.error.URLError):
                    if time.monotonic() > deadline:
                        raise
                    time.sleep(.1)
            admin = success('/api/v1/auth/login', body={'username': 'instance_owner', 'password': password})['token']
            accounts = {}
            for username in ['alice', 'bob', 'temporary_owner']:
                user = success('/api/v1/users', admin, {'username': username, 'password': password, 'role': 'member'})['user']
                token = success('/api/v1/auth/login', body={'username': username, 'password': password})['token']
                accounts[username] = {'id': user['id'], 'token': token}
            for index in range(2):
                launch(f'agent{index}', [str(binaries / 'sys0-agent'), '-hub', f'127.0.0.1:{tcp}',
                    '-transport', 'tcp', '-key', agent_key, '-label', f'Ownership fixture {index+1}',
                    '-heartbeat', '2', '-data-dir', str(base / f'agent{index}')])
            deadline = time.monotonic() + 20
            while len(nodes(admin)) != 2:
                assert time.monotonic() < deadline, 'agents did not connect'
                time.sleep(.1)
            node1, node2 = [node['id'] for node in nodes(admin)]
            alice, bob = accounts['alice']['token'], accounts['bob']['token']
            public = nodes(alice)
            assert len(public) == 2 and all(n.get('canClaim') and not n.get('canAccess') and not n.get('owner') for n in public), public
            assert all(not n.get('rescueInfo') and not n.get('agentCwd') for n in public), public
            denied(alice, node1)
            record('unowned discovery is claimable but not operable')
            assert call(f'/api/v1/nodes/{node1}/claim', body={})[0] == 401
            with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
                results = list(pool.map(lambda name: (name, *call(f'/api/v1/nodes/{node1}/claim', accounts[name]['token'], {})), ['alice', 'bob']))
            assert sorted(item[1] for item in results) == [200, 409], results
            owner_name = next(item[0] for item in results if item[1] == 200)
            other_name = next(item[0] for item in results if item[1] == 409)
            owner, other = accounts[owner_name]['token'], accounts[other_name]['token']
            assert next(n for n in nodes(owner) if n['id'] == node1)['owner'] == owner_name
            assert not any(n['id'] == node1 for n in nodes(other))
            allowed(owner, node1)
            record('concurrent claim has one winner; owner can operate')
            assert call(f'/api/v1/nodes/{node1}/access', other)[0] == 403
            assert call(f'/api/v1/nodes/{node1}/access', other, {'users': [other_name]})[0] == 403
            success(f'/api/v1/nodes/{node1}/access', owner, {'users': [other_name]})
            allowed(other, node1)
            assert call(f'/api/v1/nodes/{node1}/access', other, {'users': []})[0] == 403
            assert call(f'/api/v1/nodes/{node1}', other, method='DELETE')[0] == 403
            record('binary grant permits operation but not ACL management or node deletion')
            key = success('/api/v1/me/keys', other, {'name': 'fixture-full', 'methodScope': []})
            sk = key['key']
            allowed(sk, node1)
            assert call(f'/api/v1/nodes/{node1}/claim', sk, {})[0] == 403
            assert call(f'/api/v1/nodes/{node1}/access', sk, {'users': []})[0] == 403
            success(f'/api/v1/nodes/{node1}/access', owner, {'users': []})
            denied(other, node1)
            denied(sk, node1)
            assert call('/api/v1/metrics?node=' + node1, other)[0] in (403, 404)
            assert call('/api/v1/audit', other)[0] == 403
            mcp_status, mcp = call('/mcp', other, {'jsonrpc': '2.0', 'id': 'audit', 'method': 'resources/read', 'params': {'uri': 'sys0://audit'}})
            assert mcp_status == 200 and ('error' in mcp or mcp.get('result', {}).get('isError')), mcp
            record('revocation affects existing API key, REST metrics and MCP audit access')
            success(f'/api/v1/nodes/{node1}/access', admin, {'users': [other_name]})
            allowed(other, node1)
            limited = success('/api/v1/me/keys', other, {'name': 'fixture-scoped', 'methodScope': ['host.info']})['key']
            denied(limited, node1)
            code, response = dispatch(limited, node1, 'host.info', {})
            assert code == 200 and response['items'][0]['ok'], response
            assert call('/api/v1/users', other, {'username': 'forbidden', 'password': password, 'role': 'admin'})[0] == 403
            record('instance owner overrides node ACL; method-scoped keys remain narrowed')
            temporary = accounts['temporary_owner']
            success(f'/api/v1/nodes/{node2}/claim', temporary['token'], {})
            success(f'/api/v1/users/{temporary["id"]}', admin, method='DELETE')
            replacement = success('/api/v1/users', admin, {'username': 'temporary_owner', 'password': password, 'role': 'member'})['user']
            assert replacement['id'] != temporary['id']
            replacement_token = success('/api/v1/auth/login', body={'username': 'temporary_owner', 'password': password})['token']
            denied(replacement_token, node2)
            assert not any(n['id'] == node2 for n in nodes(replacement_token))
            assert next(n for n in nodes(admin) if n['id'] == node2)['owner'], 'deleted owner made node publicly claimable'
            record('deleted/recreated account cannot recover previous node ownership')
            # Keep a safe owner/grantee state for optional real-browser follow-up.
            state = {'url': url, 'password': password, 'admin': admin, 'accounts': accounts,
                     'ownerName': owner_name, 'otherName': other_name, 'node': node1, 'secondNode': node2,
                     'checks': checks, 'pid': os.getpid()}
            if args.serve:
                target = Path(args.state)
                fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
                with os.fdopen(fd, 'w') as stream:
                    json.dump(state, stream)
                print('FIXTURE_READY ' + str(target), flush=True)
                signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
                while True:
                    time.sleep(1)
            print(json.dumps({'ok': True, 'checks': checks}), flush=True)
        except KeyboardInterrupt:
            pass
        except BaseException:
            for path in base.glob('*.log'):
                print('DIAGNOSTIC', path.name, path.read_text(errors='replace')[-5000:], flush=True)
            raise
        finally:
            for proc in reversed(processes):
                if proc.poll() is None:
                    proc.terminate()
            for proc in reversed(processes):
                try:
                    proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait(timeout=5)
            for log in logs:
                log.close()
            if args.serve:
                Path(args.state).unlink(missing_ok=True)


if __name__ == '__main__':
    main()
