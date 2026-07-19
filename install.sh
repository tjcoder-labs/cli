#!/usr/bin/env bash
# install.sh — install `coder` (Coder CLI TUI) as a system CLI.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/tjcoder-labs/cli/main/install.sh | bash
#   ./install.sh                                 # local checkout, builds from source
#   PREFIX=/usr/local ./install.sh               # custom install prefix
#   VERSION=0.9.71 ./install.sh                  # specific release tag (with or without leading `v`)
#   REPO=tjcoder-labs/cli ./install.sh           # override repo
#
# Behavior:
#   1. Detects OS/arch.
#   2. If a prebuilt binary is available for this platform at the given
#      release tag, downloads it.
#   3. Otherwise builds from source: reuses a local checkout when run from
#      inside the repo, or clones the repo (requires `go` and `git`).
#   4. Installs to $PREFIX (default: ~/.local/bin) and prints PATH hints.
set -euo pipefail

REPO="${REPO:-tjcoder-labs/cli}"
VERSION="${VERSION:-}"
PREFIX="${PREFIX:-$HOME/.local/bin}"
BIN_NAME="coder"

# --- helpers --------------------------------------------------------------
log()  { printf '\033[1;36m==>\033[0m %s\n' "$*" >&2; }
warn() { printf '\033[1;33mwarn:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "required tool '$1' not found in PATH"; }

# --- platform detection ---------------------------------------------------
detect_platform() {
  local os arch
  case "$(uname -s)" in
    Linux)   os="linux" ;;
    Darwin)  os="darwin" ;;
    MINGW*|MSYS*|CYGWIN*) os="windows" ;;
    *) die "unsupported OS: $(uname -s)" ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64)  arch="amd64" ;;
    arm64|aarch64) arch="arm64" ;;
    *) die "unsupported arch: $(uname -m)" ;;
  esac
  echo "${os}-${arch}"
}

# --- locate a source checkout (for fallback build) ------------------------
find_source_dir() {
  # If invoked from inside the repo, reuse it.
  if [[ -f "$PWD/go.mod" ]] && [[ -f "$PWD/cmd/coder/main.go" ]]; then
    echo "$PWD"; return 0
  fi
  if [[ -f "$PWD/../go.mod" ]] && [[ -f "$PWD/../cmd/coder/main.go" ]]; then
    echo "$PWD/.."; return 0
  fi
  return 1
}

# --- download prebuilt binary --------------------------------------------
download_release() {
  local platform="$1" tmpdir="$2"
  if [[ -z "$VERSION" ]]; then
    log "resolving latest release for $REPO..."
    VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
              | sed -n 's/.*"tag_name": *"v\?\([^"]*\)".*/\1/p' | head -n1)
    [[ -n "$VERSION" ]] || die "could not determine latest release (set VERSION explicitly)"
    log "latest version: $VERSION"
  fi
  local asset="coder-${platform}.tar.gz"
  # Accept VERSION with or without a leading `v` (e.g. "0.9.71" or "v0.9.71").
  local tag="${VERSION#v}"
  [[ "$tag" != "$VERSION" ]] && VERSION="$tag" || VERSION="v$tag"
  local url="https://github.com/${REPO}/releases/download/${VERSION}/${asset}"
  log "downloading $url"
  if ! curl -fsSL -o "$tmpdir/$asset" "$url"; then
    return 1
  fi
  tar -xzf "$tmpdir/$asset" -C "$tmpdir"
  [[ -x "$tmpdir/coder" ]] || die "downloaded archive did not contain a 'coder' binary"
  echo "$tmpdir/coder"
}

# --- clone source (for `curl | bash` with no local checkout) --------------
clone_source() {
  local tmpdir="$1"
  local destdir="$tmpdir/src"
  need git
  local repo_url="https://github.com/${REPO}.git"
  if [[ -n "$VERSION" ]]; then
    # Try the exact tag first (with and without a leading `v`), then fall
    # back to a shallow clone of the default branch.
    local tag="v${VERSION#v}"
    log "cloning $repo_url @ $tag"
    if git clone --depth 1 --branch "$tag" "$repo_url" "$destdir" >/dev/null 2>&1 \
       || git clone --depth 1 --branch "${VERSION#v}" "$repo_url" "$destdir" >/dev/null 2>&1; then
      echo "$destdir"; return 0
    fi
    warn "tag $tag not found; cloning default branch"
  else
    log "cloning $repo_url"
  fi
  git clone --depth 1 "$repo_url" "$destdir" >/dev/null 2>&1 \
    || die "failed to clone $repo_url"
  echo "$destdir"
}

