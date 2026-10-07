#!/usr/bin/env bash
# capture_verify.sh — tcpdump 级密文验证（方案 B.6 ① 抓包项；WSL 内 root 运行）
#
# 用法: bash capture_verify.sh <serv-bin> <cli-bin> <workdir>
#   例: bash capture_verify.sh ~/e2e/bin/serv-r1 ~/e2e/bin/cli-new /root/e2e/capv
#
# 做两次对照会话（各自独立 wd，顺序执行；控制口 :19981 / API 口 :19983）：
#   A. 明文对照（channel_encryption.enabled=false）
#   B. 加密会话（channel_encryption.enabled=true）
# 服务端配置走 cwd 下 config.yaml（control_port/api_port 为 server 子键），
# 管理引导走 env MA_ADMIN_USER/MA_ADMIN_PASS/MA_NODE_TOKEN，节点 token 经
# REST API login + me/access-tokens 创建（与 e2e_run.sh 助手同一套约定）。
# 各抓 lo 上的控制口包，然后对 pcap 做字节标记断言：
#   明文捕获必须含:  '"cmd":"register"' 与节点 ID（smux 明文可见）
#   加密捕获必须含:  '"enc":{"v":1}' 与 '"proof":'（握手前明文段，属预期）
#   加密捕获必须不含: '"cmd":"register"' / '"cmd":"ping"' / 节点 ID / 'tunnel_push'
#                     （这些只出现在 smux 内；smux 已被 Noise 包裹 → 密文里不可见）
#   加密 server.log 必须 enc=true（剥 ANSI 色码后匹配 Node authenticated 行）
# 任一断言失败 exit 1。
#
# 依赖: tcpdump（WSL: apt-get install -y tcpdump）、curl、jq；客户端 -transport tcp 默认。
set -u

SERV_BIN=${1:?usage: capture_verify.sh <serv-bin> <cli-bin> <workdir>}
CLI_BIN=${2:?usage: capture_verify.sh <serv-bin> <cli-bin> <workdir>}
WD=${3:-/tmp/capv}
NODE_ID=capturd1
CTL_PORT=19981   # 控制口（避开 harness 的 9980-9983）
API_PORT=19983   # REST API 口（health/login/mk_token）
CLI_HTTP=21981   # 客户端本地 http 口（不抓）
API=http://127.0.0.1:$API_PORT
ADMIN_USER=admin
ADMIN_PASS='E2eAdminPass1!'
NODE_TOKEN='e2e-legacy-tok-4f7a1c'

command -v tcpdump >/dev/null 2>&1 || { echo "FAIL: tcpdump 未安装 (apt-get install -y tcpdump)"; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "FAIL: jq 未安装"; exit 1; }
mkdir -p "$WD"

api() { curl --noproxy '*' -s -m 10 "$@"; }

enc_ok_count() { # server.log —— 剥 ANSI 后 "Node authenticated" 行中 enc=true 的条数
  local c; c=$(sed 's/\x1b\[[0-9;]*m//g' "$1" 2>/dev/null | grep "Node authenticated" | grep -c "enc=true")
  [ -n "$c" ] || c=0; printf '%s' "$c"
}

wait_port_free() { # port —— 上一会话进程死透、端口释放（最多 10s）
  local i; for i in $(seq 1 20); do
    ss -tln 2>/dev/null | grep -q ":$1 " || return 0
    sleep 0.5
  done; return 1
}

run_case() { # $1=tag $2=enc_enabled —— 成功注册返回 0
  local tag=$1 enc=$2
  local cwd="$WD/$tag"; mkdir -p "$cwd"
  printf 'server:\n  control_port: ":%s"\n  api_port: ":%s"\nchannel_encryption:\n  enabled: %s\n' \
    "$CTL_PORT" "$API_PORT" "$enc" > "$cwd/config.yaml"

  ( cd "$cwd" || exit 1
    env "MA_ADMIN_USER=$ADMIN_USER" "MA_ADMIN_PASS=$ADMIN_PASS" "MA_NODE_TOKEN=$NODE_TOKEN" \
      PATH=/usr/bin:/bin nohup "$SERV_BIN" >server.log 2>&1 &
    echo $! >server.pid )
  local spid; spid=$(cat "$cwd/server.pid")

  # 等 API 就绪（health 通 + 本进程仍活，防连到残留服务）
  local i ready=""
  for i in $(seq 1 40); do sleep 0.5
    if api "$API/api/v1/health" 2>/dev/null | grep -q '"code":0'; then ready=1; break; fi
  done
  if [ -z "$ready" ] || ! [ -d "/proc/$spid" ]; then
    echo "FAIL[$tag]: 服务端未就绪"; kill "$spid" 2>/dev/null; return 1
  fi

  # 节点 token：REST login + me/access-tokens（明文 token 不落配置文件）
  local jwt raw
  jwt=$(api -X POST "$API/api/v1/auth/login" -H 'Content-Type: application/json' \
    -d "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}" | jq -r '.data.token // empty')
  if [ -z "$jwt" ]; then echo "FAIL[$tag]: admin 登录失败"; kill "$spid" 2>/dev/null; return 1; fi
  raw=$(api -X POST "$API/api/v1/me/access-tokens" -H "Authorization: Bearer $jwt" \
    -H 'Content-Type: application/json' -d '{"name":"e2e"}' | jq -r '.data.token // empty')
  if [ -z "$raw" ]; then echo "FAIL[$tag]: 创建 access token 失败"; kill "$spid" 2>/dev/null; return 1; fi

  tcpdump -i lo -U -w "$cwd/$tag.pcap" "tcp port $CTL_PORT" >/dev/null 2>&1 &
  local tpid=$!
  sleep 1

  ( cd "$cwd" || exit 1
    HOME="$cwd" nohup "$CLI_BIN" -server "127.0.0.1:$CTL_PORT" -token "$raw" -id "$NODE_ID" \
      -name "$tag-node" -http "127.0.0.1:$CLI_HTTP" >client.log 2>&1 &
    echo $! >client.pid )
  local cpid; cpid=$(cat "$cwd/client.pid")

  # 等注册成功，再睡 6s 覆盖 register/sysinfo/tunnel_status 流量
  local reg=""
  for i in $(seq 1 60); do sleep 0.5
    grep -q "Registered as node" "$cwd/client.log" 2>/dev/null && { reg=1; break; }
  done
  sleep 6

  kill "$cpid" "$spid" 2>/dev/null
  sleep 1
  kill "$tpid" 2>/dev/null; wait "$tpid" 2>/dev/null
  wait "$cpid" 2>/dev/null; wait "$spid" 2>/dev/null
  wait_port_free "$CTL_PORT" || echo "WARN[$tag]: $CTL_PORT 未按时释放"

  if [ -z "$reg" ]; then echo "FAIL[$tag]: 客户端未注册"; return 1; fi
  return 0
}

