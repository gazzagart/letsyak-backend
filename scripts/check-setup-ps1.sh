#!/usr/bin/env bash
set -euo pipefail

# Checks setup.ps1 syntax on macOS/Linux. Uses local pwsh when installed.
# If pwsh is missing, Docker fallback uses Microsoft's amd64 image, which
# Docker Desktop can run on Apple Silicon through emulation.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target="${1:-setup.ps1}"

if [[ "$target" = /* ]]; then
    target_abs="$target"
else
    target_abs="$repo_root/$target"
fi

if [[ ! -f "$target_abs" ]]; then
    echo "Error: PowerShell file not found: $target" >&2
    exit 1
fi

case "$target_abs" in
    "$repo_root"/*)
        target_rel="${target_abs#"$repo_root"/}"
        ;;
    *)
        echo "Error: target must be inside $repo_root for Docker fallback." >&2
        exit 1
        ;;
esac

parser_command='
$path = $env:LETSYAK_PWSH_CHECK_TARGET
$tokens = $null
$parseErrors = $null
$resolved = (Resolve-Path -LiteralPath $path).Path
$null = [System.Management.Automation.Language.Parser]::ParseFile($resolved, [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count -gt 0) {
    foreach ($parseError in $parseErrors) {
        Write-Error ("{0}:{1}:{2}: {3}" -f $resolved, $parseError.Extent.StartLineNumber, $parseError.Extent.StartColumnNumber, $parseError.Message)
    }
    exit 1
}
Write-Host ("PowerShell syntax OK: {0}" -f $resolved)
'

if command -v pwsh >/dev/null 2>&1; then
    LETSYAK_PWSH_CHECK_TARGET="$target_abs" pwsh -NoLogo -NoProfile -NonInteractive -Command "$parser_command"
    exit 0
fi

if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    image="${POWERSHELL_DOCKER_IMAGE:-mcr.microsoft.com/powershell:7.4-alpine-3.20}"
        platform="${POWERSHELL_DOCKER_PLATFORM:-linux/amd64}"
        if docker run --rm \
        --platform "$platform" \
        -e LETSYAK_PWSH_CHECK_TARGET="/workspace/$target_rel" \
        -v "$repo_root:/workspace:ro" \
        -w /workspace \
        "$image" \
                pwsh -NoLogo -NoProfile -NonInteractive -Command "$parser_command"; then
                exit 0
        fi

        cat >&2 <<EOF

Docker could not run the PowerShell syntax check with:
    image:    $image
    platform: $platform

On Apple Silicon macOS, install native PowerShell for the fastest local check:
    brew install --cask powershell
    bash scripts/check-setup-ps1.sh

You can also override the Docker image/platform:
    POWERSHELL_DOCKER_IMAGE=<image> POWERSHELL_DOCKER_PLATFORM=<platform> bash scripts/check-setup-ps1.sh
EOF
        exit 1
fi

cat >&2 <<'EOF'
Error: PowerShell is not installed, and Docker is not available.

On Apple Silicon macOS you can either:
  brew install --cask powershell
  bash scripts/check-setup-ps1.sh

Or start Docker Desktop and rerun the same checker to use the amd64 Docker image through emulation.
EOF
exit 127