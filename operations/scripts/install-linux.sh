#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
prefix=/opt/streamfusion-ops
state=/var/lib/streamfusion-ops
name=streamfusion-ops
port=19090
binary= sha256= password_file= platform_token_file= apply=false
help() {
  echo '用法: bash install-linux.sh --binary FILE --sha256 HASH [--apply]'
  echo '可选: --prefix DIR --state DIR --name NAME --port PORT --password-file FILE --platform-token-file FILE'
  echo '默认只展示计划。新装在本机隐藏输入密码，或提供0600密码文件。'
  echo '--platform-token-file 是0700目录内的0600源文件，安装后复制到私有状态目录；重复安装须继续提供同一令牌。'
}
fail() { echo "$*" >&2; exit 1; }
script_directory=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
. "$script_directory/target-materials.sh"
. "$script_directory/service-materials.sh"
while (($#)); do
  case "$1" in
    --binary|--sha256|--prefix|--state|--name|--port|--password-file|--platform-token-file)
      (($# >= 2)) || fail '参数缺少值'
      case "$1" in
        --binary) binary=$2;; --sha256) sha256=$2;; --prefix) prefix=$2;;
        --state) state=$2;; --name) name=$2;; --port) port=$2;; --password-file) password_file=$2;;
        --platform-token-file) platform_token_file=$2;;
      esac
      shift 2;;
    --apply) apply=true; shift;; -h|--help) help; exit 0;;
    *) help >&2; exit 2;;
  esac
