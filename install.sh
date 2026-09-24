#!/usr/bin/env bash
# install.sh — install `coder` (Coder CLI TUI) as a system CLI.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/tjcoder-labs/cli/main/install.sh | bash
#   ./install.sh                                 # local checkout, builds from source
#   INSTALL_DIR=/usr/local/bin ./install.sh      # custom install directory
#   VERSION=0.9.71 ./install.sh                  # specific release tag (with or without leading `v`)
#   REPO=tjcoder-labs/cli ./install.sh           # override repo
#   INSTALL_OLLAMA=1 ./install.sh                 # also install Ollama and pull a default model
#   SKIP_OLLAMA=1 ./install.sh                    # skip Ollama detection entirely
#   OLLAMA_MODEL=minimax-m3:cloud ./install.sh    # pull a specific model with Ollama
#
# Behavior:
#   1. Detects OS/arch.
#   2. If a prebuilt binary is available for this platform at the given
#      release tag, downloads it.
#   3. Otherwise builds from source: reuses a local checkout when run from
#      inside the repo, or clones the repo (requires `go` and `git`).
#   4. Installs to $INSTALL_DIR (default: ~/.local/bin, or $PREFIX/bin on
#      Termux) and prints PATH hints.
set -euo pipefail

REPO="${REPO:-tjcoder-labs/cli}"
VERSION="${VERSION:-}"
BIN_NAME="coder"

# --- install directory resolution ----------------------------------------
# Note: do NOT use a variable named PREFIX as our knob — on Termux, PREFIX
# is a *standard environment variable* pointing at the Termux filesystem
# root (/data/data/com.termux/files/usr), which previously caused the
# binary to land directly in $PREFIX instead of $PREFIX/bin.
# We accept INSTALL_DIR as an override, and auto-pick a sensible default.
is_termux_env() {
  [[ -n "${TERMUX_VERSION:-}" ]] || [[ -n "${TERMUX_MAIN_PACKAGE_FORMAT:-}" ]] \
    || [[ "${PREFIX:-}" == *"com.termux"* ]]
}

if [[ -n "${INSTALL_DIR:-}" ]]; then
  INSTALL_DIR="$INSTALL_DIR"
elif is_termux_env; then
  # In Termux the canonical user bin dir is $PREFIX/bin, which is already
  # on PATH for any pkg-installed tools.
  INSTALL_DIR="${PREFIX:-/data/data/com.termux/files/usr}/bin"
else
  INSTALL_DIR="$HOME/.local/bin"
fi

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

# Detect if running in Termux
is_termux() {
  if [[ -n "${TERMUX_VERSION:-}" ]]; then
    return 0
  fi
  return 1
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
    # Use -f to fail on 404, but we handle it via the return code of the subshell
    local latest_json
    if ! latest_json=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null); then
      warn "could not resolve latest release from GitHub API (it may not exist yet)"
      return 1
    fi
    VERSION=$(echo "$latest_json" | sed -n 's/.*"tag_name": *"v\?\([^"]*\)".*/\1/p' | head -n1)
    [[ -n "$VERSION" ]] || return 1
    log "latest version: $VERSION"
  fi
  local asset="coder-${platform}.tar.gz"
  # Accept VERSION with or without a leading `v` (e.g. "0.9.71" or "v0.9.71").
  local tag="${VERSION#v}"
  [[ "$tag" != "$VERSION" ]] && VERSION="$tag" || VERSION="v$tag"
  local url="https://github.com/${REPO}/releases/download/${VERSION}/${asset}"
  log "downloading $url"
  if ! curl -fsSL -o "$tmpdir/$asset" "$url"; then
    warn "prebuilt binary $asset not found at $url"
    return 1
  fi
  tar -xzf "$tmpdir/$asset" -C "$tmpdir"
  [[ -x "$tmpdir/coder" ]] || return 1
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
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-X 'main.version=$version' -X 'main.productName=$product' -X 'main.author=$author'" \
      -o "$tmpdir/coder" ./cmd/coder ) || die "build failed in $srcdir"
  [[ -x "$tmpdir/coder" ]] || die "build did not produce a 'coder' binary"
  echo "$tmpdir/coder"
}

# --- install --------------------------------------------------------------
install_binary() {
  local src="$1" dir="$2"
  if ! mkdir -p "$dir" 2>/dev/null; then
    die "cannot create $dir — try INSTALL_DIR=\$HOME/.local/bin or run with sudo"
  fi
  if [[ ! -w "$dir" ]]; then
    die "$dir is not writable — try INSTALL_DIR=\$HOME/.local/bin or run with sudo"
  fi
  install -m 0755 "$src" "$dir/$BIN_NAME"
  echo "$dir/$BIN_NAME"
}

