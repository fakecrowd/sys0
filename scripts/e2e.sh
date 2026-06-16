#!/usr/bin/env bash
# End-to-end smoke test for sys0: boots the hub + two agents (tcp & ws),
# then drives the REST API and MCP endpoint, asserting real behavior.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

HTTP_PORT=18090
TCP_PORT=17010
DB=/tmp/sys0_e2e.db
KEY=testkey
B="http://127.0.0.1:${HTTP_PORT}"
PIDS=()

cleanup() {
  for p in "${PIDS[@]:-}"; do kill -9 "$p" 2>/dev/null || true; done
  rm -f "$DB"
}
trap cleanup EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
jq_py() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

echo "== build =="
mkdir -p bin
go build -o bin/sys0-hub ./sys0-hub/
go build -o bin/sys0-agent ./sys0-agent/

echo "== start hub =="
rm -f "$DB"
./bin/sys0-hub -http ":${HTTP_PORT}" -agent-tcp ":${TCP_PORT}" -key "$KEY" -db "$DB" \
  -jwt-secret e2esecret >/tmp/sys0_e2e_hub.log 2>&1 &
PIDS+=($!)
sleep 1

echo "== start agents (tcp + ws) =="
mkdir -p /tmp/sys0_e2e_d1 /tmp/sys0_e2e_d2
./bin/sys0-agent -hub "127.0.0.1:${TCP_PORT}" -transport tcp -key "$KEY" -label e2e-tcp -heartbeat 5 -data-dir /tmp/sys0_e2e_d1 >/tmp/sys0_e2e_a1.log 2>&1 &
PIDS+=($!)
./bin/sys0-agent -hub "127.0.0.1:${HTTP_PORT}" -transport ws -key "$KEY" -label e2e-ws -heartbeat 5 -data-dir /tmp/sys0_e2e_d2 >/tmp/sys0_e2e_a2.log 2>&1 &
PIDS+=($!)
sleep 2

echo "== login =="
TOKEN=$(curl -s -X POST "$B/api/v1/auth/login" -d '{"username":"admin","password":"admin"}' | jq_py 'd["token"]')
[ -n "$TOKEN" ] || fail "no token"
AUTH="Authorization: Bearer $TOKEN"

echo "== nodes online (expect 2) =="
N=$(curl -s -H "$AUTH" "$B/api/v1/nodes" | jq_py 'len(d["nodes"])')
[ "$N" = "2" ] || fail "expected 2 nodes, got $N"

echo "== dispatch shell.run broadcast =="
OUT=$(curl -s -H "$AUTH" -X POST "$B/api/v1/dispatch" \
  -d '{"select":{"all":true},"call":{"method":"shell.run","params":{"cmd":"echo SYS0_OK"}}}')
CNT=$(echo "$OUT" | jq_py 'sum(1 for i in d["items"] if i["ok"] and i["value"]["stdout"].strip()=="SYS0_OK")')
[ "$CNT" = "2" ] || fail "shell.run ok count = $CNT (out: $OUT)"

echo "== host.info =="
NID=$(curl -s -H "$AUTH" "$B/api/v1/nodes" | jq_py 'd["nodes"][0]["id"]')
OS=$(curl -s -H "$AUTH" -X POST "$B/api/v1/dispatch" \
  -d "{\"select\":{\"nodes\":[\"$NID\"]},\"call\":{\"method\":\"host.info\"}}" | jq_py 'd["items"][0]["value"]["os"]')
[ "$OS" = "linux" ] || fail "host.info os = $OS"

echo "== fs.put + fs.get round-trip =="
DATA=$(printf 'roundtrip-ok' | base64)
curl -s -H "$AUTH" -X POST "$B/api/v1/dispatch" \
  -d "{\"select\":{\"nodes\":[\"$NID\"]},\"call\":{\"method\":\"fs.put\",\"params\":{\"path\":\"/tmp/sys0_e2e_file\",\"data\":\"$DATA\"}}}" >/dev/null
GOT=$(curl -s -H "$AUTH" -X POST "$B/api/v1/dispatch" \
  -d "{\"select\":{\"nodes\":[\"$NID\"]},\"call\":{\"method\":\"fs.get\",\"params\":{\"path\":\"/tmp/sys0_e2e_file\"}}}" \
  | python3 -c 'import sys,json,base64;print(base64.b64decode(json.load(sys.stdin)["items"][0]["value"]["data"]).decode())')
[ "$GOT" = "roundtrip-ok" ] || fail "fs round-trip = $GOT"

