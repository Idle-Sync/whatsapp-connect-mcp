#!/bin/sh
# Installs the latest whatsapp-connect-mcp release for this machine's OS
# and architecture, then runs `setup` (interactive QR pairing plus MCP
# client configuration). POSIX sh so it works unmodified under `sh`,
# `dash`, `bash`, and `zsh`. Intended to be piped from curl:
#
#   curl -fsSL https://raw.githubusercontent.com/idle-sync/whatsapp-connect-mcp/main/scripts/install.sh | sh
#
# Asset naming convention — kept in sync with .github/workflows/release.yml,
# packaging/npm/install.js, and scripts/install.ps1:
#   whatsapp-connect-mcp_<version>_<os>_<arch>[.exe]
# where <version> has no leading "v" (the release tag does).

set -eu

REPO="idle-sync/whatsapp-connect-mcp"
BIN_NAME="whatsapp-connect-mcp"
INSTALL_DIR="${WHATSAPP_CONNECT_MCP_INSTALL_DIR:-$HOME/.local/bin}"

log() {
  printf '%s\n' "$*" >&2
}

fail() {
  log "error: $*"
  exit 1
}

detect_os() {
  case "$(uname -s)" in
    Linux) echo linux ;;
    Darwin) echo darwin ;;
    *) fail "unsupported OS: $(uname -s) — see release assets at https://github.com/${REPO}/releases" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) echo amd64 ;;
    aarch64 | arm64) echo arm64 ;;
    *) fail "unsupported architecture: $(uname -m) — see release assets at https://github.com/${REPO}/releases" ;;
  esac
}

# fetch_json prints the body of a URL to stdout using whichever of
# curl/wget is available.
fetch() {
  url="$1"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- "$url"
  else
    fail "need curl or wget to download; neither was found on PATH"
  fi
}

download_to() {
  url="$1"
  dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "$dest" "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$dest" "$url"
  else
    fail "need curl or wget to download; neither was found on PATH"
  fi
}

latest_tag() {
  # Reads the "tag_name" field out of GitHub's latest-release API response
  # without requiring jq: matches the first "tag_name": "..." occurrence.
  fetch "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' \
    | head -n1 \
    | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/'
}

main() {
  os="$(detect_os)"
  arch="$(detect_arch)"

  tag="${WHATSAPP_CONNECT_MCP_VERSION:-}"
  if [ -z "$tag" ]; then
    tag="$(latest_tag)"
  fi
  [ -n "$tag" ] || fail "could not determine the latest release version"
  version="${tag#v}"

  asset="${BIN_NAME}_${version}_${os}_${arch}"
  url="https://github.com/${REPO}/releases/download/${tag}/${asset}"

  log "installing ${BIN_NAME} ${tag} (${os}/${arch})"
  mkdir -p "$INSTALL_DIR"
  tmp_file="$(mktemp "${INSTALL_DIR}/.${BIN_NAME}.XXXXXX")"
  trap 'rm -f "$tmp_file"' EXIT

  download_to "$url" "$tmp_file" || fail "download failed: $url"
  chmod +x "$tmp_file"
  mv "$tmp_file" "${INSTALL_DIR}/${BIN_NAME}"
  trap - EXIT

  log "installed to ${INSTALL_DIR}/${BIN_NAME}"

  case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *)
      log ""
      log "${INSTALL_DIR} is not on your PATH. Add it, e.g.:"
      log "  echo 'export PATH=\"${INSTALL_DIR}:\$PATH\"' >> ~/.profile"
      log "then restart your shell, or run this once for the current shell:"
      log "  export PATH=\"${INSTALL_DIR}:\$PATH\""
      log ""
      ;;
  esac

  "${INSTALL_DIR}/${BIN_NAME}" setup
}

main "$@"