# --- build from source ----------------------------------------------------
build_from_source() {
  local srcdir="$1" tmpdir="$2"
  local version product author
  local pkg_version pkg_product pkg_author
  need go

  version="${VERSION:-}"
  product="Coder CLI"
  author="TJ Coder AI Labs"

  if [[ -f "$srcdir/package.json" ]]; then
    pkg_version=$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$srcdir/package.json" | head -n1)
    pkg_product=$(sed -n 's/.*"productName"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$srcdir/package.json" | head -n1)
    pkg_author=$(sed -n 's/.*"author"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$srcdir/package.json" | head -n1)

    if [[ -z "$version" && -n "$pkg_version" ]]; then
      version="$pkg_version"
    fi
    if [[ -n "$pkg_product" ]]; then
      product="$pkg_product"
    fi
    if [[ -n "$pkg_author" ]]; then
      author="$pkg_author"
    fi
  fi

  if [[ -z "$version" ]]; then
    version="dev"
  fi

  log "building from source in $srcdir"
  # cmd/coder/main.go uses //go:embed package.json, and go:embed forbids
  # path traversal, so stage a sibling copy of the root package.json in the
  # cmd dir for the embed to resolve (mirrors the Makefile).
  if [[ -f "$srcdir/package.json" && ! -f "$srcdir/cmd/coder/package.json" ]]; then
    cp "$srcdir/package.json" "$srcdir/cmd/coder/package.json"
  fi
  ( cd "$srcdir" && \
    go build -trimpath \
      -ldflags "-X 'main.version=$version' -X 'main.productName=$product' -X 'main.author=$author'" \
      -o "$tmpdir/coder" ./cmd/coder ) || die "build failed in $srcdir"
  [[ -x "$tmpdir/coder" ]] || die "build did not produce a 'coder' binary"
  echo "$tmpdir/coder"
}

# --- install --------------------------------------------------------------
install_binary() {
  local src="$1" prefix="$2"
  if ! mkdir -p "$prefix" 2>/dev/null; then
    die "cannot create $prefix — try PREFIX=\$HOME/.local/bin or run with sudo"
  fi
  if [[ ! -w "$prefix" ]]; then
    die "$prefix is not writable — try PREFIX=\$HOME/.local/bin or run with sudo"
  fi
  install -m 0755 "$src" "$prefix/$BIN_NAME"
  echo "$prefix/$BIN_NAME"
}

# --- PATH hint ------------------------------------------------------------
path_hint() {
  local prefix="$1"
  case ":$PATH:" in
    *":$prefix:"*) return 0 ;;
  esac
  warn "$prefix is not on your PATH"
  cat >&2 <<EOF

Add this to your shell rc (~/.bashrc, ~/.zshrc, etc.):

    export PATH="$prefix:\$PATH"

Then restart your shell or:

    export PATH="$prefix:\$PATH"

EOF
}

# --- main -----------------------------------------------------------------
main() {
  local platform tmpdir src installed
  platform=$(detect_platform)
  tmpdir=$(mktemp -d)
  trap 'rm -rf "${tmpdir:-}"' EXIT

  log "platform: $platform"
  log "prefix:   $PREFIX"

  # Try prebuilt release first, then a local checkout, then clone + build.
  if src=$(download_release "$platform" "$tmpdir" 2>/dev/null); then
    log "using prebuilt binary"
  elif srcdir=$(find_source_dir 2>/dev/null) && [[ -n "$srcdir" ]]; then
    warn "no prebuilt binary for $platform; building from local checkout"
    src=$(build_from_source "$srcdir" "$tmpdir")
  elif command -v go >/dev/null 2>&1 && command -v git >/dev/null 2>&1; then
    warn "no prebuilt binary for $platform and no local checkout; cloning source"
    srcdir=$(clone_source "$tmpdir")
    src=$(build_from_source "$srcdir" "$tmpdir")
  else
    die "no prebuilt binary available for $platform, and cannot build from source (need 'go' and 'git' on PATH)"
  fi

  installed=$(install_binary "$src" "$PREFIX")
  log "installed $installed"
  "$installed" --version || true
  path_hint "$PREFIX"
}

main "$@"
