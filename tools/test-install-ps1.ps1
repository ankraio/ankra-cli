# Tests install.ps1 with PowerShell on Linux or macOS (CI runs it on the
# ubuntu runner, which has pwsh), against fake releases in a temporary
# directory:
#
#   pwsh -NoProfile -File tools/test-install-ps1.ps1
#
# It loads install.ps1's functions without running it, replaces the one step
# that only exists on Windows (writing the user PATH to the registry) with a
# recorder, and runs Install-AnkraCli once per case. The fake ankra.exe is a
# shell script, which is why this runs on a Unix host. What it cannot cover:
# Windows PowerShell 5.1, the registry write and the console handed to
# `ankra onboard` on Windows.

$ErrorActionPreference = 'Stop'
if ($IsWindows) { throw 'This test builds fake binaries as shell scripts; run it on Linux or macOS.' }

$repository = Split-Path -Parent $PSScriptRoot
$source = Get-Content -Raw -Path (Join-Path $repository 'install.ps1')
$invocation = $source.IndexOf('$ankraInstallStatus = Install-AnkraCli')
if ($invocation -lt 0) { throw 'install.ps1 no longer ends by calling Install-AnkraCli; update this test' }
. ([ScriptBlock]::Create($source.Substring(0, $invocation)))
$realDownload = ${function:Save-AnkraDownload}