# --- PATH hint ------------------------------------------------------------
path_hint() {
  local dir="$1"
  case ":$PATH:" in
    *":$dir:"*) return 0 ;;
  esac
  warn "$dir is not on your PATH"
  cat >&2 <<EOF

Add this to your shell rc (~/.bashrc, ~/.zshrc, etc.):

    export PATH="$dir:\$PATH"

Then restart your shell or:

    export PATH="$dir:\$PATH"

EOF
}

# --- optional Ollama setup ------------------------------------------------
# Check for a running Ollama server. If not found, offer to install it
# and pull a default coding model. Skip silently on Termux (Ollama
# doesn't support Android/armv7) and when --skip-ollama is set.
maybe_install_ollama() {
  # Allow opting out entirely.
  if [[ "${SKIP_OLLAMA:-}" == "1" || "${INSTALL_OLLAMA:-}" == "0" ]]; then
    return 0
  fi

  # Ollama is not available on Termux / Android.
  if is_termux_env; then
    return 0
  fi

  # Is Ollama already installed and running?
  if command -v ollama >/dev/null 2>&1; then
    if curl -fsS --max-time 3 http://localhost:11434/api/tags >/dev/null 2>&1; then
      log "Ollama is already running at http://localhost:11434"
      # Offer to pull a model if none are present.
      local model_count
      model_count=$(curl -fsS http://localhost:11434/api/tags 2>/dev/null \
        | grep -o '"name"' | wc -l || echo 0)
      if [[ "$model_count" -eq 0 ]]; then
        local default_model="${OLLAMA_MODEL:-gemma4:cloud}"
        log "no models pulled yet; pulling $default_model (this may take a while)..."
        ollama pull "$default_model" || warn "ollama pull failed — you can do this manually with: ollama pull $default_model"
      fi
      return 0
    fi
    warn "Ollama is installed but not running. Start it with: ollama serve"
    return 0
  fi

  # Ollama not installed — offer to install it (non-interactive unless
  # INSTALL_OLLAMA=1 is set to force).
  local msg="Ollama is not installed. Coder CLI needs an Ollama server to run."
  if [[ "${INSTALL_OLLAMA:-}" == "1" ]]; then
    log "installing Ollama..."
    if curl -fsSL https://ollama.com/install.sh | bash; then
      log "Ollama installed. Starting server..."
      ollama serve >/dev/null 2>&1 &
      sleep 2
      local default_model="${OLLAMA_MODEL:-gemma4:cloud}"
      log "pulling default model $default_model..."
      ollama pull "$default_model" || warn "ollama pull failed — you can do this manually with: ollama pull $default_model"
    else
      warn "Ollama installation failed. Install it manually: https://ollama.com"
    fi
  else
    warn "$msg"
    warn "Install it automatically with: INSTALL_OLLAMA=1 curl -fsSL https://raw.githubusercontent.com/tjcoder-labs/cli/main/install.sh | bash"
    warn "Or install Ollama separately: https://ollama.com"
  fi
}

# --- main -----------------------------------------------------------------
main() {
  local platform tmpdir src installed
  platform=$(detect_platform)
  tmpdir=$(mktemp -d)
  trap 'rm -rf "${tmpdir:-}"' EXIT

  log "platform: $platform"
  log "install:  $INSTALL_DIR"

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
    # Missing build deps. On Termux we can self-heal: pkg is always present.
    if is_termux_env && command -v pkg >/dev/null 2>&1; then
      warn "missing 'go' and/or 'git'; installing via pkg"
      pkg update -y >/dev/null 2>&1 || true
      pkg install -y git golang || die "pkg install git golang failed — run it manually and re-run this script"
      srcdir=$(clone_source "$tmpdir")
      src=$(build_from_source "$srcdir" "$tmpdir")
    else
      local msg="no prebuilt binary available for $platform, and cannot build from source (need 'go' and 'git' on PATH)"
      die "$msg"
    fi
  fi

  installed=$(install_binary "$src" "$INSTALL_DIR")
  log "installed $installed"
  "$installed" --version || true
  path_hint "$INSTALL_DIR"

  # Optional: check for Ollama and offer to install + pull a default model.
  maybe_install_ollama
}

main "$@"
