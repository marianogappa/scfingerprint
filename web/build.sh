#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o web/scfingerprint.wasm ./cmd/scfingerprint-web
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" web/
