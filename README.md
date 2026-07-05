# reasonix-web-persist

Reverse proxy para o `reasonix serve` que persiste as definições alteradas na web UI para o `~/.reasonix/config.toml`.

Sem modificar o Reasonix. Sem daemons extra. Um binário único, estático, ~5 MB.

## Como usar

1. Altera a porta do Reasonix no systemd:

```bash
# /etc/systemd/system/reasonix.service
ExecStart=/usr/bin/reasonix serve -addr 127.0.0.1:8788
systemctl daemon-reload && systemctl restart reasonix
```

2. Corre o proxy (escuta na 8787, encaminha para 8788):

```bash
MANAGE_REASONIX=1 reasonix-web-persist
```

Ou, ainda mais simples, substitui o `ExecStart` do systemd:

```bash
ExecStart=/usr/local/bin/reasonix-web-persist -upstream http://127.0.0.1:8788 -listen :8787
```

## O que persiste

- `/tool-approval-mode` → `default_tool_approval_mode` no config.toml
- `/auto-approve-tools` → `default_tool_approval_mode = "yolo"` no config.toml
- `/submit` com `/model <ref>` → `default_model` no config.toml

## Construir

```bash
go build -ldflags="-s -w" -o reasonix-web-persist .
```
