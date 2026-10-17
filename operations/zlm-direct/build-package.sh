#!/usr/bin/env bash
set -euo pipefail
if [[ $# != 2 ]]; then
  echo 'Usage: build-package.sh output-directory ZLM_IMAGE@sha256:...' >&2
  exit 2
fi
output=$1
zlm=$2
[[ $zlm =~ @sha256:[a-f0-9]{64}$ ]] || { echo 'Immutable ZLM image digest required' >&2; exit 2; }
[[ ! -e $output ]] || { echo 'Output directory already exists' >&2; exit 2; }
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
mkdir -p -- "$output"
output=$(realpath "$output")
tag="streamfusion/zlm-direct:v1-$(printf '%s' "$zlm" | sha256sum | cut -c1-16)"
docker build --network host --build-arg "ZLM_IMAGE=$zlm" -t "$tag" "$root"
docker run --rm --network none --entrypoint /bin/sh "$tag" -c 'test -x /opt/media/MediaServer && command -v ffmpeg && command -v curl'
docker save --output "$output/zlm-direct-image.tar" "$tag"
docker inspect --format '{{.Id}}' "$tag" > "$output/image-id.txt"
printf 'zlm=%s\n' "$zlm" > "$output/upstream-image.txt"
(cd "$output" && sha256sum zlm-direct-image.tar image-id.txt upstream-image.txt > SHA256SUMS)
echo "Created $output"
