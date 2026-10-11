# G2a S5, G2A-09 and G2A-10: runs the Android probes and, during each listening window of the
# network probe, sends UDP packets from this PC to the discovery multicast group and broadcast.
# The APK must be built with --dart-define=AUTORUN=probes [--dart-define=SIZE_MB=<cost file size>].
# Usage, from spikes/g2a:  .\android-probes.ps1 -Serial <adb serial> -Out results-android-probes-<name>.json
param(
    [Parameter(Mandatory)][string]$Serial,
    [Parameter(Mandatory)][string]$Out,
    [Parameter(Mandatory)][string]$LocalIp # this PC's address on the phone's network: the packets leave by that interface
)
$ErrorActionPreference = 'Stop'
$adb = "$env:LOCALAPPDATA\Android\sdk\platform-tools\adb.exe"

& $adb -s $Serial shell am force-stop dev.medxfer.medxfer_g2a
& $adb -s $Serial logcat -c
$job = Start-Job { param($d, $s, $o) & "$d\android-run.ps1" -Serial $s -Out $o -TimeoutS 1800 } -ArgumentList $PSScriptRoot, $Serial, $Out

$udp = New-Object Net.Sockets.UdpClient (New-Object Net.IPEndPoint ([Net.IPAddress]::Parse($LocalIp)), 0)
$udp.EnableBroadcast = $true
$udp.MulticastLoopback = $false
$group = New-Object Net.IPEndPoint ([Net.IPAddress]::Parse('239.255.255.250')), 19997
$bcast = New-Object Net.IPEndPoint ([Net.IPAddress]::Broadcast), 19998
$subnet = New-Object Net.IPEndPoint ([Net.IPAddress]::Parse(($LocalIp -replace '\.\d+$', '.255'))), 19998 # assumes a /24
$payload = [Text.Encoding]::ASCII.GetBytes('g2a-from-pc')
try {
    foreach ($lock in 'false', 'true') {
        $t = Get-Date
        while (-not (& $adb -s $Serial logcat -d -s flutter:I | Select-String "G2A09_LISTEN lock=$lock")) {
            if (((Get-Date) - $t).TotalSeconds -gt 120) { throw "no listening window (lock=$lock)" }
            Start-Sleep -Milliseconds 300
        }
        $sent = 0
        $end = (Get-Date).AddSeconds(9)
        while ((Get-Date) -lt $end) {
            [void]$udp.Send($payload, $payload.Length, $group)
            [void]$udp.Send($payload, $payload.Length, $bcast)
            [void]$udp.Send($payload, $payload.Length, $subnet)
            $sent++
            Start-Sleep -Milliseconds 100
        }
        "lock=${lock}: $sent packets sent to the multicast group, 255.255.255.255 and the /24 broadcast"
    }
} finally {
    $udp.Close()
}
$job | Wait-Job -Timeout 1800 | Receive-Job
Get-Job | Remove-Job -Force