has() { grep -a -q "$1" "$2" 2>/dev/null; }   # pcap 是二进制，grep -a 按文本搜
cnt() { local c; c=$(grep -a -c "$1" "$2" 2>/dev/null); [ -n "$c" ] || c=0; printf '%s' "$c"; }

FAILS=0

# ---- A. 明文对照 ----
if run_case plain false; then
  c_reg=$(cnt '"cmd":"register"' "$WD/plain/plain.pcap"); c_reg=${c_reg:-0}
  c_id=$(cnt "$NODE_ID" "$WD/plain/plain.pcap"); c_id=${c_id:-0}
  if [ "$c_reg" -ge 1 ] && [ "$c_id" -ge 1 ]; then
    echo "PASS[plain]: 明文捕获含 register/节点ID 标记 (register=$c_reg, 节点ID=$c_id)"
  else
    echo "FAIL[plain]: 明文捕获未检出预期明文标记 (register=$c_reg, 节点ID=$c_id)（对照失效，检查过滤表达式）"; FAILS=$((FAILS+1))
  fi
else
  echo "FAIL[plain]: 会话未完成"; FAILS=$((FAILS+1))
fi

# ---- B. 加密会话 ----
if run_case enc true; then
  c_enc=$(cnt '"enc":{"v":1}' "$WD/enc/enc.pcap"); c_enc=${c_enc:-0}
  c_proof=$(cnt '"proof":' "$WD/enc/enc.pcap"); c_proof=${c_proof:-0}
  if [ "$c_enc" -ge 1 ] && [ "$c_proof" -ge 1 ]; then
    echo "PASS[enc]: 加密捕获含握手前明文锚点 (enc 告示=$c_enc, proof 行=$c_proof)"
  else
    echo "FAIL[enc]: 未检出 enc 告示/proof 明文锚点 (enc=$c_enc, proof=$c_proof)（加密会话未真正建立）"; FAILS=$((FAILS+1))
  fi
  for m in '"cmd":"register"' '"cmd":"ping"' '"cmd":"tunnel_push"' "$NODE_ID"; do
    c=$(cnt "$m" "$WD/enc/enc.pcap"); c=${c:-0}
    if [ "$c" -ne 0 ]; then
      echo "FAIL[enc]: 密文捕获检出明文标记 $m ×$c（smux 未被 Noise 包裹或降级）"; FAILS=$((FAILS+1))
    fi
  done
  if [ "$FAILS" = 0 ]; then
    echo "INFO[enc]: 密文标记断言全部通过（register/ping/tunnel_push/节点ID 均不可见）"
  fi
  # enc.server.log 应有 enc=true，确证升级发生（console 日志带 ANSI 色码，须剥码匹配）
  c_ok=$(enc_ok_count "$WD/enc/server.log"); c_ok=${c_ok:-0}
  if [ "$c_ok" -ge 1 ]; then
    echo "PASS[enc]: 服务端日志确证 enc=true 升级 (Node authenticated enc=true ×$c_ok)"
  else
    echo "FAIL[enc]: 服务端日志无 enc=true"; FAILS=$((FAILS+1))
  fi
else
  echo "FAIL[enc]: 会话未完成"; FAILS=$((FAILS+1))
fi

echo "----"
if [ "$FAILS" = 0 ]; then echo "CAPTURE-VERIFY PASS"; else echo "CAPTURE-VERIFY FAIL ($FAILS)"; exit 1; fi
