# Creates the development key that signs medXfer's Android packages (WP-B.6 of the J6b plan),
# and app/android/key.properties, which the release build reads. Neither is ever committed
# (app/android/.gitignore). The publication key, and the Play Store, come with J8.
# Usage (PowerShell, from the repository root): scripts\android-dev-key.ps1
# The keystore goes to %USERPROFILE%\.medxfer\medxfer-dev.jks. Keep it: an installed APK only
# accepts an update signed with the same key. The script never replaces an existing one.
param(
    [string]$KeyStore = (Join-Path $env:USERPROFILE ".medxfer\medxfer-dev.jks"),
    [string]$Alias = "medxfer-dev"
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$props = Join-Path $root "app\android\key.properties"

if (Test-Path $KeyStore) {
    throw "$KeyStore exists already: it is kept (an update must be signed with the same key). Delete it by hand to start over."
}

# keytool: Android Studio's JDK, else the one on the PATH.
$keytool = Join-Path $env:ProgramFiles "Android\Android Studio\jbr\bin\keytool.exe"
if (-not (Test-Path $keytool)) { $keytool = (Get-Command keytool -ErrorAction Stop).Source }

# A random password: it only protects this development key, and lives in key.properties.
$bytes = New-Object byte[] 24
[System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
$password = [Convert]::ToBase64String($bytes) -replace '[+/=]', ''

New-Item -ItemType Directory -Force (Split-Path -Parent $KeyStore) | Out-Null
& $keytool -genkeypair -v -keystore $KeyStore -storetype PKCS12 -alias $Alias `
    -keyalg RSA -keysize 3072 -validity 10000 `
    -storepass $password -keypass $password `
    -dname "CN=medXfer development, O=medXfer" | Out-Null
if ($LASTEXITCODE -ne 0) { throw "keytool failed" }

# Gradle reads the path with forward slashes.
@(
    "storeFile=$($KeyStore -replace '\\', '/')",
    "storePassword=$password",
    "keyAlias=$Alias",
    "keyPassword=$password"
) | Set-Content -Encoding ascii $props

"Key created: $KeyStore"
"Settings written: $props (not committed)"
& $keytool -list -keystore $KeyStore -storepass $password -alias $Alias | Select-String "SHA-256|SHA256"
