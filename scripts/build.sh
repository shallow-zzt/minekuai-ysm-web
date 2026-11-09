#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
(cd web && npm ci && npm run build)
mkdir -p dist
(cd backend && go test ./... && go build -trimpath -o ../dist/ysm-bridge .)
