#requires -Version 7
<#
Стенд Windows (.github/workflows/lab.yml): служба поднимается так же, как в
приложении, и по очереди подключается к серверам всех вкладок. После каждого
подключения проверяется то, что увидел бы человек: открываются ли сайты, есть
ли DNS, сменился ли адрес, какая скорость. Потом отключение — и проверка, что
интернет без VPN вернулся.

В открытый журнал задания пишутся только итоги. Подробности (адреса, журналы
службы и ядра) уходят на наш сервер: server/ci_lab.go.
#>
param(
    [Parameter(Mandatory)] [string] $Bin,
    [string] $Arch = "amd64",
    # Сколько серверов каждой вкладки проверять (0 — все).
    [int] $PerGroup = 0
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$Api = "http://127.0.0.1:7777/api"
$Lab = "https://auth.lindavpn.com/internal/ci"
$Work = Join-Path $env:RUNNER_TEMP "rb-lab"
$Data = Join-Path $Work "data"
$Out = Join-Path $Work "out"
New-Item -ItemType Directory -Force -Path $Data, $Out | Out-Null
$Bin = (Resolve-Path $Bin).Path

$script:results = [System.Collections.Generic.List[object]]::new()
$script:daemon = $null

function Say([string] $text) { Write-Host $text }

# ── сервер стенда ─────────────────────────────────────────────────────────

function Get-LabToken {
    $uri = "$($env:ACTIONS_ID_TOKEN_REQUEST_URL)&audience=rustleboost-lab"
    $r = Invoke-RestMethod -Uri $uri -Headers @{ Authorization = "bearer $($env:ACTIONS_ID_TOKEN_REQUEST_TOKEN)" } -TimeoutSec 30
    return $r.value
}

function Send-LabBytes([string] $Name, [byte[]] $Bytes) {
    $chunk = 900KB
    $parts = [Math]::Max(1, [Math]::Ceiling($Bytes.Length / $chunk))
    for ($i = 0; $i -lt $parts; $i++) {
        $len = [Math]::Min($chunk, $Bytes.Length - $i * $chunk)
        $part = [byte[]]::new([Math]::Max(0, $len))
        if ($len -gt 0) { [Array]::Copy($Bytes, $i * $chunk, $part, 0, $len) }
        $suffix = if ($parts -gt 1) { ".part$i" } else { "" }
        try {
            Invoke-RestMethod -Method Put -Uri "$Lab/upload?name=$Name$suffix" -TimeoutSec 60 `
                -Headers @{ Authorization = "Bearer $(Get-LabToken)" } `
                -ContentType "application/octet-stream" -Body $part | Out-Null
        } catch {
            Say "  не удалось сдать $Name$suffix`: $($_.Exception.Message)"
        }
    }
}

function Send-LabFile([string] $Name, [string] $Path) {
    if (-not (Test-Path $Path)) { return }
    $stream = [IO.File]::Open($Path, "Open", "Read", "ReadWrite")
    try {
        $buffer = [IO.MemoryStream]::new()
        $gzip = [IO.Compression.GZipStream]::new($buffer, [IO.Compression.CompressionLevel]::Optimal)
        $stream.CopyTo($gzip)
        $gzip.Close()
        Send-LabBytes "$Name.gz" $buffer.ToArray()
    } finally {
        $stream.Close()
    }
}

# ── служба ────────────────────────────────────────────────────────────────

function Invoke-Daemon([string] $Method, [string] $Path, $Body = $null, [int] $Timeout = 30) {
    $params = @{ Method = $Method; Uri = "$Api$Path"; TimeoutSec = $Timeout }
    if ($null -ne $Body) {
        $params.Body = ($Body | ConvertTo-Json -Compress -Depth 5)
        $params.ContentType = "application/json"
    }
    return Invoke-RestMethod @params
}

function Start-Daemon {
    $script:daemon = Start-Process -FilePath (Join-Path $Bin "daemon.exe") -ArgumentList @("-data", $Data) -PassThru -WindowStyle Hidden
    for ($i = 0; $i -lt 60; $i++) {
        Start-Sleep -Milliseconds 500
        try { Invoke-Daemon GET "/status" | Out-Null; return } catch { }
    }
    throw "служба не ответила за 30 с"
}

function Wait-State([string] $Want, [int] $Seconds) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    do {
        try {
            $s = Invoke-Daemon GET "/status"
            if ($s.state -eq $Want) { return $s }
        } catch { }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    return $null
}

# ── проверки ──────────────────────────────────────────────────────────────

function Invoke-Curl([string[]] $Arguments, [int] $Seconds = 15) {
    $out = & curl.exe -s -m $Seconds @Arguments 2>&1
    return [pscustomobject]@{ ok = ($LASTEXITCODE -eq 0); code = $LASTEXITCODE; text = ($out -join "`n").Trim() }
}

function Measure-Step([scriptblock] $Block) {
    $sw = [Diagnostics.Stopwatch]::StartNew()
    try {
        $value = & $Block
        return [pscustomobject]@{ ok = $true; ms = $sw.ElapsedMilliseconds; value = $value; error = "" }
    } catch {
        return [pscustomobject]@{ ok = $false; ms = $sw.ElapsedMilliseconds; value = $null; error = $_.Exception.Message }
    }
}

function Get-ExitIP {
    $r = Invoke-Curl @("https://api.ipify.org") 12
    if ($r.ok -and $r.text -match '^\d{1,3}(\.\d{1,3}){3}$') { return $r.text }
    $r = Invoke-Curl @("https://ifconfig.me/ip") 12
    if ($r.ok -and $r.text -match '^\d{1,3}(\.\d{1,3}){3}$') { return $r.text }
    return ""
}

function Get-Trace {
    $r = Invoke-Curl @("https://www.cloudflare.com/cdn-cgi/trace") 12
    $map = @{}
    if ($r.ok) {
        foreach ($line in $r.text -split "`n") {
            if ($line -match '^(\w+)=(.*)$') { $map[$Matches[1]] = $Matches[2].Trim() }
        }
    }
    return $map
}

function Get-NetSnapshot([string] $Tag) {
    $file = Join-Path $Out "net-$Tag.txt"
    $lines = @()
    $lines += "== route print -4"
    $lines += (& route.exe print -4 2>&1)
    $lines += "== адаптеры"
    $lines += (Get-NetAdapter -IncludeHidden -ErrorAction SilentlyContinue | Format-Table -AutoSize Name, InterfaceDescription, ifIndex, Status, MacAddress | Out-String -Width 220)
    $lines += (Get-NetIPInterface -AddressFamily IPv4 -ErrorAction SilentlyContinue | Format-Table -AutoSize ifIndex, InterfaceAlias, ConnectionState, NlMtu, InterfaceMetric, Forwarding, WeakHostReceive, WeakHostSend | Out-String -Width 220)
    $lines += (Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue | Format-Table -AutoSize ifIndex, InterfaceAlias, IPAddress, PrefixLength, AddressState | Out-String -Width 220)
    $lines += (Get-DnsClientServerAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue | Format-Table -AutoSize InterfaceIndex, InterfaceAlias, ServerAddresses | Out-String -Width 220)
    $lines += (Get-NetConnectionProfile -ErrorAction SilentlyContinue | Format-Table -AutoSize InterfaceAlias, NetworkCategory, IPv4Connectivity | Out-String -Width 220)
    $lines += "== счётчики IPv4"
    $lines += (& netsh.exe interface ipv4 show ipstats 2>&1)
    $lines += (& netsh.exe interface ipv4 show tcpstats 2>&1)
    $lines += (& netsh.exe interface ipv4 show udpstats 2>&1)
    [IO.File]::WriteAllText($file, (($lines | ForEach-Object { "$_" }) -join "`r`n"), [Text.UTF8Encoding]::new($false))
}

function Test-Online([string] $Tag, [string] $Baseline) {
    $checks = [ordered]@{}
    $checks.http204 = Measure-Step { $r = Invoke-Curl @("-o", "NUL", "-w", "%{http_code}", "http://www.gstatic.com/generate_204") 12; if ($r.text -ne "204") { throw "ответ $($r.text) (curl $($r.code))" }; $r.text }
    $checks.https = Measure-Step { $r = Invoke-Curl @("-o", "NUL", "-w", "%{http_code}", "https://www.google.com/generate_204") 15; if ($r.text -ne "204") { throw "ответ $($r.text) (curl $($r.code))" }; $r.text }
    $checks.youtube = Measure-Step { $r = Invoke-Curl @("-o", "NUL", "-w", "%{http_code}", "https://www.youtube.com/") 20; if ($r.text -notmatch '^(200|301|302|303)$') { throw "ответ $($r.text) (curl $($r.code))" }; $r.text }
    $checks.dns = Measure-Step { (Resolve-DnsName -Name "www.wikipedia.org" -Type A -DnsOnly -QuickTimeout -ErrorAction Stop | Where-Object { $_.IPAddress } | Select-Object -First 1).IPAddress }
    $checks.udp = Measure-Step { (Resolve-DnsName -Name "example.org" -Type A -Server 9.9.9.9 -DnsOnly -QuickTimeout -ErrorAction Stop | Where-Object { $_.IPAddress } | Select-Object -First 1).IPAddress }
    $exit = Get-ExitIP
    $trace = Get-Trace
    $checks.exitChanged = [pscustomobject]@{ ok = ($exit -ne "" -and $exit -ne $Baseline); ms = 0; value = $exit; error = $(if ($exit -eq "") { "адрес не получен" } elseif ($exit -eq $Baseline) { "адрес не сменился" } else { "" }) }
    $checks.speed = Measure-Step {
        $r = Invoke-Curl @("-o", "NUL", "-w", "%{speed_download}", "https://speed.cloudflare.com/__down?bytes=25000000") 25
        $mbit = [Math]::Round([double]::Parse(($r.text -replace ',', '.'), [Globalization.CultureInfo]::InvariantCulture) * 8 / 1e6, 1)
        if ($mbit -le 0) { throw "скорость 0 (curl $($r.code))" }
        $mbit
    }
    Get-NetSnapshot $Tag
    return [pscustomobject]@{ checks = $checks; exit = $exit; warp = $trace["warp"]; loc = $trace["loc"]; colo = $trace["colo"] }
}

function Summary($checks) {
    $marks = foreach ($k in $checks.Keys) {
        $c = $checks[$k]
        if ($c.ok) { "$k ✓" } else { "$k ✗" }
    }
    return ($marks -join ", ")
}

# ── прогон ────────────────────────────────────────────────────────────────

$started = Get-Date
$system = [ordered]@{
    arch = $Arch
    os = (Get-CimInstance Win32_OperatingSystem | Select-Object -ExpandProperty Caption)
    build = [Environment]::OSVersion.Version.ToString()
    firewall = (Get-NetFirewallProfile -ErrorAction SilentlyContinue | ForEach-Object { "$($_.Name)=$($_.Enabled)" }) -join ","
}
Say "Стенд: $($system.os) $($system.build) ($Arch), брандмауэр: $($system.firewall)"

try {
    $baseline = Get-ExitIP
    if ($baseline -eq "") { throw "у машины GitHub нет интернета ещё до VPN" }
    Get-NetSnapshot "00-before"

    Start-Daemon
    Say "Служба запущена"

    $subscription = (Invoke-RestMethod -Uri "$Lab/subscription" -Headers @{ Authorization = "Bearer $(Get-LabToken)" } -TimeoutSec 30).subscription
    Write-Host "::add-mask::$subscription"
    $r = Invoke-Daemon POST "/subscription" @{ url = $subscription } 90
    # Invoke-RestMethod отдаёт массив JSON одним объектом — разворачиваем.
    $servers = @(Invoke-Daemon GET "/servers" | ForEach-Object { $_ })
    $groups = $servers | Group-Object { if ($_.group) { $_.group } else { "regular" } }
    Say ("Подписка: " + (($groups | ForEach-Object { "$($_.Name) $($_.Count)" }) -join ", "))

    $order = @("regular", "rustleboost", "warp")
    $plan = [System.Collections.Generic.List[object]]::new()
    foreach ($g in $order) {
        $list = @($servers | Where-Object { ($(if ($_.group) { $_.group } else { "regular" })) -eq $g })
        if ($PerGroup -gt 0) { $list = @($list | Select-Object -First $PerGroup) }
        foreach ($item in $list) { $plan.Add($item) }
    }

    $n = 0
    foreach ($server in $plan) {
        $n++
        $group = if ($server.group) { $server.group } else { "regular" }
        $label = "[$n/$($plan.Count)] $group · $($server.name) ($($server.protocol)/$($server.transport))"
        $tag = "{0:d2}-{1}" -f $n, $group
        $entry = [ordered]@{ n = $n; group = $group; name = $server.name; protocol = $server.protocol; transport = $server.transport; security = $server.security; engine = $server.engine }
        $connect = Measure-Step { Invoke-Daemon POST "/connect" @{ server_id = $server.id } 120 }
        $status = $null
        try { $status = Invoke-Daemon GET "/status" } catch { }
        $entry.connectMs = $connect.ms
        $entry.connectError = $connect.error
        $entry.state = $status.state
        $entry.engineUsed = $status.engine
        $entry.warning = $status.warning
        $entry.statusError = $status.error
        if ($status.state -eq "connected") {
            Start-Sleep -Seconds 1
            $online = Test-Online $tag $baseline
            $entry.checks = $online.checks
            $entry.exit = $online.exit
            $entry.warp = $online.warp
            $entry.loc = $online.loc
            $ok = $online.checks.http204.ok -and $online.checks.https.ok -and $online.checks.dns.ok -and $online.checks.exitChanged.ok
            if ($group -eq "warp") { $ok = $ok -and ($online.warp -eq "on" -or $online.warp -eq "plus") }
            $entry.ok = $ok
            $speed = if ($online.checks.speed.ok) { "$($online.checks.speed.value) Мбит/с" } else { "скорость —" }
            $mark = if ($ok) { "PASS" } else { "FAIL" }
            $warpNote = if ($group -eq "warp") { ", warp=$($online.warp)" } else { "" }
            Say "$mark $label — подключение $([Math]::Round($connect.ms / 1000, 1)) с; $(Summary $online.checks); $speed$warpNote"
        } else {
            $entry.ok = $false
            Get-NetSnapshot "$tag-failed"
            Say "FAIL $label — не подключилось за $([Math]::Round($connect.ms / 1000, 1)) с"
        }

        $disconnect = Measure-Step { Invoke-Daemon POST "/disconnect" $null 60 }
        $entry.disconnectMs = $disconnect.ms
        $back = Wait-State "disconnected" 20
        Start-Sleep -Seconds 1
        $after = Get-ExitIP
        $entry.restored = ($after -eq $baseline)
        if (-not $entry.restored) {
            Get-NetSnapshot "$tag-after"
            Say "  ВНИМАНИЕ: после отключения интернет без VPN не вернулся (адрес: $(if ($after) { 'другой' } else { 'нет' }))"
            Start-Sleep -Seconds 5
        }
        $script:results.Add([pscustomobject]$entry)
    }
} catch {
    Say "Стенд прерван: $($_.Exception.Message)"
    $script:results.Add([pscustomobject]@{ fatal = $_.Exception.Message; at = $_.InvocationInfo.PositionMessage })
} finally {
    try { Invoke-Daemon POST "/disconnect" $null 30 | Out-Null } catch { }
    Start-Sleep -Seconds 2
    if ($script:daemon) { try { Stop-Process -Id $script:daemon.Id -Force -ErrorAction SilentlyContinue } catch { } }
    Start-Sleep -Seconds 1

    $report = [ordered]@{
        system = $system
        started = $started.ToString("o")
        seconds = [int]((Get-Date) - $started).TotalSeconds
        results = $script:results
    }
    $json = $report | ConvertTo-Json -Depth 8
    Send-LabBytes "result-$Arch.json" ([Text.Encoding]::UTF8.GetBytes($json))
    Send-LabFile "daemon-$Arch.log" (Join-Path $Data "daemon.log")
    Send-LabFile "xray-$Arch.log" (Join-Path $Data "xray-core.log")
    $nets = Get-ChildItem -Path $Out -Filter "net-*.txt" -ErrorAction SilentlyContinue | Sort-Object Name
    if ($nets) {
        $all = ($nets | ForEach-Object { "##### $($_.Name)`n" + (Get-Content $_.FullName -Raw) }) -join "`n"
        Send-LabBytes "net-$Arch.txt" ([Text.Encoding]::UTF8.GetBytes($all))
    }
    $pass = @($script:results | Where-Object { $_.ok }).Count
    $total = @($script:results | Where-Object { $null -ne $_.n }).Count
    Say "Итог: $pass из $total"
}
