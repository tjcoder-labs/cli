#!/usr/bin/env bash
# Build cross-platform release binaries for the current package.json version.
# Produces assets in both naming schemes used by the project:
#   coder-<os>-<arch>.<ext>          (install.sh)
#   coder_v<ver>_<os>_<arch>.<ext>   (npm postinstall fetch-binary.js)
set -euo pipefail

VERSION=$(python3 -c 'import json; print(json.load(open("package.json"))["version"])')
PRODUCT=$(python3 -c 'import json; d=json.load(open("package.json")); print(d.get("productName") or d.get("name") or "Coder CLI")')
AUTHOR=$(python3 -c 'import json; print(json.load(open("package.json"))["author"])')
LDFLAGS="-X main.version=${VERSION} -X main.productName=${PRODUCT} -X main.author=${AUTHOR}"

# go:embed staging (mirrors Makefile)
cp package.json cmd/coder/package.json

OUT=bin/release
rm -rf "$OUT"
mkdir -p "$OUT"

targets=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
  "windows amd64"
)

for t in "${targets[@]}"; do
  read -r GOOS GOARCH <<<"$t"
  bin="coder"
  ext=".tar.gz"
  if [ "$GOOS" = "windows" ]; then
    bin="coder.exe"
    ext=".zip"
  fi
  echo "==> building ${GOOS}/${GOARCH}"
  CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH \
    go build -trimpath \
      -ldflags "-X 'main.version=${VERSION}' -X 'main.productName=${PRODUCT}' -X 'main.author=${AUTHOR}' -X 'main.buildMethod=binary'" \
      -o "$OUT/$bin" ./cmd/coder

  a1="coder-${GOOS}-${GOARCH}${ext}"
  a2="coder_v${VERSION}_${GOOS}_${GOARCH}${ext}"
  if [ "$ext" = ".zip" ]; then
    ( cd "$OUT" && python3 -c "import zipfile,sys; z=zipfile.ZipFile('${a1}','w',zipfile.ZIP_DEFLATED); z.write('${bin}'); z.close()" && cp "${a1}" "${a2}" )
  else
    ( cd "$OUT" && tar -czf "${a1}" "$bin" && cp "${a1}" "${a2}" )
  fi
  rm -f "$OUT/$bin"
done

ls -la "$OUT"
