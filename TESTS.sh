#!/usr/bin/env bash
set -euo pipefail
MODEL="${1:-gemma4}"
AGENT="${2:-terminal-specialist}"
PROVIDER="${3:-ollama}"
BIN="./bin/coder"

if [ ! -x "$BIN" ]; then
  echo "Building coder binary..."
  go build -o "$BIN" ./cmd/coder
fi

echo "Running tests: agent=$AGENT provider=$PROVIDER model=$MODEL"
cat TESTS.md | "$BIN" -provider "$PROVIDER" -model "$MODEL" -agent "$AGENT" -all-tools > report.json 2> tests.log || rc=$?
echo "Exit code: ${rc:-0}"
echo "Report saved to report.json; logs to tests.log"
exit ${rc:-0}
