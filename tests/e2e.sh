#!/bin/bash
# Teste ponta a ponta: hub + conector pareado + ssh/sftp pelo relay. Roda no CI (Linux).
set -euo pipefail
D=$(mktemp -d); trap 'kill $(jobs -p) 2>/dev/null || true' EXIT
trap 'echo "FALHOU na linha $LINENO"; tail -30 $D/hub.log $D/conn.log 2>/dev/null' ERR
export HUB_DATA=$D/hub HUB_INTERNAL_TOKEN=tok-interno HUB_RELAY_SECRET=segredo HUB_ADDR=127.0.0.1:18080 HUB_RELAY_ADDR=127.0.0.1:12222
./dist/hub > $D/hub.log 2>&1 &
sleep 1
ssh-keygen -q -t ed25519 -N "" -f $D/conn_key
PUB=$(cat $D/conn_key.pub)
CODE=$(curl -sf -H "Authorization: Bearer tok-interno" -d "{\"host_id\":\"h1\",\"tenant_id\":\"t1\",\"name\":\"XPTO\",\"conn_pub\":\"$PUB\"}" http://127.0.0.1:18080/internal/pair | python3 -c 'import sys,json;print(json.load(sys.stdin)["code"])')
export LUAR_CONECTOR_HOME=$D/pc
./dist/connector pair --hub http://127.0.0.1:18080 --code "$CODE"
if ./dist/connector pair --hub http://127.0.0.1:18080 --code "$CODE" 2>/dev/null; then echo "codigo reutilizado!"; exit 1; fi
./dist/connector run > $D/conn.log 2>&1 &
sleep 3
curl -sf -H "Authorization: Bearer tok-interno" "http://127.0.0.1:18080/internal/hosts?tenant_id=t1" | grep -q '"online":true' || { echo "nao ficou online"; cat $D/hub.log; exit 1; }
TOK=$(curl -sf -H "Authorization: Bearer tok-interno" -d '{"host_id":"h1"}' http://127.0.0.1:18080/internal/relay-token | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')
SSHO="-i $D/conn_key -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ProxyCommand=\"python3 scripts/luar-relay.py 127.0.0.1 12222 $TOK\""
OUT=$(eval ssh $SSHO -q x@xpto "echo ola-do-computador")
[ "$OUT" = "ola-do-computador" ] || { echo "ssh falhou: $OUT"; exit 1; }
echo "teste de arquivo" > $D/a.txt
eval scp -q $SSHO $D/a.txt x@xpto:$D/b.txt
[ "$(cat $D/b.txt)" = "teste de arquivo" ] || { echo "scp falhou"; exit 1; }
# token invalido
if python3 scripts/luar-relay.py 127.0.0.1 12222 invalido </dev/null 2>/dev/null; then echo "token invalido aceito"; exit 1; fi
# tunel de porta (direct-tcpip): sobe um servidor local e acessa por -L
python3 -m http.server 18999 --bind 127.0.0.1 --directory $D >/dev/null 2>&1 &
sleep 1
eval ssh $SSHO -q -N -f -L 18998:127.0.0.1:18999 x@xpto
curl -sf http://127.0.0.1:18998/a.txt | grep -q "teste de arquivo" || { echo "tunel falhou"; exit 1; }
# offline
pkill -f "dist/connector run" || true
sleep 50
curl -sf -H "Authorization: Bearer tok-interno" "http://127.0.0.1:18080/internal/hosts?tenant_id=t1" | grep -q '"online":false' || { echo "nao ficou offline"; exit 1; }
echo "E2E OK"
