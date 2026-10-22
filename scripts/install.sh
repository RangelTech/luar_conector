#!/bin/sh
# Instalador Linux do conector LUAR: curl -fsSL https://conector.rangeltech.net/dl/install.sh | sudo sh -s -- CODIGO
set -e
HUB="${LUAR_HUB:-https://conector.rangeltech.net}"
CODE="$1"
[ -n "$CODE" ] || { echo "uso: sudo sh install.sh CODIGO_DE_PAREAMENTO"; exit 2; }
case "$(uname -m)" in x86_64|amd64) A=amd64;; aarch64|arm64) A=arm64;; *) echo "arquitetura nao suportada"; exit 1;; esac
TMP=$(mktemp)
curl -fsSL "$HUB/dl/luar-conector-linux-$A" -o "$TMP"
chmod +x "$TMP"
"$TMP" pair --hub "$HUB" --code "$CODE"
"$TMP" install
rm -f "$TMP"
