#!/bin/bash
# Переносит общее ядро rustlecore из репозитория iOS/Android/Mac в службу Windows.
#
#     scripts/sync-core.sh            # из ~/Developer/RustleBoost
#     RUSTLEBOOST_REPO=/путь scripts/sync-core.sh
#
# Ядро одно на все платформы: Xray, Hysteria2, VK-туннели (WDTT/SRTP) и WARP.
# Раньше у Windows была своя урезанная копия (third_party/warpcore) и свои
# программы sing-box и xray.exe — поведение расходилось с телефонами. Теперь
# Windows собирается из того же кода, а копия здесь — только чтобы сборка
# на GitHub Actions не зависела от второго репозитория.
#
# После переноса: scripts/fetch-xray.sh (исходники Xray-core нужной версии).
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
src="${RUSTLEBOOST_REPO:-$HOME/Developer/RustleBoost}/Vendor"
dst="$root/daemon/third_party"

[ -f "$src/rustlecore/xray.go" ] || { echo "нет $src/rustlecore — укажите RUSTLEBOOST_REPO" >&2; exit 1; }

sync() {
    rsync -a --delete \
        --exclude '.deps/' --exclude 'cmd/' --exclude '*.bak' --exclude '.DS_Store' \
        "$src/$1/" "$dst/$1/"
}
sync rustlecore
sync vkturn
sync vkturnproxy

commit="$(git -C "$src/.." rev-parse --short HEAD 2>/dev/null || echo unknown)"
dirty="$(git -C "$src/.." status --porcelain -- Vendor/rustlecore Vendor/vkturn Vendor/vkturnproxy 2>/dev/null | head -1)"
echo "rustlecore из RustleBoost@$commit${dirty:+ (с незакоммиченными правками)}" > "$dst/rustlecore/SOURCE"
echo "Ядро перенесено: RustleBoost@$commit${dirty:+ + правки}"
