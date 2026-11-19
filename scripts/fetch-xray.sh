#!/bin/bash
# Готовит исходники Xray-core для службы Windows: та же версия и те же
# правки, что в ядре iOS/Android/Mac, плюс правки Windows (daemon/patches/xray).
#
#     scripts/fetch-xray.sh
#
# Запускается и на Маке, и на GitHub Actions (Git Bash на Windows).
# Версия Xray-core та же, что в RustleBoost/Scripts/fetch-cores.sh.
set -euo pipefail

XRAY_TAG="v26.7.28"

root="$(cd "$(dirname "$0")/.." && pwd)"
dir="$root/daemon/third_party/xray-core"

if [ ! -d "$dir/.git" ]; then
    echo "Клонирую Xray-core $XRAY_TAG"
    rm -rf "$dir"
    git clone --quiet --depth 1 --branch "$XRAY_TAG" https://github.com/XTLS/Xray-core.git "$dir"
fi

have="$(git -C "$dir" describe --tags --exact-match 2>/dev/null || echo '?')"
[ "$have" = "$XRAY_TAG" ] || { echo "в $dir версия $have, нужна $XRAY_TAG — удалите папку" >&2; exit 1; }

# Общие правки ядра (окна HTTP/2) и правки Windows. Каждая накладывается один
# раз: если уже стоит, проверка обратного наложения проходит, и её пропускаем.
for patch in "$root"/daemon/third_party/rustlecore/patches/*.patch "$root"/daemon/patches/xray/*.patch; do
    [ -f "$patch" ] || continue
    if git -C "$dir" apply --reverse --check "$patch" 2>/dev/null; then
        continue
    fi
    git -C "$dir" apply "$patch"
    echo "Правка наложена: $(basename "$patch")"
done
