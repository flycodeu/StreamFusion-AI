#!/usr/bin/env bash
set -Eeuo pipefail
prefix=/opt/streamfusion-ops
apply=false
while (($#)); do
  case "$1" in
    --prefix) prefix=$2; shift 2;; --apply) apply=true; shift;;
    -h|--help) echo '用法: bash uninstall-linux.sh [--prefix DIR] [--apply]；永久保留状态及账号'; exit 0;;
    *) exit 2;;
  esac
done
fail() { echo "$*" >&2; exit 1; }
[[ $prefix =~ ^/[A-Za-z0-9_./-]+$ && $(realpath -m "$prefix") == "$prefix" ]] || fail '程序路径不正确'
marker="$prefix/.ops-install"
[[ -f $marker && ! -L $marker ]] || fail '缺少安装归属标记'
name=$(sed -n 's/^name=//p' "$marker")
state=$(sed -n 's/^state=//p' "$marker")
port=$(sed -n 's/^port=//p' "$marker")
sha256=$(sed -n 's/^sha256=//p' "$marker")
[[ $name =~ ^[a-z][a-z0-9-]{2,28}$ && $sha256 =~ ^[0-9a-f]{64}$ ]] || fail '归属标记不正确'
grep -Fxq "prefix=$prefix" "$marker" || fail '路径归属不一致'
unit="/etc/systemd/system/$name.service"
[[ -f $unit && ! -L $unit ]] || fail '缺少受管单元'
grep -Fxq "ExecStart=$prefix/streamfusion-ops serve --state $state --listen 127.0.0.1:$port" "$unit" ||
  grep -Fxq "ExecStart=$prefix/streamfusion-ops serve --state $state --listen 127.0.0.1:$port --platform-token-file $state/platform-service.token" "$unit" ||
  fail '单元归属发生变化'
[[ -f $prefix/streamfusion-ops && ! -L $prefix/streamfusion-ops && $(sha256sum "$prefix/streamfusion-ops" | cut -d' ' -f1) == "$sha256" ]] || fail '程序归属发生变化'
printf '卸载计划: 程序 %s；单元 %s；状态及账号保留: %s\n' "$prefix" "$name" "$state"
$apply || exit 0
[[ $EUID == 0 ]] || fail '--apply 需要root'
systemctl disable --now "$name.service"
rm -f -- "$unit"; systemctl daemon-reload
rm -f -- "$prefix/streamfusion-ops" "$marker"
rmdir -- "$prefix" 2>/dev/null || true
echo '已移除程序；状态、凭据、密钥及服务账号保留。'
