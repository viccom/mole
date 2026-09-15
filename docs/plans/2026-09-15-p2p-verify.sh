#!/usr/bin/env bash
# P2P 两层重构 —— 联合验收测试骨架
#
# 用法：bash verify.sh <server_bin> <p2p_client_bin>
#   server_bin      服务端二进制（本重构后的版本）
#   p2p_client_bin  客户端二进制（-tags p2p 构建，本重构后的版本）
#
# ⚠️ 二进制不要放在本脚本同目录：脚本开头会 rm -rf 工作目录 $WORK
#
# 验证目标（对应 §0.4 运行时语义）：
#   1. 两端各一条同 room 的 p2p 隧道（B 侧仅连接参数，无 mappings）
#   2. A 侧配 2 组 mappings
#   3. 两条映射都能端到端打通（本端监听端口 → 对端 target）
#   4. 对端无需任何映射配置
#
# 依赖：curl python3；端口：29980/29981/29983、21883、23478、15990/15991、19001/19002
set -u

SERV_BIN="${1:?用法: verify.sh <server_bin> <p2p_client_bin>}"
CLI_BIN="${2:?用法: verify.sh <server_bin> <p2p_client_bin>}"

WORK=/tmp/p2p-verify/run          # 子目录：脚本会清空它，勿放二进制
API=http://127.0.0.1:29983
ROOM="verifyRoom$(date +%s | tail -c 6)"
PASS=0; FAIL=0
ok()  { printf '  ✓ %s\n' "$1"; PASS=$((PASS+1)); }
bad() { printf '  ✗ %s\n' "$1"; FAIL=$((FAIL+1)); }
cleanup() {
  for f in "$WORK"/*.pid; do [ -f "$f" ] && kill "$(cat "$f")" 2>/dev/null; done
  rm -f "$WORK"/*.pid
}
trap cleanup EXIT

rm -rf "$WORK"; mkdir -p "$WORK/data"
echo "room=$ROOM"

# ── 0. 服务端 ───────────────────────────────────────────────
cat > "$WORK/config.yaml" <<EOF
server:
  control_port: ":29981"
  gateway_port: ":29980"
  api_port: ":29983"
  transport: "tcp"
auth:
  jwt_secret: "p2p-verify-secret-0123456789"
database:
  path: "$WORK/data/config.db"
logging:
  level: "info"
  console: {enabled: true, color: false}
  file: {enabled: false}
mqtt: {enabled: true, tcp_port: ":21883", ws_port: ":21882"}
stun: {enabled: true, bind_addr: ":23478"}
EOF
"$SERV_BIN" -config "$WORK/config.yaml" > "$WORK/serv.log" 2>&1 &
echo $! > "$WORK/serv.pid"
for i in $(seq 30); do
  curl -sf -m 2 "$API/api/v1/health" >/dev/null 2>&1 && break; sleep 0.5
done
if curl -sf -m 2 "$API/api/v1/health" >/dev/null 2>&1; then ok "服务端启动"
else bad "服务端启动失败"; tail -20 "$WORK/serv.log"; exit 1; fi

TOKEN=$(curl -s -X POST "$API/api/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["token"])' 2>/dev/null)
if [ -n "${TOKEN:-}" ]; then ok "管理员登录"; else bad "登录失败"; exit 1; fi

# ── 1. 两端客户端 ───────────────────────────────────────────
for n in p2pnodeA p2pnodeB p2pnodeC; do
  curl -s -X POST "$API/api/v1/nodes" -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/json' -d "{\"name\":\"$n\"}" >/dev/null
done

cat > "$WORK/cliA.json" <<EOF
{ "server_addr": "127.0.0.1:29981", "token": "default-node-token-change-me",
  "node_id": "p2pnodeA", "tunnels": [], "http_port": "127.0.0.1:15990" }
EOF
cat > "$WORK/cliB.json" <<EOF
{ "server_addr": "127.0.0.1:29981", "token": "default-node-token-change-me",
  "node_id": "p2pnodeB", "tunnels": [], "http_port": "127.0.0.1:15991" }
EOF
"$CLI_BIN" -config "$WORK/cliA.json" > "$WORK/cliA.log" 2>&1 & echo $! > "$WORK/cliA.pid"
"$CLI_BIN" -config "$WORK/cliB.json" > "$WORK/cliB.log" 2>&1 & echo $! > "$WORK/cliB.pid"
sleep 4
ONLINE=$(curl -s "$API/api/v1/nodes" -H "Authorization: Bearer $TOKEN" \
  | python3 -c 'import sys,json;print(sum(1 for n in json.load(sys.stdin)["data"]["items"] if n["status"]=="online"))' 2>/dev/null)
[ "$ONLINE" = "2" ] && ok "两端节点均在线（A/B）" || bad "在线节点数=$ONLINE（期望 2，p2pnodeC 仅建记录不启动）"

# ── 2. 对端 B 侧的后端目标 ──────────────────────────────────
python3 -c "
import http.server, socketserver, threading, time
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(s):
        s.send_response(200); s.send_header('Content-Type','text/plain'); s.end_headers()
        s.wfile.write(b'TARGET-%d' % s.server.server_address[1])
    def log_message(*a): pass
for port in (19001, 19002):
    socketserver.TCPServer.allow_reuse_address = True
    t = socketserver.TCPServer(('127.0.0.1', port), H)
    threading.Thread(target=t.serve_forever, daemon=True).start()
time.sleep(900)
" > "$WORK/backend.log" 2>&1 & echo $! > "$WORK/backend.pid"
sleep 1

# ── 3. 建两条同 room 的 p2p 隧道（A 带 2 组映射，B 无映射）──
mk() { # mk <name> <node> <json-para>
  curl -s -X POST "$API/api/v1/tunnels" -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"$1\",\"type\":\"p2p\",\"enabled\":true,\"node_id\":\"$2\",\"para\":$3}"
}
R1=$(mk p2p-a p2pnodeA "{\"room\":\"$ROOM\",\"mappings\":[{\"protocol\":\"tcp\",\"local_port\":18401,\"target_host\":\"127.0.0.1\",\"target_port\":19001},{\"protocol\":\"tcp\",\"local_port\":18402,\"target_host\":\"127.0.0.1\",\"target_port\":19002}]}")
echo "$R1" | grep -q '"code":0' && ok "A 侧隧道创建（2 组映射）" || bad "A 侧创建失败: $R1"
R2=$(mk p2p-b p2pnodeB "{\"room\":\"$ROOM\"}")
echo "$R2" | grep -q '"code":0' && ok "B 侧隧道创建（仅连接参数，无映射）" || bad "B 侧创建失败: $R2"

R3=$(mk p2p-c p2pnodeC "{\"room\":\"$ROOM\"}")
echo "$R3" | grep -q '"code":400' && ok "第三端同 room 被拒（配对不变量）" || bad "第三端未被拒: $R3"

# ── 4. 端到端验证两条映射 ──────────────────────────────────
for p in 18401 18402; do
  [ "$p" = "18401" ] && WANT="TARGET-19001" || WANT="TARGET-19002"
  OUT=""
  for i in $(seq 30); do
    OUT=$(curl -s -m 3 "http://127.0.0.1:$p/" 2>/dev/null)
    [ "$OUT" = "$WANT" ] && break
    sleep 2
  done
  [ "$OUT" = "$WANT" ] && ok "映射 local_port=$p → 对端 $WANT" \
                       || bad "映射 local_port=$p 不通（得到 '$OUT'，期望 '$WANT'）"
done

echo
echo "════ 通过 $PASS / 失败 $FAIL ════"
echo "日志：$WORK/{serv,cliA,cliB}.log"
[ "$FAIL" = "0" ] || exit 1
