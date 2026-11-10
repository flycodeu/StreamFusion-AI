set -euo pipefail
umask 077
root="$OPS_INSTALL_ROOT"
unit="ops-labelstudio-$OPS_SOFTWARE_ID.service"
container="ops-labelstudio-$OPS_SOFTWARE_ID"
service_user="ls${OPS_SOFTWARE_ID//-/}"
service_user=${service_user:0:27}
fail() { printf '%s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null || fail "缺少依赖 $1"; }
[[ "$root" == /* && "$root" != / && ! -L "$root" ]] || fail '安装目录不安全'
check_owner() { [[ -f "$root/config/ops-owner" && ! -L "$root/config/ops-owner" && "$(cat "$root/config/ops-owner")" == "label-studio:$OPS_SOFTWARE_ID" ]] || fail '安装目录归属不符，请核对原任务'; }
