# Installs the latest whatsapp-connect-mcp release for this machine's
# architecture, then runs `setup` (interactive QR pairing plus MCP client
# configuration). Runnable directly via:
#
#   irm https://raw.githubusercontent.com/idle-sync/whatsapp-connect-mcp/main/scripts/install.ps1 | iex
#
# Asset naming convention -- kept in sync with .github/workflows/release.yml,
# packaging/npm/install.js, and scripts/install.sh:
#   whatsapp-connect-mcp_<version>_<os>_<arch>[.exe]
# where <version> has no leading "v" (the release tag does).

$ErrorActionPreference = "Stop"

$Repo = "idle-sync/whatsapp-connect-mcp"
$BinName = "whatsapp-connect-mcp"
$InstallDir = if ($env:WHATSAPP_CONNECT_MCP_INSTALL_DIR) {
    $env:WHATSAPP_CONNECT_MCP_INSTALL_DIR
} else {
    Join-Path $env:LOCALAPPDATA "Programs\whatsapp-connect-mcp"
}

function Get-ReleaseArch {
    $arch = [System.Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture
    switch ($arch) {
        "X64" { return "amd64" }
        "Arm64" { return "arm64" }
        default {
            throw "unsupported architecture: $arch -- see release assets at https://github.com/$Repo/releases"
        }
    }
}

function Get-LatestTag {
    $uri = "https://api.github.com/repos/$Repo/releases/latest"
    $headers = @{ "User-Agent" = "whatsapp-connect-mcp-installer" }
    $release = Invoke-RestMethod -Uri $uri -Headers $headers
    return $release.tag_name
}

function Install-WhatsappConnectMcp {
    $arch = Get-ReleaseArch

    $tag = $env:WHATSAPP_CONNECT_MCP_VERSION
    if (-not $tag) {
        $tag = Get-LatestTag
    }
    if (-not $tag) {
        throw "could not determine the latest release version"
    }
    $version = $tag.TrimStart("v")

    $asset = "${BinName}_${version}_windows_${arch}.exe"
    $url = "https://github.com/$Repo/releases/download/$tag/$asset"

    Write-Host "installing $BinName $tag (windows/$arch)"
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

    $destPath = Join-Path $InstallDir "$BinName.exe"
    $tmpPath = "$destPath.download"
    try {
        Invoke-WebRequest -Uri $url -OutFile $tmpPath -UseBasicParsing
        Move-Item -Force $tmpPath $destPath
    } catch {
        Remove-Item -Force -ErrorAction SilentlyContinue $tmpPath
        throw "download failed: $url -- $_"
    }

    Write-Host "installed to $destPath"

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $pathEntries = $userPath -split ";" | Where-Object { $_ -ne "" }
    if ($InstallDir -notin $pathEntries) {
        Write-Host ""
        Write-Host "$InstallDir is not on your PATH. Add it, e.g.:"
        Write-Host "  [Environment]::SetEnvironmentVariable('Path', `"`$env:Path;$InstallDir`", 'User')"
        Write-Host "then open a new terminal, or run this once for the current session:"
        Write-Host "  `$env:Path = `"$InstallDir;`$env:Path`""
        Write-Host ""
        $env:Path = "$InstallDir;$env:Path"
    }

    & $destPath setup
}

Install-WhatsappConnectMcp
