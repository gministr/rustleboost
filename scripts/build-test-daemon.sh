#!/bin/bash
# Проверочная сборка службы для виртуальной машины Windows (ARM64) и обычных
# ПК (x64). Оболочка и интерфейс берутся из установленного выпуска: в папке
# приложения подменяются только daemon.exe и wintun.dll.
#
#     scripts/build-test-daemon.sh            # собрать
#     scripts/build-test-daemon.sh --install  # и поставить в машину (ssh rbvm)
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
out="$root/build/test"
mkdir -p "$out"
export GOROOT="${GOROOT:-$HOME/Developer/RustleBoost/.toolchain/go}"
export GOPATH="${GOPATH:-$HOME/Developer/RustleBoost/.toolchain/gopath}"
export PATH="$GOROOT/bin:$PATH"

"$root/scripts/fetch-xray.sh" >/dev/null
cd "$root/daemon"
for arch in arm64 amd64; do
    GOOS=windows GOARCH=$arch CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H windowsgui" -o "$out/daemon-$arch.exe" .
done
ls -la "$out"/daemon-*.exe

if [ "${1:-}" = "--install" ]; then
    [ -f "$out/wintun-arm64.dll" ] || { echo "нет $out/wintun-arm64.dll (официальный 0.14.1)" >&2; exit 1; }
    scp -q "$out/daemon-arm64.exe" rbvm:'C:/Windows/Temp/rb-daemon.exe'
    scp -q "$out/wintun-arm64.dll" rbvm:'C:/Windows/Temp/rb-wintun.dll'
    # Скриптом-файлом: из стандартного ввода PowerShell читает как с клавиатуры.
    tmp="$(mktemp)"; printf '\xef\xbb\xbf' > "$tmp"; cat "$root/scripts/vm-install-daemon.ps1" >> "$tmp"
    scp -q "$tmp" rbvm:'C:/Windows/Temp/rb-install.ps1'; rm -f "$tmp"
    ssh rbvm 'powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File C:\Windows\Temp\rb-install.ps1'
fi
