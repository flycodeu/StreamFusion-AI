#!/usr/bin/env bash
set -Eeuo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
material_library="$root/materials"
material_library_explicit=false
python_command="${PYTHON:-python3}"
while (($#)); do
  case "$1" in
    --material-library)
      [[ $# -ge 2 && -n "$2" ]] || { echo '需要 --material-library <材料目录>' >&2; exit 2; }
      material_library="$2"; material_library_explicit=true; shift 2 ;;
    --python)
      [[ $# -ge 2 && -n "$2" ]] || { echo '需要 --python <Python命令>' >&2; exit 2; }
      python_command="$2"; shift 2 ;;
    *) echo "未知参数: $1" >&2; exit 2 ;;
  esac
done
(cd web && pnpm install --frozen-lockfile && pnpm build)
node scripts/embed-web.mjs
go test ./...
go vet ./...
mkdir -p release/0.2.0
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o release/0.2.0/streamfusion-ops ./cmd/streamfusion-ops
cp scripts/install-linux.sh scripts/uninstall-linux.sh scripts/install-target.sh scripts/target-materials.sh scripts/service-materials.sh README.md release/0.2.0/
files=(streamfusion-ops install-linux.sh uninstall-linux.sh install-target.sh target-materials.sh service-materials.sh README.md)
if [[ "$material_library_explicit" == true || -e "$material_library/library.json" || -L "$material_library/library.json" ]]; then
  stage_args=(scripts/stage-material-library.py --library "$material_library" --output release/0.2.0)
  [[ "$material_library_explicit" == true ]] || stage_args+=(--optional)
  stage_json=$("$python_command" "${stage_args[@]}")
  # The validator rejects line breaks; read preserves spaces and never evals
  # names. Command substitution propagates JSON-parser failure to set -e.
  material_names=$(printf '%s' "$stage_json" | "$python_command" -c 'import json,sys; print("\n".join(json.load(sys.stdin)["files"]))')
  if [[ -n "$material_names" ]]; then
    while IFS= read -r material_file; do files+=("$material_file"); done <<< "$material_names"
  fi
fi
(cd release/0.2.0 && sha256sum -- "${files[@]}" > SHA256SUMS)
tar -czf release/streamfusion-ops-0.2.0-linux-amd64.tar.gz -C release/0.2.0 "${files[@]}" SHA256SUMS
echo '输出: release/streamfusion-ops-0.2.0-linux-amd64.tar.gz；安装时显式提供可信SHA256。'
