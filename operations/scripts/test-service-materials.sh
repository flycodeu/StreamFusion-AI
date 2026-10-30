#!/usr/bin/env bash
# Isolated copy/preflight fixture. No accounts, units, controller state, targets,
# vendor installs or running services are touched. Root checks public ownership.
set -Eeuo pipefail
umask 077
[[ $EUID == 0 && $# == 1 ]] || { echo '用法: sudo bash scripts/test-service-materials.sh /absolute/path/to/binary'; exit 2; }
scripts=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
binary=$(realpath "$1")
work=$(mktemp -d /tmp/sfo-materials-fixture-XXXXXX)
fail() { echo "$*" >&2; exit 1; }
. "$scripts/target-materials.sh"
. "$scripts/service-materials.sh"
cleanup() {
  # Enumerate only fixture-created paths; do not recurse through links.
  rm -f -- "$work/release/materials/library.json" "$work/release/materials/jdk package.tar.gz" "$work/release/SHA256SUMS" "$work/release/streamfusion-ops" \
    "$work/prefix/materials/library.json" "$work/prefix/materials/jdk package.tar.gz" "$work/prefix/materials/operator-note"
  rmdir -- "$work/release/materials" "$work/release" "$work/prefix/materials" "$work/prefix" "$work" 2>/dev/null || true
}
trap cleanup EXIT
mkdir -m 0755 "$work/release" "$work/prefix"
cp -- "$binary" "$work/release/streamfusion-ops"
chmod 0755 "$work/release/streamfusion-ops"
service_materials_preflight "$work/prefix" "$work/release/streamfusion-ops"
[[ -z $service_materials_source_dir && ! -e $work/prefix/materials ]]
mkdir -m 0755 "$work/release/materials"
printf 'transport fixture, not an official vendor package\n' > "$work/release/materials/jdk package.tar.gz"
package_sha=$(sha256sum -- "$work/release/materials/jdk package.tar.gz" | cut -d' ' -f1)
package_size=$(stat -c %s "$work/release/materials/jdk package.tar.gz")
printf '{"schemaVersion":1,"materials":[{"recipeKey":"java","recipeVersion":"8","recipeRevision":"java-8u504b01-native-v1","runtimeMode":"NATIVE","name":"JDK 8 fixture","file":"jdk package.tar.gz","sha256":"%s","byteSize":%s}]}\n' "$package_sha" "$package_size" > "$work/release/materials/library.json"
(cd "$work/release" && sha256sum -- materials/library.json 'materials/jdk package.tar.gz' > SHA256SUMS)
service_materials_preflight "$work/prefix" "$work/release/streamfusion-ops"
[[ ! -e $work/prefix/materials ]] || fail 'preflight wrote destination'
service_materials_install "$work/prefix" "$work/release/streamfusion-ops"
[[ $(stat -c %a "$work/prefix/materials") == 755 && $(stat -c %a "$work/prefix/materials/library.json") == 644 && $(stat -c %U "$work/prefix/materials/jdk package.tar.gz") == root ]]
cmp -- "$work/release/materials/jdk package.tar.gz" "$work/prefix/materials/jdk package.tar.gz"
printf 'keep operator file\n' > "$work/prefix/materials/operator-note"
chmod 0644 "$work/prefix/materials/operator-note"
service_materials_preflight "$work/prefix" "$work/release/streamfusion-ops"
service_materials_install "$work/prefix" "$work/release/streamfusion-ops"
[[ $(cat "$work/prefix/materials/operator-note") == 'keep operator file' ]] || fail 'operator file changed'
printf 'different existing bytes\n' > "$work/prefix/materials/jdk package.tar.gz"
if (service_materials_preflight "$work/prefix" "$work/release/streamfusion-ops") >/dev/null 2>&1; then fail 'conflicting destination accepted'; fi
[[ $(cat "$work/prefix/materials/jdk package.tar.gz") == 'different existing bytes' ]] || fail 'conflicting destination overwritten'
rm -f -- "$work/prefix/materials/jdk package.tar.gz"
ln -s -- "$work/release/materials/jdk package.tar.gz" "$work/prefix/materials/jdk package.tar.gz"
if (service_materials_preflight "$work/prefix" "$work/release/streamfusion-ops") >/dev/null 2>&1; then fail 'linked destination accepted'; fi
rm -f -- "$work/prefix/materials/jdk package.tar.gz"
service_materials_preflight "$work/prefix" "$work/release/streamfusion-ops"
service_materials_install "$work/prefix" "$work/release/streamfusion-ops"
[[ -f $work/prefix/materials/jdk\ package.tar.gz ]] || fail 'missing declared file was not recovered'
printf '\n' >> "$work/release/materials/library.json"
if (service_materials_preflight "$work/prefix" "$work/release/streamfusion-ops") >/dev/null 2>&1; then fail 'changed release checksum accepted'; fi
printf '{"schemaVersion":1,"schemaVersion":1,"materials":[]}\n' > "$work/release/materials/library.json"
if (service_materials_preflight "$work/prefix" "$work/release/streamfusion-ops") >/dev/null 2>&1; then fail 'invalid release manifest accepted'; fi
echo 'PASS: material preflight / public modes / repeat / conflict / link / missing-file resume / checksum / invalid manifest'
