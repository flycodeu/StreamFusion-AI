#!/usr/bin/env bash
set -euo pipefail
# Run on the build machine. Target nodes only load the resulting archive; they never compile.
if [[ $# != 4 ]]; then echo 'Usage: build-package.sh preview-node.jar output-directory ZLM_IMAGE@sha256:... JRE_IMAGE@sha256:...' >&2; exit 2; fi
jar=$(realpath "$1"); output=$2; zlm=$3; jre=$4
[[ $zlm =~ @sha256:[a-f0-9]{64}$ && $jre =~ @sha256:[a-f0-9]{64}$ ]] || { echo 'Immutable image digests required' >&2; exit 2; }
[[ ! -e $output ]] || { echo 'Output directory already exists' >&2; exit 2; }
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
mkdir -p -- "$output/build"
output=$(realpath "$output")
cp -- "$jar" "$output/build/preview-node.jar"
cp -- "$root/Dockerfile" "$root/entrypoint.sh" "$root/logback-node.xml" "$output/build/"
version=$(sha256sum "$jar" | cut -c1-16)
image="streamfusion/preview-node:s3-$version"
docker build --network host --build-arg "ZLM_IMAGE=$zlm" --build-arg "JRE_IMAGE=$jre" -t "$image" "$output/build"
docker run --rm --network none --entrypoint java "$image" -jar /opt/preview/preview-node.jar --help
docker save --output "$output/preview-node-image.tar" "$image"
cp -- "$root/compose.yaml" "$root/configure.py" "$root/preflight.sh" "$root/README.md" "$output/"
awk '!/^DROP TABLE/ {print}' "$root/../sql/业务/预览节点表.sql" "$root/../sql/业务/预览源归属表.sql" "$root/../sql/业务/预览输出预占表.sql" "$root/../sql/业务/预览远程观看表.sql" "$root/../sql/业务/预览远程操作表.sql" > "$output/platform-upgrade.sql"
python3 "$root/export-s2-upgrade.py" "$output/platform-s2-to-s3.sql"
printf 'PREVIEW_IMAGE=%s\nPREVIEW_BIND_IP=127.0.0.1\nPREVIEW_CPU_LIMIT=8\nPREVIEW_MEMORY_LIMIT=8g\n' "$image" > "$output/.env"
docker inspect --format '{{.Id}}' "$image" > "$output/image-id.txt"
printf 'zlm=%s\njre=%s\n' "$zlm" "$jre" > "$output/upstream-images.txt"
(cd "$output" && sha256sum preview-node-image.tar image-id.txt upstream-images.txt compose.yaml configure.py preflight.sh README.md platform-upgrade.sql platform-s2-to-s3.sql > SHA256SUMS)
echo "Created $output"
