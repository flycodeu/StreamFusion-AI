#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
[[ $(uname -m) == x86_64 ]] || { echo 'Only Linux amd64 has a package recipe' >&2; exit 2; }
[[ $(. /etc/os-release; echo "$ID:$VERSION_ID") == ubuntu:24.04 ]] || { echo 'This package targets Ubuntu 24.04' >&2; exit 2; }
sha256sum -c SHA256SUMS
docker compose version >/dev/null
docker load --input preview-node-image.tar
image=$(sed -n 's/^PREVIEW_IMAGE=//p' .env)
[[ $(docker inspect --format '{{.Id}}' "$image") == "$(cat image-id.txt)" ]] || { echo 'Image identity mismatch' >&2; exit 2; }
[[ -f config/node.json && -f config/identity.p12 && -f config/machine-token ]] || { echo 'Run configure.py first' >&2; exit 2; }
docker compose config --quiet
mkdir -p state
# No server or media process starts here. The command checks TLS, paths and journal exclusivity.
docker run --rm --network none --entrypoint java -v "$PWD/config:/config:ro" -v "$PWD/state:/state" "$image" -jar /opt/preview/preview-node.jar --render /config/node.json
docker run --rm --network none --entrypoint java -v "$PWD/config:/config:ro" -v "$PWD/state:/state" "$image" -jar /opt/preview/preview-node.jar /config/node.json --check
for port in 9443 18000; do
  if ss -H -lntu "sport = :$port" | grep -q .; then echo "Port $port is already in use" >&2; exit 2; fi
done
echo 'Preflight passed. Start with: docker compose up -d. Then register/check/enable in Platform.'
