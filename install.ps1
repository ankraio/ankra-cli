# Ankra CLI installer for Windows.
#
#   irm https://ankra.ai/install.ps1 | iex
#
# Installs ankra.exe into %LOCALAPPDATA%\Programs\ankra, adds that directory
# to your user PATH and to this session, and then runs `ankra onboard`, which
# signs you up or in and sets up only what your account still needs.
#
# Environment variables:
#   ANKRA_VERSION=vX.Y.Z    install this release instead of the latest
#   ANKRA_INSTALL_DIR=DIR   install into DIR
#   ANKRA_NO_ONBOARD=1      install only, do not run `ankra onboard`
#
# It downloads ankra-cli-windows-<arch>.exe and its .sha256 from the GitHub
# release and refuses to install when the checksum is missing or does not
# match. It never asks a question and never needs administrator rights.
#
# Test hook: ANKRA_DOWNLOAD_BASE replaces the release download URL (a file://
# URL works).
#
# Everything runs inside Install-AnkraCli, so a download cut short runs
# nothing, and settings such as $ErrorActionPreference stay inside it rather
# than changing the PowerShell session that ran `iex`. Errors are thrown, not
# exited, so the window stays open.
#
# Not executed in CI: the release workflow uploads it next to install.sh.

function Install-AnkraCli {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'

    $releases = 'https://github.com/ankraio/ankra-cli/releases'

    $version = "$env:ANKRA_VERSION".Trim()
    if ($version -eq 'latest') { $version = '' }
    if ($version -ne '') {
        if ($version -notmatch '^[A-Za-z0-9._-]+$') { throw "ANKRA_VERSION is not a release name: $version" }
        if ($version -match '^[0-9]') { $version = "v$version" }
    }

    if ("$env:ANKRA_DOWNLOAD_BASE" -ne '') {
        $base = "$env:ANKRA_DOWNLOAD_BASE".TrimEnd('/')
    } elseif ($version -ne '') {
        $base = "$releases/download/$version"
    } else {
        $base = "$releases/latest/download"
    }

    # A 32-bit PowerShell on 64-bit Windows reports x86 and names the real
    # architecture in PROCESSOR_ARCHITEW6432.
    $machine = $env:PROCESSOR_ARCHITECTURE
    if ($machine -eq 'x86' -and $env:PROCESSOR_ARCHITEW6432) { $machine = $env:PROCESSOR_ARCHITEW6432 }
    switch ($machine) {
        'AMD64' { $arch = 'amd64' }
        'ARM64' { $arch = 'arm64' }
        default { throw "The Ankra CLI is built for amd64 and arm64 Windows, not $machine. See $releases" }
    }

    if ("$env:ANKRA_INSTALL_DIR" -ne '') {
        $installDir = $env:ANKRA_INSTALL_DIR
    } else {
        if (-not $env:LOCALAPPDATA) { throw 'LOCALAPPDATA is not set; choose a directory with ANKRA_INSTALL_DIR' }
        $installDir = Join-Path $env:LOCALAPPDATA 'Programs\ankra'
    }
    $installDir = [IO.Path]::GetFullPath($installDir)
    $target = Join-Path $installDir 'ankra.exe'

    # Windows PowerShell 5.1 does not offer TLS 1.2 by default; GitHub needs it.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $asset = "ankra-cli-windows-$arch.exe"
    $workdir = Join-Path ([IO.Path]::GetTempPath()) ("ankra-install-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $workdir | Out-Null
    try {
        $binary = Join-Path $workdir $asset
        $checksumFile = "$binary.sha256"

        $label = if ($version -ne '') { "$asset $version" } else { $asset }
        Write-Host "Downloading $label from $base"
        try {
            Save-AnkraDownload "$base/$asset" $binary
        } catch {
            throw "Could not download $base/$asset ($($_.Exception.Message))"
        }
        if ((Get-Item $binary).Length -eq 0) { throw "The download of $base/$asset is empty" }

        try {
            Save-AnkraDownload "$base/$asset.sha256" $checksumFile
        } catch {
            throw "The release has no checksum at $base/$asset.sha256, so the download cannot be verified; refusing to install"
        }
        $expected = ''
        $firstLine = Get-Content -Path $checksumFile -TotalCount 1
        if ($firstLine) { $expected = ("$firstLine".Trim() -split '\s+')[0].ToLowerInvariant() }
        if ($expected -notmatch '^[0-9a-f]{64}$') {
            throw "The checksum file at $base/$asset.sha256 is not a SHA-256 checksum, so the download cannot be verified; refusing to install"
        }
        $actual = (Get-FileHash -Algorithm SHA256 -Path $binary).Hash.ToLowerInvariant()
        if ($actual -ne $expected) {
            throw "Checksum mismatch for the downloaded binary (expected $expected, got $actual); refusing to install"
        }
        Write-Host 'Checksum verified.'

        # Copy under a temporary name next to the target, then rename. A
        # running ankra.exe cannot be overwritten but can be renamed, so the
        # old one moves aside first and is removed when nothing holds it.
        New-Item -ItemType Directory -Path $installDir -Force | Out-Null
        $staged = Join-Path $installDir ".ankra.install.$PID.exe"
        $previous = "$target.old"
        Copy-Item -Path $binary -Destination $staged -Force
        try {
            if (Test-Path -LiteralPath $target) {
                Remove-Item -LiteralPath $previous -Force -ErrorAction SilentlyContinue
                Move-Item -LiteralPath $target -Destination $previous -Force
            }
            Move-Item -LiteralPath $staged -Destination $target
        } catch {
            if (-not (Test-Path -LiteralPath $target) -and (Test-Path -LiteralPath $previous)) {
                Move-Item -LiteralPath $previous -Destination $target -ErrorAction SilentlyContinue
            }
            Remove-Item -LiteralPath $staged -Force -ErrorAction SilentlyContinue
            throw "Cannot install into ${installDir}: $($_.Exception.Message)"
        }
        Remove-Item -LiteralPath $previous -Force -ErrorAction SilentlyContinue
    } finally {
        Remove-Item -LiteralPath $workdir -Recurse -Force -ErrorAction SilentlyContinue
    }

    $installedVersion = (& $target --version | Out-String).Trim()
    if ($LASTEXITCODE -ne 0) { throw "The installed ankra at $target does not run: $installedVersion" }

    $pathChanged = Add-AnkraToUserPath $installDir
    if (-not (Test-AnkraPathContains $env:Path $installDir)) {
        $env:Path = "$installDir;$env:Path"
    }

    Write-Host ''
    Write-Host "Installed $installedVersion to $target"
    if ($pathChanged) {
        Write-Host "Added $installDir to your user PATH. Terminals opened from now on find ankra; this one already does."
    }
    $others = @(Get-Command ankra -CommandType Application -All -ErrorAction SilentlyContinue |
        Where-Object { $_.Source -and ([IO.Path]::GetFullPath($_.Source) -ne $target) })
    foreach ($other in $others) {
        Write-Host "Note: another ankra is installed at $($other.Source); remove it so it does not run instead of this one."
    }

    if ("$env:ANKRA_NO_ONBOARD" -match '^(1|true|yes|on)$') {
        Write-Host ''
        Write-Host 'Next, sign up or sign in and set up with:'
        Write-Host '  ankra onboard'
        return 0
    }
    Write-Host ''
    Write-Host 'Starting onboarding: ankra onboard'
    Write-Host ''
    # Start-Process -NoNewWindow gives ankra this console as it is. Called as
    # `& $target` inside a function its output would be piped, and onboarding
    # would decide nobody is at the terminal. Reading Handle before waiting
    # keeps ExitCode available afterwards.
    $onboarding = Start-Process -FilePath $target -ArgumentList 'onboard' -NoNewWindow -PassThru
    $null = $onboarding.Handle
    $onboarding.WaitForExit()
    return $onboarding.ExitCode
}

function Save-AnkraDownload([string]$Url, [string]$Destination) {
    if ($Url -like 'file://*') {
        Copy-Item -LiteralPath ([Uri]$Url).LocalPath -Destination $Destination -Force
        return
    }
    Invoke-WebRequest -Uri $Url -OutFile $Destination -UseBasicParsing
}

function Test-AnkraPathContains([string]$PathValue, [string]$Directory) {
    $wanted = $Directory.TrimEnd('\')
    foreach ($entry in "$PathValue".Split(';')) {
        if ($entry.TrimEnd('\') -eq $wanted) { return $true }
    }
    return $false
}

# Add-AnkraToUserPath puts the directory first on the user PATH, read and
# written through the registry so entries such as %USERPROFILE%\bin keep
# their unexpanded form. Returns $true when it changed anything.
function Add-AnkraToUserPath([string]$Directory) {
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
    if ($null -eq $key) { $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment') }
    try {
        $current = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $expanded = [Environment]::ExpandEnvironmentVariables($current)
        if ((Test-AnkraPathContains $current $Directory) -or (Test-AnkraPathContains $expanded $Directory)) {
            return $false
        }
        $updated = if ($current.Trim(';') -eq '') { $Directory } else { "$Directory;$($current.TrimStart(';'))" }
        $key.SetValue('Path', $updated, [Microsoft.Win32.RegistryValueKind]::ExpandString)
    } finally {
        $key.Close()
    }
    # Setting any user variable through .NET broadcasts WM_SETTINGCHANGE, so
    # Explorer and terminals started from it pick up the new PATH.
    [Environment]::SetEnvironmentVariable('ANKRA_INSTALLER', '1', 'User')
    [Environment]::SetEnvironmentVariable('ANKRA_INSTALLER', $null, 'User')
    return $true
}

# The status is onboarding's. Run as a file, the script exits with it; under
# `irm | iex` it lands in $LASTEXITCODE instead, because `exit` would close
# the PowerShell window.
$ankraInstallStatus = Install-AnkraCli
$global:LASTEXITCODE = $ankraInstallStatus
if ($PSCommandPath) { exit $ankraInstallStatus }
