#!/usr/bin/env bash
#
# hibot CLI installer.
#
# Usage:
#   tmp="$(mktemp -d)"
#   curl -fL --retry 8 --retry-delay 2 --retry-max-time 300 \
#     -o "$tmp/hibot-install.sh" \
#     https://raw.githubusercontent.com/volcengine/hiagent-go-sdk/main/scripts/install.sh
#   sh "$tmp/hibot-install.sh"
#
# Environment overrides:
#   HIBOT_VERSION   Version to install: 1.0.0, v1.0.0, or cmd/hibot/v1.0.0
#   HIBOT_PREFIX    Install prefix (default: /usr/local)
#   HIBOT_BIN_DIR   Binary destination directory (default: $HIBOT_PREFIX/bin)
#   HIBOT_REPO      GitHub repo (default: volcengine/hiagent-go-sdk)
#   HIBOT_REF       Git ref for source fallback when no release exists (default: main)
#   HIBOT_SOURCE_FALLBACK  Set to 0 to disable source fallback (default: 1)
#   GITHUB_TOKEN    Optional token used for GitHub downloads when provided
#
set -euo pipefail

REPO="${HIBOT_REPO:-volcengine/hiagent-go-sdk}"
PREFIX="${HIBOT_PREFIX:-/usr/local}"
BIN_DIR="${HIBOT_BIN_DIR:-$PREFIX/bin}"
VERSION="${HIBOT_VERSION:-}"
REF="${HIBOT_REF:-main}"
SOURCE_FALLBACK="${HIBOT_SOURCE_FALLBACK:-1}"

err() {
  echo "[hibot-install] error: $*" >&2
  exit 1
}

info() {
  echo "[hibot-install] $*" >&2
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || err "required command '$1' not found in PATH"
}

urlencode_tag() {
  printf '%s' "$1" | sed 's#/#%2F#g'
}

curl_retry_flags() {
  if curl --help all 2>/dev/null | grep -q -- '--retry-all-errors'; then
    printf '%s\n' "--retry" "5" "--retry-delay" "2" "--retry-max-time" "180" "--retry-all-errors"
  else
    printf '%s\n' "--retry" "5" "--retry-delay" "2" "--retry-max-time" "180"
  fi
}

download() {
  out="$1"
  url="$2"
  shift 2

  if [ -n "${GITHUB_TOKEN:-}" ]; then
    # shellcheck disable=SC2046
    curl -fSL $(curl_retry_flags) -H "Authorization: Bearer $GITHUB_TOKEN" "$@" -o "$out" "$url"
  else
    # shellcheck disable=SC2046
    curl -fSL $(curl_retry_flags) "$@" -o "$out" "$url"
  fi
}

resolve_latest_tag() {
  need_cmd git
  info "resolving latest cmd/hibot tag from git refs for github.com/$REPO ..."
  tags="$(
    git ls-remote --tags --refs "https://github.com/$REPO.git" 'refs/tags/cmd/hibot/v*' 2>/dev/null \
      | sed -E 's#^.*refs/tags/(cmd/hibot/v.*)$#\1#' \
      || true
  )"
  if [ -z "$tags" ]; then
    return 1
  fi
  if sort -V </dev/null >/dev/null 2>&1; then
    printf '%s\n' "$tags" | sort -V | tail -n 1
  else
    printf '%s\n' "$tags" | sort | tail -n 1
  fi
}

install_binary() {
  src="$1"

  mkdir -p "$BIN_DIR" 2>/dev/null || {
    info "cannot create $BIN_DIR without sudo; retrying with sudo"
    sudo mkdir -p "$BIN_DIR"
  }

  if [ -w "$BIN_DIR" ]; then
    install -m 0755 "$src" "$BIN_DIR/hibot"
  else
    info "$BIN_DIR is not writable; using sudo"
    sudo install -m 0755 "$src" "$BIN_DIR/hibot"
  fi

  info "installed: $BIN_DIR/hibot"
  "$BIN_DIR/hibot" version || true

  case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *) info "warning: $BIN_DIR is not in your PATH; add it to your shell profile." ;;
  esac
}

