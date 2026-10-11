# G2a S5, G2A-05 on Android: (1) the app serves a file, goes to the background for 5 minutes with
# its TCP listener open, comes back; the PC then downloads the file. (2) The app is killed while
# serving: no process must remain, the port must be free, and a relaunch must serve again.
# The APK must be built with --dart-define=AUTORUN=listen --dart-define=SIZE_MB=<n>.
# Usage, from spikes/g2a:  .\g2a05-android.ps1 -Serial <adb serial> -PhoneIp <ip> [-BackgroundS 300]
param(
    [Parameter(Mandatory)][string]$Serial,
    [Parameter(Mandatory)][string]$PhoneIp,
    [int]$BackgroundS = 300,
    [string]$Xfer = "$PSScriptRoot\..\..\bin\xfer.exe",
    [string]$Out = 'results-android-g2a05.json'
)
$ErrorActionPreference = 'Stop'
$adb = "$env:LOCALAPPDATA\Android\sdk\platform-tools\adb.exe"
$pkg = 'dev.medxfer.medxfer_g2a'
$apk = "$PSScriptRoot\app\build\app\outputs\flutter-apk\app-release.apk"
$work = Join-Path $env:TEMP "g2a05_$(Get-Random)"
New-Item -ItemType Directory $work | Out-Null
$res = [ordered]@{}

function Log-Lines([string]$pattern) { & $adb -s $Serial logcat -d -s flutter:I | Select-String $pattern }
function Wait-Log([string]$pattern, [int]$timeoutS) {
    $t = Get-Date
    while (((Get-Date) - $t).TotalSeconds -lt $timeoutS) {
        $m = Log-Lines $pattern
        if ($m) { return $m[-1].Line }
        Start-Sleep 1
    }
    throw "timeout waiting for '$pattern'"
}
function App-Pids { (& $adb -s $Serial shell pidof $pkg) -split '\s+' | Where-Object { $_ } }
function Port-Open([int]$port) {
    $c = New-Object Net.Sockets.TcpClient
    try { return $c.ConnectAsync($PhoneIp, $port).Wait(3000) -and $c.Connected } catch { return $false } finally { $c.Dispose() }
}
function Launch {
    & $adb -s $Serial logcat -c
    & $adb -s $Serial shell am start -n "$pkg/.MainActivity" | Out-Null
    $line = Wait-Log 'G2A05_LISTENING' 120
    return @{ code = [regex]::Match($line, 'code=([0-9-]+)').Groups[1].Value; sha = [regex]::Match($line, 'sha256=([0-9a-f]+)').Groups[1].Value }
}
function Download([string]$code, [string]$sha) {
    $dir = Join-Path $work "in_$(Get-Random)"
    New-Item -ItemType Directory $dir | Out-Null
    $sw = [Diagnostics.Stopwatch]::StartNew()
    cmd /c "echo y| `"$Xfer`" recv -ip ${PhoneIp}:19990 -code $code -out `"$dir`"" *> "$dir.log"
    $f = Get-ChildItem $dir -File | Where-Object { $_.Name -notlike '*.medxfer' } | Select-Object -First 1
    $got = if ($f) { (Get-FileHash $f.FullName -Algorithm SHA256).Hash.ToLower() } else { '' }
    return @{ hash_ok = ($got -eq $sha); seconds = [math]::Round($sw.Elapsed.TotalSeconds, 1) }
}

try {
    & $adb -s $Serial shell am force-stop $pkg
    & $adb -s $Serial install -r $apk | Out-Null

    # (1) Background for $BackgroundS seconds with the listener open.
    $a = Launch
    $pid0 = App-Pids
    "listening, pid $pid0, code $($a.code)"
    & $adb -s $Serial shell input keyevent KEYCODE_HOME
    $probes = @()
    $t = Get-Date
    while (((Get-Date) - $t).TotalSeconds -lt $BackgroundS) {
        Start-Sleep 30
        $probes += [ordered]@{ s = [int]((Get-Date) - $t).TotalSeconds; port_open = (Port-Open 19990); pid = "$(App-Pids)" }
        "  t+$($probes[-1].s) s: port open $($probes[-1].port_open), pid $($probes[-1].pid)"
    }
    & $adb -s $Serial shell am start -n "$pkg/.MainActivity" | Out-Null
    Start-Sleep 5
    $d = Download $a.code $a.sha
    $sent = Wait-Log 'G2A05_SENT' 120
    $res.background = [ordered]@{
        seconds = $BackgroundS; pid_before = "$pid0"; probes = $probes
        lifecycle = @(Log-Lines 'G2A_LIFECYCLE' | ForEach-Object { ($_.Line -replace '^.*G2A_LIFECYCLE ', '') })
        download_after_foreground = $d; app_sent = ($sent -replace '^.*G2A05_SENT ', '')
    }
    "background: download after foreground hash_ok=$($d.hash_ok) in $($d.seconds) s"

    # (2) Killed while serving, then relaunched.
    $b = Launch
    $pidBefore = "$(App-Pids)"
    & $adb -s $Serial shell am force-stop $pkg
    Start-Sleep 2
    $pidAfterKill = "$(App-Pids)"
    $portAfterKill = Port-Open 19990
    $c = Launch
    $start = (Log-Lines 'G2A_RESTART|core loaded' | Select-Object -Last 1).Line
    $d2 = Download $c.code $c.sha
    $sent2 = Wait-Log 'G2A05_SENT' 120
    $res.kill = [ordered]@{
        pid_before = $pidBefore; pids_after_kill = $pidAfterKill; port_open_after_kill = $portAfterKill
        relaunch_listening = [bool]$c.code; download_after_relaunch = $d2; app_sent = ($sent2 -replace '^.*G2A05_SENT ', '')
    }
    "kill: pids after kill '$pidAfterKill', port open after kill $portAfterKill, relaunch hash_ok=$($d2.hash_ok)"
    $res.pass = $d.hash_ok -and $d2.hash_ok -and -not $pidAfterKill -and -not $portAfterKill
} finally {
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
[IO.File]::WriteAllText((Join-Path $PSScriptRoot $Out), ($res | ConvertTo-Json -Depth 6))
"overall G2A-05 Android: $(if ($res.pass) { 'PASS' } else { 'FAIL' }) -> $Out"
