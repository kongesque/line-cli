$ErrorActionPreference = "Stop"

$repository = "kongesque/line-cli"
$downloadBase = if ($env:LINE_CLI_DOWNLOAD_BASE) {
    $env:LINE_CLI_DOWNLOAD_BASE.TrimEnd("/")
} else {
    "https://github.com/$repository/releases/latest/download"
}
$installDir = if ($env:LINE_CLI_INSTALL_DIR) {
    $env:LINE_CLI_INSTALL_DIR
} else {
    Join-Path $env:LOCALAPPDATA "line-cli\bin"
}

$architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
$arch = switch ($architecture) {
    "X64" { "amd64" }
    "Arm64" { "arm64" }
    default { throw "Unsupported CPU architecture: $architecture" }
}

$archive = "line-windows-$arch.tar.gz"
$temporaryDir = Join-Path ([System.IO.Path]::GetTempPath()) ("line-cli-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $temporaryDir -ErrorAction Stop | Out-Null

try {
    $archivePath = Join-Path $temporaryDir $archive
    $checksumsPath = Join-Path $temporaryDir "SHA256SUMS.txt"

    Write-Host "Downloading LINE CLI for windows/$arch..."
    Invoke-WebRequest -UseBasicParsing -Uri "$downloadBase/$archive" -OutFile $archivePath
    Invoke-WebRequest -UseBasicParsing -Uri "$downloadBase/SHA256SUMS.txt" -OutFile $checksumsPath

    $matches = @(Get-Content $checksumsPath | ForEach-Object {
        $parts = $_ -split "\s+", 2
        if ($parts.Count -eq 2 -and $parts[1].TrimStart("*") -eq $archive) {
            $parts[0]
        }
    })
    if ($matches.Count -ne 1) {
        throw "Could not find one checksum for $archive."
    }

    $actual = (Get-FileHash $archivePath -Algorithm SHA256).Hash
    if ($actual -ine $matches[0]) {
        throw "Archive checksum verification failed."
    }

    $extractedDir = Join-Path $temporaryDir "extracted"
    New-Item -ItemType Directory -Path $extractedDir -ErrorAction Stop | Out-Null
    tar.exe -xzf $archivePath -C $extractedDir
    if ($LASTEXITCODE -ne 0) {
        throw "Could not extract the release archive."
    }

    $source = Join-Path $extractedDir "line.exe"
    if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
        throw "The release archive does not contain line.exe."
    }

    New-Item -ItemType Directory -Force -Path $installDir -ErrorAction Stop | Out-Null
    Copy-Item -Force -LiteralPath $source -Destination (Join-Path $installDir "line.exe")

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $pathEntries = @($userPath -split ";" | Where-Object { $_ })
    if (-not ($pathEntries | Where-Object { $_.TrimEnd("\") -ieq $installDir.TrimEnd("\") })) {
        $newUserPath = if ($userPath) { "$installDir;$userPath" } else { $installDir }
        [Environment]::SetEnvironmentVariable("Path", $newUserPath, "User")
    }
    if (-not (($env:Path -split ";") | Where-Object { $_.TrimEnd("\") -ieq $installDir.TrimEnd("\") })) {
        $env:Path = "$installDir;$env:Path"
    }

    Write-Host "Installed LINE CLI: $(Join-Path $installDir 'line.exe')"
    Write-Host "Ready: line help"
} finally {
    Remove-Item -LiteralPath $temporaryDir -Recurse -Force -ErrorAction SilentlyContinue
}
