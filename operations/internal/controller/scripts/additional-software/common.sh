set -euo pipefail
umask 077
app='@@APP@@'
version='@@VERSION@@'
root="$OPS_INSTALL_ROOT"
unit="ops-app-${OPS_SOFTWARE_ID}.service"
container="sfops-${OPS_SOFTWARE_ID}"
service_user="ops${OPS_SOFTWARE_ID//-/}"
service_user="${service_user:0:27}"
fail() { printf '%s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null || fail "缺少依赖 $1，请先安装后重试"; }
[[ "$root" == /* && "$root" != / && ! -L "$root" ]] || fail '安装目录不安全'
check_owner() { [[ -f "$root/config/ops-owner" && "$(cat "$root/config/ops-owner")" == "$app:$OPS_SOFTWARE_ID" ]] || fail '安装目录归属不符，请核对原任务'; }
record_owner() { printf '%s\n' "$app:$OPS_SOFTWARE_ID" > "$root/config/ops-owner"; chmod 600 "$root/config/ops-owner"; }
owned_container() { [[ "$(docker inspect --format '{{index .Config.Labels "streamfusion.ops.software-id"}}' "$container")" == "$OPS_SOFTWARE_ID" ]] || fail '容器归属不符'; }