install_from_source() {
  src_ref="${1:-$REF}"
  need_cmd git
  need_cmd go

  info "falling back to source build from github.com/$REPO@$src_ref"
  git clone --depth=1 --branch "$src_ref" "https://github.com/$REPO.git" "$TMP/src"
  (cd "$TMP/src/cmd/hibot" && GOBIN="$TMP/bin" go install .)
  install_binary "$TMP/bin/hibot"
}

need_cmd curl
need_cmd grep
need_cmd install
need_cmd mkdir
need_cmd sed
need_cmd sort
need_cmd tar
need_cmd uname

OS_RAW="$(uname -s)"
ARCH_RAW="$(uname -m)"

case "$OS_RAW" in
  Linux) GOOS="linux" ;;
  Darwin) GOOS="darwin" ;;
  MINGW*|MSYS*|CYGWIN*) err "Windows shell detected; install on Windows by downloading the .zip release manually." ;;
  *) err "unsupported OS: $OS_RAW" ;;
esac

case "$ARCH_RAW" in
  x86_64|amd64) GOARCH="amd64" ;;
  arm64|aarch64) GOARCH="arm64" ;;
  *) err "unsupported architecture: $ARCH_RAW" ;;
esac

if [ -z "$VERSION" ]; then
  VERSION="$(resolve_latest_tag || true)"
  if [ -z "$VERSION" ]; then
    info "no cmd/hibot release found for github.com/$REPO"
    if [ "$SOURCE_FALLBACK" != "0" ]; then
      TMP="$(mktemp -d -t hibot-install.XXXXXX)"
      trap 'rm -rf "$TMP"' EXIT
      install_from_source "$REF"
      exit 0
    fi
    err "could not determine latest cmd/hibot release; publish cmd/hibot/v*, set HIBOT_VERSION, or set HIBOT_SOURCE_FALLBACK=1"
  fi
fi

case "$VERSION" in
  cmd/hibot/v*)
    TAG="$VERSION"
    BARE="${VERSION#cmd/hibot/v}"
    ;;
  cmd/hibot/*)
    TAG="$VERSION"
    BARE="${VERSION#cmd/hibot/}"
    BARE="${BARE#v}"
    ;;
  v*)
    TAG="cmd/hibot/$VERSION"
    BARE="${VERSION#v}"
    ;;
  *)
    TAG="cmd/hibot/v$VERSION"
    BARE="$VERSION"
    ;;
esac

TAG_PATH="$(urlencode_tag "$TAG")"
ARCHIVE="hibot_${BARE}_${GOOS}_${GOARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/$TAG_PATH/$ARCHIVE"
SUMS_URL="https://github.com/$REPO/releases/download/$TAG_PATH/checksums.txt"

TMP="$(mktemp -d -t hibot-install.XXXXXX)"
trap 'rm -rf "$TMP"' EXIT

info "downloading $URL"
if ! download "$TMP/$ARCHIVE" "$URL"; then
  info "release download failed"
  if [ "$SOURCE_FALLBACK" != "0" ]; then
    install_from_source "$TAG"
    exit 0
  fi
  err "download failed"
fi

if download "$TMP/checksums.txt" "$SUMS_URL" -s; then
  info "verifying SHA-256 checksum"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$TMP" && grep " $ARCHIVE\$" checksums.txt | sha256sum -c -) \
      || err "checksum mismatch"
  elif command -v shasum >/dev/null 2>&1; then
    (cd "$TMP" && grep " $ARCHIVE\$" checksums.txt | shasum -a 256 -c -) \
      || err "checksum mismatch"
  else
    info "warning: no sha256sum/shasum found, skipping checksum verification"
  fi
else
  info "warning: checksums.txt not available, skipping verification"
fi

tar -xzf "$TMP/$ARCHIVE" -C "$TMP"
install_binary "$TMP/hibot"
