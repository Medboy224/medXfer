# G2a, G2A-07: app (Android) <-> xfer (this PC), 1 GiB each way, SHA-256 checked on both sides.
# The APK must be built with --dart-define=AUTORUN=g2a07 --dart-define=PEER=<this PC ip>:18888.
# Usage, from spikes/g2a:  .\g2a07.ps1 -Serial <adb serial> -PhoneIp <ip> [-Xfer ..\..\bin\xfer.exe]
param(
    [Parameter(Mandatory)][string]$Serial,
    [Parameter(Mandatory)][string]$PhoneIp,
    [string]$Xfer = "$PSScriptRoot\..\..\bin\xfer.exe",
    [int]$SizeMB = 1024,
    [string]$Out = 'results-android-g2a07.json'
)
$ErrorActionPreference = 'Stop'
$adb = "$env:LOCALAPPDATA\Android\sdk\platform-tools\adb.exe"
$work = Join-Path $env:TEMP "g2a07_$(Get-Random)"
New-Item -ItemType Directory "$work\in", "$work\src" | Out-Null

function Wait-Log([string]$pattern, [int]$timeoutS) {
    $t = Get-Date
    while (((Get-Date) - $t).TotalSeconds -lt $timeoutS) {
        $m = & $adb -s $Serial logcat -d -s flutter:I | Select-String $pattern
        if ($m) { return $m[-1].Line }
        Start-Sleep 2
    }
    throw "timeout waiting for '$pattern'"
}

try {
    # The app runs in the background job; android-run.ps1 collects its JSON at the end. The log is
    # cleared first so that lines of an earlier run cannot be taken for this one.
    & $adb -s $Serial shell am force-stop dev.medxfer.medxfer_g2a
    & $adb -s $Serial logcat -c
    $job = Start-Job { param($d, $s, $o) & "$d\android-run.ps1" -Serial $s -Out $o -TimeoutS 2400 } -ArgumentList $PSScriptRoot, $Serial, $Out

    # Phase 1: app -> PC, with the pairing code the app printed; cmd pipes the confirmation
    # (a PowerShell pipe would prefix it with a BOM).
    $line = Wait-Log 'G2A07_SENDING' 300
    $appSha = [regex]::Match($line, 'sha256=([0-9a-f]+)').Groups[1].Value
    $code = [regex]::Match($line, 'code=([0-9-]+)').Groups[1].Value
    $sw = [Diagnostics.Stopwatch]::StartNew()
    cmd /c "echo y| `"$Xfer`" recv -ip ${PhoneIp}:19990 -code $code -out `"$work\in`"" *> "$work\recv.log"
    $recvS = $sw.Elapsed.TotalSeconds
    $got = Get-ChildItem "$work\in" -File | Select-Object -First 1
    $pcSha = if ($got) { (Get-FileHash $got.FullName -Algorithm SHA256).Hash.ToLower() } else { '' }
    $m = Select-String -Path "$work\recv.log" -Pattern 'Transfer completed in (\S+)' | Select-Object -Last 1
    $engine = if ($m) { $m.Matches[0].Groups[1].Value } else { Get-Content "$work\recv.log" -Tail 3 | Out-String }
    "phase 1 app -> PC: hash $(if ($pcSha -eq $appSha) { 'OK' } else { "MISMATCH app=$appSha pc=$pcSha" }), engine $engine, recv process $([math]::Round($recvS, 1)) s"
    if ($got) { Remove-Item $got.FullName }

    # Phase 2: PC -> app. The app retries receive_file until this sender is up.
    Wait-Log 'G2A07_WAITING_FOR' 300 | Out-Null
    $src = "$work\src\from_pc.bin"
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    $buf = New-Object byte[] (4MB)
    $fs = [IO.File]::Create($src)
    for ($i = 0; $i -lt $SizeMB / 4; $i++) { $rng.GetBytes($buf); $fs.Write($buf, 0, $buf.Length) }
    $fs.Close()
    $srcSha = (Get-FileHash $src -Algorithm SHA256).Hash.ToLower()
    $send = Start-Process $Xfer -ArgumentList 'send', '--port', '18888', $src -PassThru -NoNewWindow -RedirectStandardOutput "$work\send.log"
    $line = Wait-Log 'G2A07_RECEIVED' 1200
    $phoneSha = [regex]::Match($line, 'sha256=([0-9a-f]+)').Groups[1].Value
    if (-not $send.HasExited) { $send.Kill() }
    "phase 2 PC -> app: hash $(if ($phoneSha -eq $srcSha) { 'OK' } else { "MISMATCH pc=$srcSha app=$phoneSha" })"

    $job | Wait-Job -Timeout 300 | Receive-Job
    $res = Get-Content (Join-Path $PSScriptRoot $Out) -Raw | ConvertFrom-Json
    $res.g2a07 | Add-Member pc_side @{
        app_to_pc = @{ hash_ok = ($pcSha -eq $appSha); pc_sha256 = $pcSha; xfer_recv_engine = $engine; xfer_recv_process_s = $recvS }
        pc_to_app = @{ hash_ok = ($phoneSha -eq $srcSha); pc_sha256 = $srcSha }
    }
    $res.pass = $res.pass -and ($pcSha -eq $appSha) -and ($phoneSha -eq $srcSha)
    [IO.File]::WriteAllText((Join-Path $PSScriptRoot $Out), ($res | ConvertTo-Json -Depth 10))
    "overall G2A-07: $(if ($res.pass) { 'PASS' } else { 'FAIL' })"
} finally {
    Get-Job | Remove-Job -Force
    Remove-Item -Recurse -Force $work
}
