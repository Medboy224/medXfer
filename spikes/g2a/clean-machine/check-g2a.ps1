# medXfer G2a, G2A-08: runs the test app on a clean Windows PC (no Go, MinGW, NDK, Visual Studio).
# Nothing is installed or changed on the PC: the app runs from this folder and writes its results
# next to this script (resultat-g2a.txt, resultat-g2a.json, journal-g2a.txt).
$ErrorActionPreference = 'Continue'
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$out = Join-Path $here 'resultat-g2a.txt'
$lines = New-Object System.Collections.Generic.List[string]
function Say([string]$s) { $lines.Add($s); Write-Host $s }

Say "medXfer G2a - essai sur machine propre - $(Get-Date -Format 'yyyy-MM-dd HH:mm')"
$os = Get-CimInstance Win32_OperatingSystem
Say "Windows : $($os.Caption) $($os.Version) (build $($os.BuildNumber)), $($os.OSArchitecture)"
Say "Processeur : $((Get-CimInstance Win32_Processor | Select-Object -First 1).Name)"
Say ("Memoire : {0:N1} Go" -f ($os.TotalVisibleMemorySize / 1MB))

# Is the PC clean? Development tools found on the PATH or installed.
foreach ($t in 'go', 'gcc', 'clang', 'flutter', 'dart', 'cl', 'x86_64-w64-mingw32-gcc') {
    $c = Get-Command $t -ErrorAction SilentlyContinue
    Say ("Outil {0,-24}: {1}" -f $t, $(if ($c) { $c.Source } else { 'absent' }))
}
Say ("Visual Studio installe : {0}" -f ((Test-Path "${env:ProgramFiles(x86)}\Microsoft Visual Studio") -or (Test-Path "$env:ProgramFiles\Microsoft Visual Studio")))
foreach ($d in 'vcruntime140.dll', 'vcruntime140_1.dll', 'msvcp140.dll') {
    Say ("Runtime VC++ du systeme, {0,-19}: {1}" -f $d, $(if (Test-Path (Join-Path $env:SystemRoot "System32\$d")) { 'present' } else { 'absent' }))
}

# Run the quick test suite of the app.
$json = Join-Path $here 'resultat-g2a.json'
Remove-Item $json -ErrorAction SilentlyContinue
$env:MEDXFER_G2A_AUTORUN = 'quick'
$env:MEDXFER_G2A_RESULT = $json
$exe = Join-Path $here 'app\medxfer_g2a.exe'
Say "Lancement : $exe"
$sw = [Diagnostics.Stopwatch]::StartNew()
$p = Start-Process $exe -PassThru -RedirectStandardOutput (Join-Path $here 'journal-g2a.txt') -RedirectStandardError (Join-Path $here 'erreurs-g2a.txt')
$null = $p.Handle # Windows PowerShell 5.1 only reports ExitCode if the handle was opened
Start-Sleep 3
# Which copy of each DLL did the process load? The VC++ runtime must come from the app folder.
try {
    $p.Refresh()
    foreach ($m in $p.Modules) {
        if ($m.ModuleName -match '^(vcruntime140|vcruntime140_1|msvcp140|medxfer|flutter_windows)\.dll$') { Say ("DLL chargee : {0}" -f $m.FileName) }
    }
} catch {
    Say "Liste des DLL chargees indisponible : $($_.Exception.Message)"
}
if (-not $p.WaitForExit(600000)) { $p.Kill(); Say 'ECHEC : application toujours en cours apres 10 min' }
$code = $p.ExitCode
$meaning = switch ($code) { 0 { 'tout reussi' } 1 { 'un test a echoue' } -1073741515 { 'DLL MANQUANTE (0xC0000135)' } -1073741701 { 'DLL incompatible (0xC000007B)' } default { 'inattendu' } }
Say ("Code de sortie : {0} ({1}), duree {2:N0} s" -f $code, $meaning, $sw.Elapsed.TotalSeconds)

if (Test-Path $json) {
    $r = Get-Content $json -Raw | ConvertFrom-Json
    foreach ($prop in $r.PSObject.Properties) {
        $v = $prop.Value
        if ($v -is [psobject] -and $v.PSObject.Properties['pass']) { Say ("{0,-28}: {1}" -f $prop.Name, $(if ($v.pass) { 'REUSSI' } else { 'ECHEC' })) }
    }
    Say ("Demarrage, de main() au premier appel du coeur : {0:N1} ms" -f $r.cold_start_ms_main_to_first_ping)
    Say "RESULTAT GLOBAL : $(if ($r.pass) { 'REUSSI' } else { 'ECHEC' })"
} else {
    Say 'ECHEC : pas de fichier de resultat (voir journal-g2a.txt et erreurs-g2a.txt)'
}
$lines | Set-Content -Path $out -Encoding UTF8
Write-Host ''
Write-Host "Resultats ecrits dans $out"
