# G2a prototype: runs the automatic suite on an Android device or emulator and saves the
# JSON results (plan §5, S3). The APK must be built with --dart-define=AUTORUN=quick|full.
# Usage, from spikes/g2a:  .\android-run.ps1 -Out results-android-<name>.json [-Serial <adb serial>]
param(
    [Parameter(Mandatory)][string]$Out,
    [string]$Serial,
    [int]$TimeoutS = 900
)
$ErrorActionPreference = 'Stop'
$adb = "$env:LOCALAPPDATA\Android\sdk\platform-tools\adb.exe"
$adbArgs = if ($Serial) { @('-s', $Serial) } else { @() }
$apk = "$PSScriptRoot\app\build\app\outputs\flutter-apk\app-release.apk"
$pkg = 'dev.medxfer.medxfer_g2a'

& $adb @adbArgs install -r $apk | Out-Null
& $adb @adbArgs logcat -c
& $adb @adbArgs shell am start -W -n "$pkg/.MainActivity" | Select-String 'TotalTime'

# The release APK is not debuggable (no run-as): results come back through logcat, in chunks.
$t = Get-Date
do {
    Start-Sleep 15
    $log = & $adb @adbArgs logcat -d -s flutter:I
    $lines = $log | Select-String 'G2A_RESULT_CHUNK (\d+)/(\d+) (.*)$'
    $done = $lines -and $lines.Count -eq [int]$lines[0].Matches[0].Groups[2].Value
} while (-not $done -and ((Get-Date) - $t).TotalSeconds -lt $TimeoutS)
$log | Select-String ': (PASS|FAIL|EXCEPTION)' | ForEach-Object { ($_.Line -replace '^.*G2A ', '') -replace ' \{.*$', '' }
if (-not $done) { throw "no complete result after $TimeoutS s" }

$json = -join ($lines | Sort-Object { [int]$_.Matches[0].Groups[1].Value } | ForEach-Object { $_.Matches[0].Groups[3].Value })
$res = $json | ConvertFrom-Json
[IO.File]::WriteAllText((Join-Path $PSScriptRoot $Out), ($res | ConvertTo-Json -Depth 10))
"overall: $(if ($res.pass) { 'PASS' } else { 'FAIL' }) -> $Out"