done
[[ $name =~ ^[a-z][a-z0-9-]{2,28}$ ]] || fail '服务名称不正确'
[[ $port =~ ^[0-9]{1,5}$ ]] && ((10#$port >= 1 && 10#$port <= 65535)) || fail '端口不正确'
[[ $sha256 =~ ^[0-9a-f]{64}$ ]] || fail '必须提供固定二进制的 --sha256'
[[ -f $binary && ! -L $binary && -x $binary ]] || fail '二进制不存在、不可执行或为符号链接'
binary=$(realpath "$binary")
[[ $(sha256sum "$binary" | cut -d' ' -f1) == "$sha256" ]] || fail '二进制摘要不匹配'
[[ $("$binary" version) == 0.2.0 ]] || fail '本安装脚本只接受0.2.0'
[[ $(uname -s) == Linux && $(uname -m) == x86_64 && -d /run/systemd/system ]] || fail '需要Linux amd64及systemd'
. /etc/os-release
[[ $ID == ubuntu && $VERSION_ID == 24.04 ]] || fail '首期支持Ubuntu24.04'
for path in "$prefix" "$state"; do
  [[ $path =~ ^/[A-Za-z0-9_./-]+$ ]] || fail '安装路径须为不含空格的绝对路径'
  [[ $(realpath -m "$path") == "$path" ]] || fail '安装路径须规范化，不能包含符号链接或..'
  [[ $path != / && $path != /opt && $path != /var && $path != /var/lib && $path != /usr && $path != /home ]] || fail '不能使用系统根目录'
done
[[ $prefix != "$state" && $state != "$prefix/"* && $prefix != "$state/"* ]] || fail '程序和状态目录不能相互包含'
if [[ -n $platform_token_file ]]; then
  [[ $platform_token_file == /* && $(realpath -m "$platform_token_file") == "$platform_token_file" ]] || fail 'Platform 服务令牌源路径须为规范绝对路径'
  platform_token_dir=$(dirname "$platform_token_file")
  [[ -d $platform_token_dir && ! -L $platform_token_dir && $(stat -c %a "$platform_token_dir") == 700 ]] || fail 'Platform 服务令牌源目录须为0700普通目录'
  [[ -f $platform_token_file && ! -L $platform_token_file && $(stat -c %a "$platform_token_file") == 600 ]] || fail 'Platform 服务令牌源文件须为0600普通文件'
  platform_token=$(head -c 64 "$platform_token_file")
  [[ $platform_token =~ ^[a-f0-9]{64}$ ]] &&
    { cmp -s "$platform_token_file" <(printf '%s' "$platform_token") || cmp -s "$platform_token_file" <(printf '%s\n' "$platform_token"); } ||
    fail 'Platform 服务令牌须为64位小写十六进制，可带末尾换行'
  platform_destination="$state/platform-service.token"
  if [[ -e $platform_destination || -L $platform_destination ]]; then
    [[ -f $platform_destination && ! -L $platform_destination && $(stat -c %a "$platform_destination") == 600 &&
       $(stat -c %U "$platform_destination") == "$name" && $(stat -c %G "$platform_destination") == "$name" &&
       $(cat "$platform_destination") == "$platform_token" ]] || fail '已安装的 Platform 服务令牌不一致或权限不安全，拒绝覆盖'
  fi
fi
unit="/etc/systemd/system/$name.service"
marker="$prefix/.ops-install"
expected=$(printf 'schema=2\nversion=0.2.0\nname=%s\nprefix=%s\nstate=%s\nport=%s\nsha256=%s' "$name" "$prefix" "$state" "$port" "$sha256")
if [[ -e $marker ]]; then
  [[ ! -L $marker && $(cat "$marker") == "$expected" ]] || fail '已有安装的路径、版本或归属不一致'
elif [[ -e $prefix && -n $(find "$prefix" -mindepth 1 -maxdepth 1 -print -quit) ]] ||
     [[ -e $state && -n $(find "$state" -mindepth 1 -maxdepth 1 -print -quit) ]] ||
     [[ -e $unit ]]; then
  fail '路径或单元已被非受管安装使用，拒绝接管'
fi
if id "$name" >/dev/null 2>&1; then
  IFS=: read -r _ _ uid gid _ account_home account_shell < <(getent passwd "$name")
  [[ $uid != 0 && $account_home == "$state" && $account_shell == /usr/sbin/nologin && $(id -G "$name") == "$gid" ]] || fail '现有账号归属或权限不符'
fi
make_unit_text() { cat <<UNIT
[Unit]
Description=StreamFusion Ops local controller
After=network.target

[Service]
Type=simple
User=$name
Group=$name
WorkingDirectory=$state
ExecStart=$prefix/streamfusion-ops serve --state $state --listen 127.0.0.1:$port$1
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=$state
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
LockPersonality=true
UMask=0077
TimeoutStopSec=15

[Install]
WantedBy=multi-user.target
UNIT
}
platform_arg=
[[ -z $platform_token_file ]] || platform_arg=" --platform-token-file $platform_destination"
unit_text=$(make_unit_text "$platform_arg")
baseline_unit_text=$(make_unit_text '')
unit_changed=false
if [[ -e $unit ]]; then
  [[ ! -L $unit ]] || fail '服务单元不允许为符号链接'
  if [[ $(cat "$unit") != "$unit_text" ]]; then
    [[ -n $platform_token_file && $(cat "$unit") == "$baseline_unit_text" ]] || fail '服务单元内容不一致'
    unit_changed=true
  fi
fi
if [[ -e $prefix/streamfusion-ops ]]; then
  [[ ! -L $prefix/streamfusion-ops && $(sha256sum "$prefix/streamfusion-ops" | cut -d' ' -f1) == "$sha256" ]] || fail '已有二进制内容发生变化'
fi
target_script=$(target_materials_source "$binary" "$script_directory")
target_materials_preflight "$prefix" "$sha256" "$target_script"
service_materials_preflight "$prefix" "$binary"
printf '安装计划\n版本: 0.2.0\n程序: %s\n状态: %s\n单元及账号: %s\n地址: http://127.0.0.1:%s\nPlatform 目录: %s\n摘要: %s\n' "$prefix" "$state" "$name" "$port" "$([[ -n $platform_token_file ]] && echo 启用 || echo 关闭)" "$sha256"
printf 'Linux目标准备材料（只读）: %s/release/0.2.0\n' "$prefix"
[[ -z $service_materials_source_dir ]] || printf '安装材料库（公开只读文件）: %s/materials，%s个文件\n' "$prefix" "${#service_materials_files[@]}"
$apply || exit 0
[[ $EUID == 0 ]] || fail '--apply 需要root'
pending_binary= pending_unit= pending_password=
cleanup() {
  [[ -z $pending_binary ]] || rm -f -- "$pending_binary"
  [[ -z $pending_unit ]] || rm -f -- "$pending_unit"
  [[ -z $pending_password ]] || rm -f -- "$pending_password"
}
trap cleanup EXIT
if ! id "$name" >/dev/null 2>&1; then useradd --system --user-group --home-dir "$state" --shell /usr/sbin/nologin "$name"; fi
install -d -m 0755 "$prefix"
install -d -m 0700 -o "$name" -g "$name" "$state"
if [[ -n $platform_token_file && ! -e $platform_destination ]]; then
  install -m 0600 -o "$name" -g "$name" "$platform_token_file" "$platform_destination"
fi
printf '%s\n' "$expected" > "$marker"; chmod 0600 "$marker"
pending_binary=$(mktemp "$prefix/.binary-XXXXXX")
install -m 0755 "$binary" "$pending_binary"
if ! systemctl is-active --quiet "$name.service"; then
  # 检查管理员后再提示初始化；重复安装不要求再次输入密码。
  check_output=$(runuser -u "$name" -- "$pending_binary" check-state --state "$state" 2>&1) || {
    [[ $check_output == *ADMIN_REQUIRED* ]] || fail "$check_output"
    if [[ -n $password_file ]]; then
      [[ -f $password_file && ! -L $password_file && $(stat -c %a "$password_file") == 600 ]] || fail '密码文件须为0600普通文件'
      pending_password=$(mktemp "$state/.password-XXXXXX")
      install -m 0600 -o "$name" -g "$name" "$password_file" "$pending_password"
      runuser -u "$name" -- "$pending_binary" init-admin --state "$state" --password-file "$pending_password"
    else
      runuser -u "$name" -- "$pending_binary" init-admin --state "$state"
    fi
  }
fi
if [[ -f $prefix/streamfusion-ops ]]; then
  # 固定版本重复安装保留正在运行的inode，不产生 /proc/exe 的 deleted 身份。
  rm -f -- "$pending_binary"
else
  mv -T "$pending_binary" "$prefix/streamfusion-ops"
fi
pending_binary=
target_materials_install "$prefix" "$binary" "$sha256" "$target_script"
service_materials_install "$prefix" "$binary"
pending_unit=$(mktemp "/etc/systemd/system/.$name-XXXXXX")
printf '%s\n' "$unit_text" > "$pending_unit"; chmod 0644 "$pending_unit"
mv -T "$pending_unit" "$unit"; pending_unit=
systemctl daemon-reload
if $unit_changed && systemctl is-active --quiet "$name.service"; then systemctl restart "$name.service"; fi
systemctl enable --now "$name.service"
for attempt in {1..15}; do
  if systemctl is-active --quiet "$name.service" &&
     [[ $(readlink -f "/proc/$(systemctl show -p MainPID --value "$name.service")/exe") == "$prefix/streamfusion-ops" ]] &&
     curl --noproxy '*' --fail --silent --max-time 2 "http://127.0.0.1:$port/ops/api/v1/health" >/dev/null; then
    echo '控制器已启动'; exit 0
  fi
  sleep 1
done
fail "控制器未就绪。检查 journalctl -u $name；状态保留，可按同一计划续装。"
