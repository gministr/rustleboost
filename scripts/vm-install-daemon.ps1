# Подменяет службу и драйвер в установленном приложении RustleBoost (машина
# для проверки). Запускается по SSH из scripts/build-test-daemon.sh --install.
# Работающее приложение не трогает: просит закрыть его и выходит.
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$ErrorActionPreference = 'Stop'
$dirs = Get-ChildItem -Path 'C:\Users\*\AppData\Local\RustleBoost\RustleBoost.exe', 'C:\Program Files\RustleBoost\RustleBoost.exe' -ErrorAction SilentlyContinue | ForEach-Object { $_.DirectoryName }
if (-not $dirs) { Write-Output 'NOT_INSTALLED: RustleBoost ещё не установлен'; exit 2 }
$running = Get-Process -ErrorAction SilentlyContinue | Where-Object { $p = $_.Path; $p -and ($dirs | Where-Object { $p -like "$_\*" }) }
if ($running) { Write-Output ('RUNNING: закройте RustleBoost (' + (($running | ForEach-Object { $_.ProcessName }) -join ', ') + ')'); exit 3 }
foreach ($dir in $dirs) {
    if (-not (Test-Path "$dir\daemon-1.6.1.exe") -and (Test-Path "$dir\daemon.exe")) { Copy-Item "$dir\daemon.exe" "$dir\daemon-1.6.1.exe" }
    if (-not (Test-Path "$dir\wintun-1.6.1.dll") -and (Test-Path "$dir\wintun.dll")) { Copy-Item "$dir\wintun.dll" "$dir\wintun-1.6.1.dll" }
    Copy-Item 'C:\Windows\Temp\rb-daemon.exe' "$dir\daemon.exe" -Force
    Copy-Item 'C:\Windows\Temp\rb-wintun.dll' "$dir\wintun.dll" -Force
    $h = (Get-FileHash "$dir\daemon.exe" -Algorithm SHA256).Hash.Substring(0,12)
    Write-Output "INSTALLED: $dir (daemon $h)"
}
