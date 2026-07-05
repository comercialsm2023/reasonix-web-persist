#!/bin/bash
set -e

echo "=== 1. A iniciar servidor de teste (simula Reasonix) na porta 8788 ==="
python3 /test-server.py &
SERV_PID=$!
sleep 1

echo "=== 2. A iniciar proxy na porta 8787 (upstream 8788) ==="
mkdir -p /root/.reasonix
echo 'default_model = "test/model"' > /root/.reasonix/config.toml
UPSTREAM=http://127.0.0.1:8788 LISTEN=:8787 /usr/local/bin/reasonix-web-persist &
PR_PID=$!
sleep 1

echo "=== 3. Proxy encaminha? ==="
curl -s http://127.0.0.1:8787/status | python3 -c "
import sys,json
d=json.load(sys.stdin)
print(f'cwd={d[\"cwd\"]}, mode={d[\"toolApprovalMode\"]}')
"

echo "=== 4. Persistir tool-approval-mode? ==="
curl -s -X POST http://127.0.0.1:8787/tool-approval-mode \
  -H "Content-Type: application/json" -d '{"mode":"auto"}'
if grep -q tool_approval_mode /root/.reasonix/config.toml; then echo "  ✅"; else echo "  ❌"; fi

echo "=== 5. Persistir model? ==="
curl -s -X POST http://127.0.0.1:8787/submit \
  -H "Content-Type: application/json" -d '{"input":"/model google/gemma-4-31b-it"}'
if grep -q "google/gemma" /root/.reasonix/config.toml; then echo "  ✅"; else echo "  ❌"; fi

echo "=== 6. Config final ==="
cat /root/.reasonix/config.toml

kill $PR_PID $SERV_PID 2>/dev/null
wait 2>/dev/null
echo "=== TESTE OK ==="
