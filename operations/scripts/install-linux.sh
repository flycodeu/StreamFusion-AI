#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
prefix=/opt/streamfusion-ops
state=/var/lib/streamfusion-ops
name=streamfusion-ops
port=19090
binary= sha256= password_file= apply=false
help() {
  echo '用法: bash install-linux.sh --binary FILE --sha256 HASH [--apply]'
  echo '可选: --prefix DIR --state DIR --name NAME --port PORT --password-file FILE'
  echo '默认只展示计划。新装在本机隐藏输入密码，或提供0600密码文件。'
}
fail() { echo "$*" >&2; exit 1; }
while (($#)); do
  case "$1" in
    --binary|--sha256|--prefix|--state|--name|--port|--password-file)
      (($# >= 2)) || fail '参数缺少值'
      case "$1" in
        --binary) binary=$2;; --sha256) sha256=$2;; --prefix) prefix=$2;;
        --state) state=$2;; --name) name=$2;; --port) port=$2;; --password-file) password_file=$2;;
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
unit_text=$(cat <<UNIT
[Unit]
Description=StreamFusion Ops local controller
After=network.target

[Service]
Type=simple
User=$name
Group=$name
WorkingDirectory=$state
ExecStart=$prefix/streamfusion-ops serve --state $state --listen 127.0.0.1:$port
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
)
if [[ -e $unit ]]; then [[ ! -L $unit && $(cat "$unit") == "$unit_text" ]] || fail '服务单元内容不一致'; fi
if [[ -e $prefix/streamfusion-ops ]]; then
  [[ ! -L $prefix/streamfusion-ops && $(sha256sum "$prefix/streamfusion-ops" | cut -d' ' -f1) == "$sha256" ]] || fail '已有二进制内容发生变化'
fi
printf '安装计划\n版本: 0.2.0\n程序: %s\n状态: %s\n单元及账号: %s\n地址: http://127.0.0.1:%s\n摘要: %s\n' "$prefix" "$state" "$name" "$port" "$sha256"
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
pending_unit=$(mktemp "/etc/systemd/system/.$name-XXXXXX")
printf '%s\n' "$unit_text" > "$pending_unit"; chmod 0644 "$pending_unit"
mv -T "$pending_unit" "$unit"; pending_unit=
systemctl daemon-reload
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
