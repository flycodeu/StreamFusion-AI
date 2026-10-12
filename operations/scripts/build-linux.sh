#!/usr/bin/env bash
set -Eeuo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
(cd web && pnpm install --frozen-lockfile && pnpm build)
node scripts/embed-web.mjs
go test ./...
go vet ./...
mkdir -p release/0.2.0
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o release/0.2.0/streamfusion-ops ./cmd/streamfusion-ops
cp scripts/install-linux.sh scripts/uninstall-linux.sh scripts/install-target.sh README.md release/0.2.0/
(cd release/0.2.0 && sha256sum streamfusion-ops install-linux.sh uninstall-linux.sh install-target.sh README.md > SHA256SUMS)
tar -czf release/streamfusion-ops-0.2.0-linux-amd64.tar.gz -C release/0.2.0 streamfusion-ops SHA256SUMS install-linux.sh uninstall-linux.sh install-target.sh README.md
echo '输出: release/streamfusion-ops-0.2.0-linux-amd64.tar.gz；安装时显式提供可信SHA256。'
