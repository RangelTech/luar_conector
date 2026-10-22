#!/bin/bash
# Constroi os kits Python do conector (spec 11 §26.14): Python portatil + bibliotecas, um zip/tar por sistema.
# Roda no CI (Linux). Saida: kits/kit-windows-amd64.zip, kits/kit-linux-amd64.tar.gz, kits/kit-linux-arm64.tar.gz
set -euo pipefail
ROOT=$(pwd)
PKGS="openpyxl xlsxwriter python-docx pypdf pillow requests pip"
PY=3.12.8
PBS=20241206
mkdir -p "$ROOT/kits" "$ROOT/work"
cd "$ROOT/work"

# Windows: embeddable + pacotes instalados de fora (wheels win_amd64)
curl -fsSL -o embed.zip "https://www.python.org/ftp/python/$PY/python-$PY-embed-amd64.zip"
rm -rf win && mkdir -p win/python && unzip -q embed.zip -d win/python
python3 -m pip install -q --target win/python/Lib/site-packages --platform win_amd64 --python-version 3.12 --only-binary=:all: $PKGS
sed -i 's/^#import site/import site/' win/python/python312._pth
echo "Lib/site-packages" >> win/python/python312._pth
(cd win && zip -qr "$ROOT/kits/kit-windows-amd64.zip" python)

# Linux: python-build-standalone
for A in x86_64:amd64 aarch64:arm64; do
  ARCH=${A%%:*}
  NAME=${A##*:}
  curl -fsSL -o pbs-$NAME.tar.gz "https://github.com/astral-sh/python-build-standalone/releases/download/$PBS/cpython-$PY+$PBS-$ARCH-unknown-linux-gnu-install_only.tar.gz"
  rm -rf lin-$NAME && mkdir lin-$NAME && tar -xzf pbs-$NAME.tar.gz -C lin-$NAME
  if [ "$NAME" = amd64 ]; then
    lin-$NAME/python/bin/python3 -m pip install -q $PKGS
  else
    python3 -m pip install -q --target lin-$NAME/python/lib/python3.12/site-packages --platform manylinux2014_aarch64 --python-version 3.12 --only-binary=:all: $PKGS
  fi
  (cd lin-$NAME && tar -czf "$ROOT/kits/kit-linux-$NAME.tar.gz" python)
done
ls -l "$ROOT/kits"
