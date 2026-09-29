#!/bin/bash
# 安全修复 R1 端到端矩阵（12 项）
# 用法: ./e2e_run.sh <起始场景号> <结束场景号>
set -u
FROM=${1:-1}; TO=${2:-12}
BASE=~/e2e
BIN=$BASE/bin
CLI_OLD=${CLI_OLD:-$BIN/cli-old}
API=http://127.0.0.1:9983
ADMIN_USER=admin
ADMIN_PASS='E2eAdminPass1!'
NODE_TOKEN='e2e-legacy-tok-4f7a1c'
PASS=0; FAIL=0; RESULTS=()

api()   { curl --noproxy '*' -s -m 10 "$@"; }
api_code() { curl --noproxy '*' -s -m 10 -o /dev/null -w '%{http_code}' "$@"; }
rec() { local name=$1 ok=$2 detail=$3
  if [ "$ok" = 0 ]; then PASS=$((PASS+1)); RESULTS+=("PASS | $name | $detail"); echo "    [PASS] $name — $detail"
  else FAIL=$((FAIL+1)); RESULTS+=("FAIL | $name | $detail"); echo "    [FAIL] $name — $detail"; fi
}
jwt_field() { python3 -c "
import sys,json,base64
p=sys.argv[2].split('.')[1]; p+='='*(-len(p)%4)
d=json.loads(base64.urlsafe_b64decode(p)); print(d.get(sys.argv[1],''))" "$1" "$2"; }

login() { api -X POST "$API/api/v1/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$1\",\"password\":\"$2\"}" | jq -r '.data.token // empty'; }
mk_token() { api -X POST "$API/api/v1/me/access-tokens" -H "Authorization: Bearer $1" \
  -H 'Content-Type: application/json' -d '{"name":"e2e"}' | jq -r '.data.token // empty'; }
mk_user() { api -X POST "$API/api/v1/users" -H "Authorization: Bearer $1" \
  -H 'Content-Type: application/json' -d "{\"username\":\"$2\",\"password\":\"$3\"}" ; }
node_json() { api -H "Authorization: Bearer $1" "$API/api/v1/nodes" | jq -c --arg id "$2" '[.data.items[]? | select(.id==$id)][0]'; }
wait_online() { # jwt nodeid tries
  local i; for i in $(seq 1 "${3:-24}"); do sleep 0.5
    [ "$(node_json "$1" "$2" | jq -r '.status // empty')" = "online" ] && return 0
  done; return 1; }

proc_alive() { [ -d "/proc/$1" ]; }
ports_busy() { ss -tln 2>/dev/null | grep -qE ':(9980|9981|9982|9983|1882|1883) '; }
wait_ports() { local i; for i in $(seq 1 30); do ports_busy || return 0; sleep 0.5; done; return 1; }
kill_wait() { # pid —— TERM 后最长等 10s，仍活则 KILL
  local pid=$1 i
  kill "$pid" 2>/dev/null
  for i in $(seq 1 20); do proc_alive "$pid" || return 0; sleep 0.5; done
  kill -9 "$pid" 2>/dev/null; sleep 1
  proc_alive "$pid" && return 1 || return 0
}
srv_start() { # bin wd [ENV=V ...]
  local bin=$1 wd=$2; shift 2; mkdir -p "$wd"
  if ! wait_ports; then echo "    !! 端口仍被占用，拒绝启动（防止误连残留服务）"; return 1; fi
  ( cd "$wd" || exit 1
    env "$@" nohup "$bin" >server.log 2>&1 &
    echo $! >server.pid )
  local i; for i in $(seq 1 40); do sleep 0.5
    if api "$API/api/v1/health" | grep -q '"code":0'; then
      proc_alive "$(cat "$wd/server.pid")" && return 0
      echo "    !! 健康通过但本进程已死（疑似连到残留服务）"; return 1
    fi; done
  echo "    !! server 未就绪: $wd/server.log 尾部:"; tail -5 "$wd/server.log" 2>/dev/null; return 1
}
srv_stop() { local wd=$1
  [ -f "$wd/server.pid" ] || return 0
  kill_wait "$(cat "$wd/server.pid")"
  wait_ports
}
srv_run() { # bin wd timeout_s [ENV=V ...] —— 前台跑（用于拒启断言）
  local bin=$1 wd=$2 tmo=$3; shift 3; mkdir -p "$wd"
  ( cd "$wd" && env "$@" timeout "$tmo" "$bin" >server.log 2>&1; echo $? >server.exit )
}
cli_start() { # bin id token port wd
  local bin=$1 id=$2 token=$3 port=$4 wd=$5; mkdir -p "$wd"
  ( cd "$wd" || exit 1
    HOME="$wd" nohup "$bin" -server 127.0.0.1:9981 -token "$token" -id "$id" \
      -name "e2e-$id" -http "127.0.0.1:$port" >client.log 2>&1 &
    echo $! >client.pid )
}
cli_stop() { [ -f "$1/client.pid" ] && kill_wait "$(cat "$1/client.pid")"; return 0; }
tunnel_echo() { python3 - <<'PYEOF'
import socket
try:
    s=socket.create_connection(("127.0.0.1",19100),timeout=8)
    s.sendall(b"e2e-echo-payload")
    d=s.recv(64); s.close()
    print("OK" if d==b"e2e-echo-payload" else "BAD:"+repr(d))
except Exception as e:
    print("ERR:"+str(e))
PYEOF
}
setup_tunnel() { # jwt nodeid
  api -X POST "$API/api/v1/tunnels" -H "Authorization: Bearer $1" -H 'Content-Type: application/json' \
    -d "{\"name\":\"e2e-echo\",\"type\":\"tcp\",\"target\":\"127.0.0.1:15999\",\"listen_port\":19100,\"node_id\":\"$2\"}"
}
STD_ENV=("MA_ADMIN_USER=$ADMIN_USER" "MA_ADMIN_PASS=$ADMIN_PASS" "MA_NODE_TOKEN=$NODE_TOKEN" "PATH=/usr/bin:/bin")

run1to6() { # 通用组合场景: 序号 描述 server_bin client_bin r2mode(0/1)
  local n=$1 desc=$2 sbin=$3 cbin=$4 r2=$5
  local wd=$BASE/run/sc$n; rm -rf "$wd"; mkdir -p "$wd"
  [ "$r2" = 1 ] && printf 'node_auth:\n  legacy_format_enabled: false\n' > "$wd/config.yaml"
  echo "  场景$n: $desc"
  if ! srv_start "$sbin" "$wd" "${STD_ENV[@]}"; then rec "sc$n-server-up" 1 "服务端未就绪"; return; fi
  local jwt raw
  jwt=$(login "$ADMIN_USER" "$ADMIN_PASS"); [ -z "$jwt" ] && { rec "sc$n" 1 "admin登录失败"; return; }
  raw=$(mk_token "$jwt"); [ -z "$raw" ] && { rec "sc$n" 1 "创建access token失败"; return; }
  cli_start "$cbin" "sc${n}node1" "$raw" "159$((10+n))" "$wd/cli"
  if [ "$n" = 3 ] || [ "$n" = 5 ]; then
    sleep 8
    local online; online=$(node_json "$jwt" "sc${n}node1" | jq -r '.status // empty')
    if [ -z "$online" ]; then
      local mark; mark=$( { grep -c "Legacy node auth rejected" "$wd/server.log" 2>/dev/null || true; } )
      if [ "$n" = 3 ] && [ "${mark:-0}" -ge 1 ]; then rec "sc$n" 0 "旧客户端被 R2 拒绝（日志含 Legacy node auth rejected）"
      elif [ "$n" = 5 ]; then
        local m5; m5=$(grep -c "Node auth failed" "$wd/server.log" 2>/dev/null || true)
        if [ "${m5:-0}" -ge 1 ]; then rec "sc$n" 0 "新客户端被旧服务端拒绝（日志含 Node auth failed）"
        else rec "sc$n" 1 "节点未上线但缺少 Node auth failed 日志"; fi
      else rec "sc$n" 1 "节点未上线但缺少预期拒绝日志"; fi
    else rec "sc$n" 1 "节点不应上线却 online"; fi
  else
    if ! wait_online "$jwt" "sc${n}node1"; then rec "sc$n" 1 "节点未上线"; cli_stop "$wd/cli"; srv_stop "$wd"; return; fi
    local ver; ver=$(node_json "$jwt" "sc${n}node1" | jq -r '.sysinfo.agent_version // "unknown"')
    setup_tunnel "$jwt" "sc${n}node1" >/dev/null; sleep 2
    local echo; echo=$(tunnel_echo)
    [ "$echo" = OK ] && rec "sc$n" 0 "认证/注册/隧道回环全通 (client=$ver)" || rec "sc$n" 1 "隧道回声失败: $echo"
  fi
  cli_stop "$wd/cli"; srv_stop "$wd"
}

sc7() {
  local wd=$BASE/run/sc7; rm -rf "$wd"; mkdir -p "$wd"
  echo "  场景7: SEC-02 跨租户劫持拒绝 + 同主重连"
  srv_start "$BIN/serv-r1" "$wd" "${STD_ENV[@]}" || { rec sc7 1 "server 未就绪"; return; }
  local ajwt=$(login "$ADMIN_USER" "$ADMIN_PASS")
  mk_user "$ajwt" u7a 'E2eUser7aPass1!' >/dev/null; mk_user "$ajwt" u7b 'E2eUser7bPass1!' >/dev/null
  local jwta=$(login u7a 'E2eUser7aPass1!') jwtb=$(login u7b 'E2eUser7bPass1!')
  local ua=$(jwt_field user_id "$jwta") ub=$(jwt_field user_id "$jwtb")
  local ta=$(mk_token "$jwta") tb=$(mk_token "$jwtb")
  cli_start "$BIN/cli-new" hjack001 "$ta" 15977 "$wd/ca"
  wait_online "$ajwt" hjack001 || { rec sc7 1 "A 节点未上线"; cli_stop "$wd/ca"; srv_stop "$wd"; return; }
  local owner1=$(node_json "$ajwt" hjack001 | jq -r '.owner_user_id')
  cli_stop "$wd/ca"; sleep 2
  cli_start "$BIN/cli-new" hjack001 "$tb" 15978 "$wd/cb"; sleep 8
  local online_b=$(node_json "$ajwt" hjack001 | jq -r '.status // empty')
  local rej=$(grep -c "registered by another user" "$wd/server.log" 2>/dev/null || echo 0)
  local owner2=$(node_json "$ajwt" hjack001 | jq -r '.owner_user_id // empty')
  cli_stop "$wd/cb"
  local ok1=0; [ "$owner1" = "$ua" ] || ok1=1
  rec "sc7-a-owner" $ok1 "初始归属=$owner1 (期望 $ua)"
  local ok2=0; [ -z "$online_b" ] && [ "${rej:-0}" -ge 1 ] && [ "$owner2" != "$ub" ] || ok2=1
  rec "sc7-b-hijack" $ok2 "B 劫持被拒(rej日志=$rej, online=$online_b, owner2=$owner2)"
  cli_start "$BIN/cli-new" hjack001 "$ta" 15979 "$wd/ca2"
  local ok3=0; wait_online "$ajwt" hjack001 20 || ok3=1
  rec "sc7-c-reconnect" $ok3 "A 同主重连恢复在线"
  cli_stop "$wd/ca2"; srv_stop "$wd"
}

sc8() {
  local wd=$BASE/run/sc8; rm -rf "$wd"; mkdir -p "$wd"
  echo "  场景8: SEC-03 节点 API 响应无 token 字段"
  srv_start "$BIN/serv-r1" "$wd" "${STD_ENV[@]}" || { rec sc8 1 "server 未就绪"; return; }
  local jwt raw
  jwt=$(login "$ADMIN_USER" "$ADMIN_PASS"); raw=$(mk_token "$jwt")
  cli_start "$BIN/cli-new" sc8node1 "$raw" 15980 "$wd/cli"
  wait_online "$jwt" sc8node1 || { rec sc8 1 "节点未上线"; cli_stop "$wd/cli"; srv_stop "$wd"; return; }
  local g=$(node_json "$jwt" sc8node1)
  local has=$(echo "$g" | jq 'has("token")')
  local c=$(api -X POST "$API/api/v1/nodes" -H "Authorization: Bearer $jwt" -H 'Content-Type: application/json' \
    -d '{"id":"sc8pre01","name":"e2e-pre"}' | jq '.data | has("token")')
  local ok=0; [ "$has" = false ] && [ "$c" = false ] || ok=1
  rec sc8 $ok "GET 单节点 token 字段=$has, POST 创建响应 token 字段=$c (期望均 false)"
  cli_stop "$wd/cli"; srv_stop "$wd"
}

sc9() {
  local wd=$BASE/run/sc9; rm -rf "$wd"; mkdir -p "$wd"
  echo "  场景9: SEC-04 refresh 角色实时生效（授予与撤销双向）"
  srv_start "$BIN/serv-r1" "$wd" "${STD_ENV[@]}" || { rec sc9 1 "server 未就绪"; return; }
  local ajwt=$(login "$ADMIN_USER" "$ADMIN_PASS")
  local role=$(api -H "Authorization: Bearer $ajwt" "$API/api/v1/roles" | jq -r '.data[0].id // empty')
  mk_user "$ajwt" u9 'E2eUser9Pass1!' >/dev/null
  local j1 uid
  j1=$(login u9 'E2eUser9Pass1!'); uid=$(jwt_field user_id "$j1")
  local r0=$(jwt_field roles "$j1" 2>/dev/null || echo ERR)
  api -X POST "$API/api/v1/users/$uid/roles/$role" -H "Authorization: Bearer $ajwt" >/dev/null
  local j2=$(api -X POST "$API/api/v1/auth/refresh" -H "Authorization: Bearer $j1" | jq -r '.data.token // empty')
  local r2=$(jwt_field roles "$j2" 2>/dev/null || echo ERR)
  api -X DELETE "$API/api/v1/users/$uid/roles/$role" -H "Authorization: Bearer $ajwt" >/dev/null
  local j3=$(api -X POST "$API/api/v1/auth/refresh" -H "Authorization: Bearer $j2" | jq -r '.data.token // empty')
  local r3=$(jwt_field roles "$j3" 2>/dev/null || echo ERR)
  local ok=0
  echo "$r2" | grep -q "'$role'" && ! (echo "$r3" | grep -q "'$role'") || ok=1
  rec sc9 $ok "角色轨迹: 初始=$r0 → 授予后refresh=$r2 → 撤销后refresh=$r3"
  srv_stop "$wd"
}

sc10() {
  local wd=$BASE/run/sc10; rm -rf "$wd"; mkdir -p "$wd"
  echo "  场景10: SEC-06 AccessKey 被 strict 路由拒绝"
  srv_start "$BIN/serv-r1" "$wd" "${STD_ENV[@]}" || { rec sc10 1 "server 未就绪"; return; }
  local ajwt=$(login "$ADMIN_USER" "$ADMIN_PASS")
  api -X PUT "$API/api/v1/accesskey" -H "Authorization: Bearer $ajwt" -H 'Content-Type: application/json' \
    -d '{"key":"e2e-ak-test-12345"}' >/dev/null
  AK='-H'
  local c1=$(api_code -H "W-Access-Key: e2e-ak-test-12345" "$API/api/v1/accesskey")
  local c2=$(api_code -X POST -H "W-Access-Key: e2e-ak-test-12345" "$API/api/v1/self-update")
  local c3=$(api_code -X DELETE -H "W-Access-Key: e2e-ak-test-12345" "$API/api/v1/users/ghostuser")
  local c4=$(api_code -H "W-Access-Key: e2e-ak-test-12345" "$API/api/v1/nodes")
  api -X DELETE "$API/api/v1/accesskey" -H "Authorization: Bearer $ajwt" >/dev/null
  local ok=0; [ "$c1" = 403 ] && [ "$c2" = 403 ] && [ "$c3" = 403 ] && [ "$c4" = 200 ] || ok=1
  rec sc10 $ok "accesskey GET=$c1 self-update=$c2 删用户=$c3 (期望403) 普通路由=$c4 (期望200)"
  srv_stop "$wd"
}

sc11() {
  local wd=$BASE/run/sc11; rm -rf "$wd"; mkdir -p "$wd"
  echo "  场景11: SEC-07 登录限速锁定"
  srv_start "$BIN/serv-r1" "$wd" "${STD_ENV[@]}" || { rec sc11 1 "server 未就绪"; return; }
  local ajwt=$(login "$ADMIN_USER" "$ADMIN_PASS")
  mk_user "$ajwt" u11 'E2eUser11Pas1!' >/dev/null
  local i codes=""
  for i in $(seq 1 10); do
    codes="$codes$(api_code -X POST "$API/api/v1/auth/login" -H 'Content-Type: application/json' -d '{"username":"u11","password":"WRONG"}')"
  done
  local locked=$(api_code -X POST "$API/api/v1/auth/login" -H 'Content-Type: application/json' \
    -d '{"username":"u11","password":"E2eUser11Pas1!"}')
  local still=$(api_code -X POST "$API/api/v1/auth/login" -H 'Content-Type: application/json' \
    -d '{"username":"u11","password":"WRONG"}')
  local ok=0
  [ "${#codes}" = 30 ] && [ "$locked" = 429 ] && [ "$still" = 429 ] || ok=1
  rec sc11 $ok "10次错密=$codes 第11次正确密码=$locked (期望429) 第12次错密=$still (期望429)"
  srv_stop "$wd"
}

sc12() {
  echo "  场景12: SEC-05 默认凭据安全化"
  local wd=$BASE/run/sc12a; rm -rf "$wd"; mkdir -p "$wd"
  srv_run "$BIN/serv-r1" "$wd" 15 "MA_ADMIN_USER=$ADMIN_USER" "MA_ADMIN_PASS=$ADMIN_PASS" "PATH=/usr/bin:/bin"
  local ec=$(cat "$wd/server.exit" 2>/dev/null || echo timeout)
  local mark=$(grep -ci "nodetoken\|node.*token" "$wd/server.log" 2>/dev/null || echo 0)
  local ok=0; [ "$ec" != 0 ] && [ "$ec" != timeout ] && [ "${mark:-0}" -ge 1 ] || ok=1
  rec "sc12a-refuse" $ok "无节点token退出码=$ec (期望非0非124) 日志含指引=$mark"
  local wd2=$BASE/run/sc12b; rm -rf "$wd2"; mkdir -p "$wd2"
  srv_run "$BIN/serv-r1" "$wd2" 15 "MA_ADMIN_USER=$ADMIN_USER" "MA_NODE_TOKEN=$NODE_TOKEN" "PATH=/usr/bin:/bin"
  local pw=$(grep -oP 'randomly generated password for this run: \K[A-Za-z0-9]+' "$wd2/server.log" 2>/dev/null | head -1)
  if [ -z "$pw" ]; then rec "sc12b-rotate" 1 "未找到随机口令打印"; return; fi
  ( cd "$wd2" || exit 1
    env "MA_ADMIN_USER=$ADMIN_USER" "MA_NODE_TOKEN=$NODE_TOKEN" "PATH=/usr/bin:/bin" \
      nohup "$BIN/serv-r1" >server2.log 2>&1 &
    echo $! >server.pid )
  local i; for i in $(seq 1 40); do sleep 0.5; api "$API/api/v1/health" | grep -q '"code":0' && break; done
  local c_ok=$(api_code -X POST "$API/api/v1/auth/login" -H 'Content-Type: application/json' \
    -d "{\"username\":\"admin\",\"password\":\"$pw\"}")
  local c_bad=$(api_code -X POST "$API/api/v1/auth/login" -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"admin"}')
  local ok2=0; [ "$c_ok" = 200 ] && [ "$c_bad" = 401 ] || ok2=1
  rec "sc12b-rotate" $ok2 "随机口令登录=$c_ok (期望200) admin/admin=$c_bad (期望401) 口令=${pw:0:4}****"
  srv_stop "$wd2"
}

cleanup() {
  local d; for d in "$BASE"/run/*/; do
    [ -f "$d/client.pid" ] && kill "$(cat "$d/client.pid")" 2>/dev/null
    [ -f "$d/server.pid" ] && kill "$(cat "$d/server.pid")" 2>/dev/null
  done
  [ -f "$BASE/run/sc17/blocker.pid" ] && kill "$(cat "$BASE/run/sc17/blocker.pid")" 2>/dev/null
}
trap cleanup EXIT

sc17() {
  local wd=$BASE/run/sc17; rm -rf "$wd"; mkdir -p "$wd"
  echo "  场景17: 额外 listener（ws_port）被占时服务端快速报错退出（F1 回归守卫）"
  python3 -c 'import socket,time
s=socket.socket(); s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
s.bind(("0.0.0.0",9989)); s.listen(1); time.sleep(60)' &
  echo $! > "$wd/blocker.pid"
  sleep 1
  printf 'server:\n  ws_port: ":9989"\n' > "$wd/config.yaml"
  ( cd "$wd" || exit 1
    env "MA_ADMIN_USER=$ADMIN_USER" "MA_ADMIN_PASS=$ADMIN_PASS" "MA_NODE_TOKEN=$NODE_TOKEN" \
      PATH=/usr/bin:/bin timeout 20 "$BIN/serv-r1" -config config.yaml >server.log 2>&1
    echo $? >server.exit )
  local ec=$(cat "$wd/server.exit" 2>/dev/null || echo timeout)
  local listen_err=$(grep -c "control listen" "$wd/server.log" 2>/dev/null || true)
  local storm=$(grep -c "Accept connection failed" "$wd/server.log" 2>/dev/null || true)
  local ok=0
  [ "$ec" != 0 ] && [ "$ec" != 124 ] && [ "${listen_err:-0}" -ge 1 ] && [ "${storm:-0}" -lt 3 ] || ok=1
  rec sc17 $ok "退出码=$ec(期望非0非124) listen错误=$listen_err(≥1) Accept风暴=$storm(<3)"
  kill "$(cat "$wd/blocker.pid")" 2>/dev/null
}

sc15() {
  local wd=$BASE/run/sc15; rm -rf "$wd"; mkdir -p "$wd"
  local srvbin=${SC15_SRV:-$BIN/serv-r1}
  echo "  场景15: 旧客户端携带自有合法隧道注册（server=$srvbin）"
  srv_start "$srvbin" "$wd" "${STD_ENV[@]}" || { rec sc15 1 "server 未就绪"; return; }
  local jwt=$(login "$ADMIN_USER" "$ADMIN_PASS") raw
  raw=$(mk_token "$jwt")
  mkdir -p "$wd/cli"
  cat > "$wd/cli/client-config.json" << 'CFGEOF'
{
  "server": "127.0.0.1:9981",
  "transport": "tcp",
  "tunnels": [
    {"name": "cli-tcp-a", "type": "tcp", "target": "127.0.0.1:15999", "listen_port": 19102, "enabled": true}
  ]
}
CFGEOF
  ( cd "$wd/cli" || exit 1
    HOME="$PWD" nohup "$CLI_OLD" -config client-config.json -token "$raw" \
      -id sc15nod1 -name e2e-sc15 -http "127.0.0.1:15990" >client.log 2>&1 &
    echo $! >client.pid )
  if ! wait_online "$jwt" sc15nod1; then
    rec sc15 1 "节点未上线（携带隧道注册被拒？查 $wd/cli/client.log）"
    kill "$(cat "$wd/cli/client.pid")" 2>/dev/null; srv_stop "$wd"; return
  fi
  local tc=$(node_json "$jwt" sc15nod1 | jq -r '.tunnel_count // 0')
  # 产品语义（新旧服务端 A/B 一致）：注册携带的隧道被记录可见但不立即起监听，
  # 兼容性断言以「注册被接受 + 隧道完整入册」为准
  local tp=$(node_json "$jwt" sc15nod1 | jq -r '[.tunnels[]? | select(.name=="cli-tcp-a")][0].listen_port // empty')
  local ok=0
  [ "$tc" = 1 ] && [ "$tp" = 19102 ] || ok=1
  rec sc15 $ok "旧客户端携隧道注册被接受: tunnel_count=$tc cli-tcp-a端口=$tp (期望 1/19102)"
  kill "$(cat "$wd/cli/client.pid")" 2>/dev/null; srv_stop "$wd"
}

sc16() {
  local wd=$BASE/run/sc16; rm -rf "$wd"; mkdir -p "$wd"
  echo "  场景16: 旧客户端携带不合规隧道（保留端口）被拒——有意的兼容性边界"
  srv_start "$BIN/serv-r1" "$wd" "${STD_ENV[@]}" || { rec sc16 1 "server 未就绪"; return; }
  local jwt=$(login "$ADMIN_USER" "$ADMIN_PASS") raw
  raw=$(mk_token "$jwt")
  mkdir -p "$wd/cli"
  cat > "$wd/cli/client-config.json" << 'CFGEOF'
{
  "server": "127.0.0.1:9981",
  "transport": "tcp",
  "tunnels": [
    {"name": "cli-bad", "type": "tcp", "target": "127.0.0.1:15999", "listen_port": 9983, "enabled": true}
  ]
}
CFGEOF
  ( cd "$wd/cli" || exit 1
    HOME="$PWD" nohup "$CLI_OLD" -config client-config.json -token "$raw" \
      -id sc16nod1 -name e2e-sc16 -http "127.0.0.1:15991" >client.log 2>&1 &
    echo $! >client.pid )
  sleep 8
  local online=$(node_json "$jwt" sc16nod1 | jq -r '.status // empty')
  local rej=$(grep -c "Register failed" "$wd/cli/client.log" 2>/dev/null || true)
  local ok=0
  [ -z "$online" ] && [ "${rej:-0}" -ge 1 ] || ok=1
  rec sc16 $ok "不合规隧道注册被拒: online='$online' 客户端Register failed=$rej (期望空/≥1)"
  kill "$(cat "$wd/cli/client.pid")" 2>/dev/null; srv_stop "$wd"
}

sc13() {
  local wd=$BASE/run/sc13; rm -rf "$wd"; mkdir -p "$wd"
  echo "  场景13: SEC-08 密码重置权限（users:write 不可重置他人）"
  srv_start "$BIN/serv-r1" "$wd" "${STD_ENV[@]}" || { rec sc13 1 "server 未就绪"; return; }
  local ajwt=$(login "$ADMIN_USER" "$ADMIN_PASS")
  api -X POST "$API/api/v1/roles" -H "Authorization: Bearer $ajwt" -H 'Content-Type: application/json' \
    -d '{"id":"helpdesk","name":"helpdesk","permissions":[{"resource":"users","action":"write"}]}' >/dev/null
  mk_user "$ajwt" u13 'E2eUser13Pas1!' >/dev/null
  local uid=$(api -H "Authorization: Bearer $ajwt" "$API/api/v1/users" | jq -r '.data.items[]? | select(.username=="u13") | .id // empty')
  [ -z "$uid" ] && { local j13=$(login u13 'E2eUser13Pas1!'); uid=$(jwt_field user_id "$j13"); }
  api -X POST "$API/api/v1/users/$uid/roles/helpdesk" -H "Authorization: Bearer $ajwt" >/dev/null
  local jh=$(login u13 'E2eUser13Pas1!')
  local c1=$(api_code -X PUT "$API/api/v1/users/admin/password" -H "Authorization: Bearer $jh" \
    -H 'Content-Type: application/json' -d '{"password":"Hijacked123!"}')
  local c2=$(api_code -X PUT "$API/api/v1/users/$uid/password" -H "Authorization: Bearer $ajwt" \
    -H 'Content-Type: application/json' -d '{"password":"E2eUser13New1!"}')
  local c3=$(api_code -X POST "$API/api/v1/auth/login" -H 'Content-Type: application/json' \
    -d '{"username":"u13","password":"E2eUser13New1!"}')
  local c4=$(api_code -X POST "$API/api/v1/auth/login" -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"Hijacked123!"}')
  local ok=0
  [ "$c1" = 403 ] && [ "$c2" = 200 ] && [ "$c3" = 200 ] && [ "$c4" = 401 ] || ok=1
  rec sc13 $ok "helpdesk重置admin=$c1(期望403) admin重置u13=$c2(期望200) 新密登录=$c3(期望200) admin未被动=$c4(期望401)"
  srv_stop "$wd"
}

sc14() {
  local wd=$BASE/run/sc14; rm -rf "$wd"; mkdir -p "$wd"
  echo "  场景14: REL-01 listen_port 校验与唯一性"
  srv_start "$BIN/serv-r1" "$wd" "${STD_ENV[@]}" || { rec sc14 1 "server 未就绪"; return; }
  local jwt=$(login "$ADMIN_USER" "$ADMIN_PASS") raw
  raw=$(mk_token "$jwt")
  cli_start "$BIN/cli-new" sc14nod1 "$raw" 15981 "$wd/cli"
  wait_online "$jwt" sc14nod1 || { rec sc14 1 "节点未上线"; cli_stop "$wd/cli"; srv_stop "$wd"; return; }
  local c1=$(api_code -X POST "$API/api/v1/tunnels" -H "Authorization: Bearer $jwt" \
    -H 'Content-Type: application/json' \
    -d '{"name":"t-reserved","type":"tcp","target":"127.0.0.1:15999","listen_port":9983,"node_id":"sc14nod1"}')
  local c2=$(api_code -X POST "$API/api/v1/tunnels" -H "Authorization: Bearer $jwt" \
    -H 'Content-Type: application/json' \
    -d '{"name":"t-dup-a","type":"tcp","target":"127.0.0.1:15999","listen_port":19101,"node_id":"sc14nod1"}')
  local c3=$(api_code -X POST "$API/api/v1/tunnels" -H "Authorization: Bearer $jwt" \
    -H 'Content-Type: application/json' \
    -d '{"name":"t-dup-b","type":"tcp","target":"127.0.0.1:15999","listen_port":19101,"node_id":"sc14nod1"}')
  local ok=0
  [ "$c1" = 400 ] && [ "$c2" = 200 ] && [ "$c3" = 400 ] || ok=1
  rec sc14 $ok "保留端口9983=$c1(期望400) 正常端口=$c2(期望200) 重复端口=$c3(期望400)"
  cli_stop "$wd/cli"; srv_stop "$wd"
}

# ---- 主流程 ----
if [ ! -f "$BASE/echo.pid" ] || ! kill -0 "$(cat "$BASE/echo.pid" 2>/dev/null)" 2>/dev/null; then
  nohup python3 "$BASE/echo_server.py" >"$BASE/echo.log" 2>&1 & echo $! >"$BASE/echo.pid"
  sleep 0.5
fi

n=$FROM
while [ "$n" -le "$TO" ]; do
  case $n in
    1) run1to6 1 "R1 + 新客户端（proof 格式）" "$BIN/serv-r1" "$BIN/cli-new" 0;;
    2) run1to6 2 "R1 + 旧客户端（legacy 格式兼容）" "$BIN/serv-r1" "$CLI_OLD" 0;;
    3) run1to6 3 "R2模式 + 旧客户端（应拒绝）" "$BIN/serv-r1" "$CLI_OLD" 1;;
    4) run1to6 4 "R2模式 + 新客户端（应正常）" "$BIN/serv-r1" "$BIN/cli-new" 1;;
    5) run1to6 5 "旧服务端 + 新客户端（应拒绝，记录）" "$BIN/serv-old" "$BIN/cli-new" 0;;
    6) run1to6 6 "旧服务端 + 旧客户端（基线）" "$BIN/serv-old" "$CLI_OLD" 0;;
    7) sc7;; 8) sc8;; 9) sc9;; 10) sc10;; 11) sc11;; 12) sc12;; 13) sc13;; 14) sc14;; 15) sc15;; 16) sc16;; 17) sc17;;
  esac
  n=$((n+1))
done

echo; echo "===== 结果汇总（场景 $FROM-$TO）: PASS=$PASS FAIL=$FAIL ====="
printf '%s\n' "${RESULTS[@]}"
[ "$FAIL" = 0 ]
