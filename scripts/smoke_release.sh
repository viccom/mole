#!/bin/bash
# 发布产物冒烟：v0.7.0 服务端 + v0.8.0 客户端 proof 全链路
set -u
SRV=/mnt/e/Go_codes/mole/_release/moles/moleagent-serv-linux-amd64
CLI=/mnt/e/Go_codes/mole/_release/molec/moleagent-client-linux-amd64
API=http://127.0.0.1:9983
cd /tmp && rm -rf smoke && mkdir smoke && cd smoke

env MA_ADMIN_USER=admin 'MA_ADMIN_PASS=SmokePass123!' 'MA_NODE_TOKEN=smoke-tok-1a' \
  PATH=/usr/bin:/bin nohup "$SRV" >s.log 2>&1 &
echo $! >s.pid
for i in $(seq 1 20); do sleep 0.5
  curl --noproxy '*' -s "$API/api/v1/health" | grep -q '"code":0' && break; done

JW=$(curl --noproxy '*' -s -X POST "$API/api/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"SmokePass123!"}' | jq -r .data.token)
TK=$(curl --noproxy '*' -s -X POST "$API/api/v1/me/access-tokens" \
  -H "Authorization: Bearer $JW" -H 'Content-Type: application/json' -d '{"name":"smoke"}' | jq -r .data.token)

HOME=/tmp/smoke nohup "$CLI" -server 127.0.0.1:9981 -token "$TK" \
  -id smoke001 -name smoke -http off >c.log 2>&1 &
echo $! >c.pid

ST=missing
for i in $(seq 1 16); do sleep 0.5
  ST=$(curl --noproxy '*' -s -H "Authorization: Bearer $JW" "$API/api/v1/nodes" \
    | jq -r '[.data.items[]? | select(.id=="smoke001")][0].status // empty')
  [ "$ST" = online ] && break; done

PROOF=$(grep -c "authenticated via access token proof" s.log 2>/dev/null || true)
echo "smoke: node=$ST proof认证日志=$PROOF server=$($SRV -version 2>/dev/null | head -c 8) client=$($CLI -version 2>/dev/null | head -c 8)"
kill "$(cat c.pid)" "$(cat s.pid)" 2>/dev/null
[ "$ST" = online ] && [ "${PROOF:-0}" -ge 1 ]