$sandbox = Join-Path ([IO.Path]::GetTempPath()) ("ankra-install-ps1-test-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $sandbox | Out-Null
$script:failures = 0
$script:checks = 0
$script:case = ''
$script:pathAdded = @()

function Check([bool]$Condition, [string]$Message) {
    $script:checks++
    if (-not $Condition) {
        $script:failures++
        Write-Host "FAIL [$script:case] $Message"
    }
}

function Add-AnkraToUserPath([string]$Directory) {
    $script:pathAdded += $Directory
    return $true
}

function New-FakeRelease([string]$Name, [string]$Checksum, [string]$Version = 'v9.9.9-test') {
    $directory = Join-Path $sandbox $Name
    New-Item -ItemType Directory -Path $directory | Out-Null
    foreach ($arch in 'amd64', 'arm64') {
        $asset = Join-Path $directory "ankra-cli-windows-$arch.exe"
        $script = @(
            '#!/bin/sh'
            'case "$1" in'
            "--version) echo 'ankra version $Version' ;;"
            'onboard) echo onboarded >> "$FAKE_ONBOARD_LOG"; exit "${FAKE_ONBOARD_EXIT:-0}" ;;'
            'esac'
        ) -join "`n"
        [IO.File]::WriteAllText($asset, "$script`n")
        & chmod 0755 $asset
        $hash = (Get-FileHash -Algorithm SHA256 -Path $asset).Hash.ToLowerInvariant()
        switch ($Checksum) {
            'good' { [IO.File]::WriteAllText("$asset.sha256", "$hash  ankra-cli-windows-$arch.exe`n") }
            'bad' { [IO.File]::WriteAllText("$asset.sha256", ('0' * 64) + "  ankra-cli-windows-$arch.exe`n") }
            'malformed' { [IO.File]::WriteAllText("$asset.sha256", "<html>Not Found</html>`n") }
            'missing' { }
        }
    }
    return "file://$directory"
}

# Start-Case clears every variable the installer reads and gives the case its
# own LOCALAPPDATA.
function Start-Case([string]$Name) {
    $script:case = $Name
    $script:pathAdded = @()
    foreach ($variable in 'ANKRA_VERSION', 'ANKRA_INSTALL_DIR', 'ANKRA_NO_ONBOARD', 'ANKRA_DOWNLOAD_BASE', 'FAKE_ONBOARD_EXIT', 'PROCESSOR_ARCHITEW6432') {
        Remove-Item -Path "Env:$variable" -ErrorAction SilentlyContinue
    }
    $env:PROCESSOR_ARCHITECTURE = 'AMD64'
    $env:LOCALAPPDATA = Join-Path $sandbox ("localappdata-" + ($Name -replace '[^A-Za-z0-9]', '-'))
    $env:FAKE_ONBOARD_LOG = Join-Path $env:LOCALAPPDATA 'onboard.log'
    New-Item -ItemType Directory -Path $env:LOCALAPPDATA -Force | Out-Null
    Set-Item -Path function:script:Save-AnkraDownload -Value $realDownload
}

function Get-Target { Join-Path $env:LOCALAPPDATA 'Programs/ankra/ankra.exe' }

function Get-OnboardRuns {
    if (Test-Path $env:FAKE_ONBOARD_LOG) { return @(Get-Content $env:FAKE_ONBOARD_LOG).Count }
    return 0
}

# Invoke-Install runs the installer and returns its status, or the message
# it threw.
function Invoke-Install {
    try {
        $status = Install-AnkraCli 6>$null
        return [pscustomobject]@{ Status = $status; Thrown = '' }
    } catch {
        return [pscustomobject]@{ Status = $null; Thrown = $_.Exception.Message }
    }
}

# Use-RecordedDownloads replaces downloads with a recorder that fails, to
# see which URL the installer asks for without a network.
function Use-RecordedDownloads {
    $script:downloads = @()
    Set-Item -Path function:script:Save-AnkraDownload -Value {
        param([string]$Url, [string]$Destination)
        $script:downloads += $Url
        throw 'recorded'
    }
}

try {
    $good = New-FakeRelease 'good' 'good'
    $bad = New-FakeRelease 'bad' 'bad'
    $missing = New-FakeRelease 'missing' 'missing'
    $malformed = New-FakeRelease 'malformed' 'malformed'

    Start-Case 'installs, adds to PATH and onboards'
    $env:ANKRA_DOWNLOAD_BASE = $good
    $result = Invoke-Install
    Check ($result.Thrown -eq '') "threw: $($result.Thrown)"
    Check ($result.Status -eq 0) "status $($result.Status), want 0"
    Check (Test-Path (Get-Target)) 'ankra.exe was not installed'
    Check ((Get-OnboardRuns) -eq 1) "onboarding ran $(Get-OnboardRuns) times, want 1"
    $installDir = Split-Path -Parent (Get-Target)
    Check (($script:pathAdded -join ';') -eq $installDir) "user PATH got '$($script:pathAdded -join ';')', want $installDir"
    Check (Test-AnkraPathContains $env:Path $installDir) 'the session PATH does not hold the install directory'

    Start-Case 'the status is onboarding''s'
    $env:ANKRA_DOWNLOAD_BASE = $good
    $env:FAKE_ONBOARD_EXIT = '7'
    $result = Invoke-Install
    Check ($result.Status -eq 7) "status $($result.Status), want 7"

    Start-Case 'ANKRA_NO_ONBOARD skips onboarding'
    $env:ANKRA_DOWNLOAD_BASE = $good
    $env:ANKRA_NO_ONBOARD = '1'
    $result = Invoke-Install
    Check ($result.Status -eq 0) "status $($result.Status), want 0"
    Check (Test-Path (Get-Target)) 'ankra.exe was not installed'
    Check ((Get-OnboardRuns) -eq 0) 'onboarding ran'

    Start-Case 'a reinstall replaces the binary and leaves nothing behind'
    $env:ANKRA_DOWNLOAD_BASE = $good
    $env:ANKRA_NO_ONBOARD = '1'
    $null = Invoke-Install
    [IO.File]::WriteAllText((Get-Target), "#!/bin/sh`necho old`n")
    $result = Invoke-Install
    Check ($result.Status -eq 0) "status $($result.Status), want 0 ($($result.Thrown))"
    Check ((Get-Content -Raw (Get-Target)) -match 'v9.9.9-test') 'the old binary was not replaced'
    $leftovers = @(Get-ChildItem -Force -Path (Split-Path -Parent (Get-Target)) | Where-Object { $_.Name -ne 'ankra.exe' })
    Check ($leftovers.Count -eq 0) "left behind: $($leftovers.Name -join ', ')"

    Start-Case 'ANKRA_INSTALL_DIR is honoured'
    $env:ANKRA_DOWNLOAD_BASE = $good
    $env:ANKRA_NO_ONBOARD = '1'
    $env:ANKRA_INSTALL_DIR = Join-Path $env:LOCALAPPDATA 'custom dir'
    $result = Invoke-Install
    Check ($result.Status -eq 0) "status $($result.Status), want 0 ($($result.Thrown))"
    Check (Test-Path (Join-Path $env:ANKRA_INSTALL_DIR 'ankra.exe')) 'ankra.exe is not in ANKRA_INSTALL_DIR'

    foreach ($refusal in @(
            @{ Name = 'a checksum mismatch is refused'; Base = $bad; Message = 'Checksum mismatch' },
            @{ Name = 'a missing checksum is refused'; Base = $missing; Message = 'refusing to install' },
            @{ Name = 'a checksum file that is not a checksum is refused'; Base = $malformed; Message = 'is not a SHA-256 checksum' })) {
        Start-Case $refusal.Name
        $env:ANKRA_DOWNLOAD_BASE = $refusal.Base
        $result = Invoke-Install
        Check ($result.Thrown -like "*$($refusal.Message)*") "threw '$($result.Thrown)', want '$($refusal.Message)'"
        Check (-not (Test-Path (Get-Target))) 'ankra.exe was installed'
        Check ((Get-OnboardRuns) -eq 0) 'onboarding ran'
    }

    Start-Case 'the latest release by default'
    Use-RecordedDownloads
    $null = Invoke-Install
    Check ($script:downloads[0] -eq 'https://github.com/ankraio/ankra-cli/releases/latest/download/ankra-cli-windows-amd64.exe') "downloaded $($script:downloads[0])"

    Start-Case 'ANKRA_VERSION pins a release and gains its v'
    $env:ANKRA_VERSION = '1.2.3'
    Use-RecordedDownloads
    $null = Invoke-Install
    Check ($script:downloads[0] -eq 'https://github.com/ankraio/ankra-cli/releases/download/v1.2.3/ankra-cli-windows-amd64.exe') "downloaded $($script:downloads[0])"

    Start-Case 'ARM64 gets the arm64 binary'
    $env:PROCESSOR_ARCHITECTURE = 'ARM64'
    Use-RecordedDownloads
    $null = Invoke-Install
    Check ($script:downloads[0] -like '*/ankra-cli-windows-arm64.exe') "downloaded $($script:downloads[0])"

    Start-Case '32-bit PowerShell on 64-bit Windows gets amd64'
    $env:PROCESSOR_ARCHITECTURE = 'x86'
    $env:PROCESSOR_ARCHITEW6432 = 'AMD64'
    Use-RecordedDownloads
    $null = Invoke-Install
    Check ($script:downloads[0] -like '*/ankra-cli-windows-amd64.exe') "downloaded $($script:downloads[0])"

    Start-Case 'a 32-bit Windows is refused'
    $env:PROCESSOR_ARCHITECTURE = 'x86'
    $result = Invoke-Install
    Check ($result.Thrown -like '*built for amd64 and arm64*') "threw '$($result.Thrown)'"
} finally {
    Remove-Item -LiteralPath $sandbox -Recurse -Force -ErrorAction SilentlyContinue
}

if ($script:failures -gt 0) {
    Write-Host "install.ps1: $script:failures of $script:checks checks failed"
    exit 1
}
Write-Host "install.ps1: all $script:checks checks passed"