echo "== API key: dangerous method gating =="
KEYJSON=$(curl -s -H "$AUTH" -X POST "$B/api/v1/keys" -d '{"name":"e2e-bot","role":"operator","allowDangerous":false}')
SK=$(echo "$KEYJSON" | jq_py 'd["key"]')
BLOCKED=$(curl -s -H "Authorization: Bearer $SK" -X POST "$B/api/v1/dispatch" \
  -d '{"select":{"all":true},"call":{"method":"shell.run","params":{"cmd":"id"}}}' | jq_py 'd.get("code")')
[ "$BLOCKED" = "4030" ] || fail "expected 4030 for dangerous method, got $BLOCKED"
SAFE=$(curl -s -H "Authorization: Bearer $SK" -X POST "$B/api/v1/dispatch" \
  -d '{"select":{"all":true},"call":{"method":"host.metrics"}}' | jq_py 'd["ok"]')
[ "$SAFE" = "True" ] || fail "safe method via key should work"

echo "== MCP tools/list + tools/call =="
TOOLS=$(curl -s -H "Authorization: Bearer $SK" -X POST "$B/mcp" \
  -d '{"jsonrpc":"2.0","id":"1","method":"tools/list"}' | jq_py 'len(d["result"]["tools"])')
[ "$TOOLS" -ge 14 ] || fail "mcp tools = $TOOLS"
MCPNODES=$(curl -s -H "Authorization: Bearer $SK" -X POST "$B/mcp" \
  -d '{"jsonrpc":"2.0","id":"2","method":"tools/call","params":{"name":"sys0_list_nodes","arguments":{}}}' \
  | python3 -c 'import sys,json;print(len(json.loads(json.load(sys.stdin)["result"]["content"][0]["text"])))')
[ "$MCPNODES" = "2" ] || fail "mcp list_nodes = $MCPNODES"

echo "== host.watch -> samples stored =="
curl -s -H "$AUTH" -X POST "$B/api/v1/dispatch" \
  -d "{\"select\":{\"nodes\":[\"$NID\"]},\"call\":{\"method\":\"host.watch\",\"params\":{\"enable\":true,\"interval\":1}}}" >/dev/null
sleep 3
SAMP=$(curl -s -H "$AUTH" "$B/api/v1/metrics?node=$NID" | jq_py 'len(d["samples"])')
[ "$SAMP" -ge 2 ] || fail "expected >=2 samples, got $SAMP"

echo "== console served =="
HTTP_CODE=$(curl -s -o /dev/null -w '%{http_code}' "$B/")
[ "$HTTP_CODE" = "200" ] || fail "console root = $HTTP_CODE"

echo "== managed task: start + list + signal =="
TASK=$(curl -s -H "$AUTH" -X POST "$B/api/v1/dispatch" \
  -d "{\"select\":{\"nodes\":[\"$NID\"]},\"call\":{\"method\":\"task.start\",\"params\":{\"name\":\"e2e-task\",\"cmd\":\"sleep 30\"}}}" \
  | jq_py 'd["items"][0]["value"]["task"]')
[ -n "$TASK" ] || fail "task.start returned no id"
RUNNING=$(curl -s -H "$AUTH" -X POST "$B/api/v1/dispatch" \
  -d "{\"select\":{\"nodes\":[\"$NID\"]},\"call\":{\"method\":\"task.list\"}}" \
  | jq_py 'sum(1 for t in d["items"][0]["value"]["tasks"] if t["state"]=="running")')
[ "$RUNNING" -ge 1 ] || fail "task not running ($RUNNING)"
curl -s -H "$AUTH" -X POST "$B/api/v1/dispatch" \
  -d "{\"select\":{\"nodes\":[\"$NID\"]},\"call\":{\"method\":\"task.signal\",\"params\":{\"task\":\"$TASK\",\"sig\":\"KILL\"}}}" >/dev/null

echo "== node.shutdown -> offline =="
curl -s -H "$AUTH" -X POST "$B/api/v1/dispatch" \
  -d "{\"select\":{\"nodes\":[\"$NID\"]},\"call\":{\"method\":\"node.shutdown\"}}" >/dev/null
sleep 1
ONLINE=$(curl -s -H "$AUTH" "$B/api/v1/nodes" | jq_py 'sum(1 for n in d["nodes"] if n["state"]=="online")')
[ "$ONLINE" = "1" ] || fail "expected 1 online node after shutdown, got $ONLINE"

echo ""
echo "ALL E2E CHECKS PASSED ✓"
