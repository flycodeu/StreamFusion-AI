#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
binary= sha256= root= controller_id= target_id= account= controller_unit=
docker_binary=/usr/bin/docker
docker_socket=/var/run/docker.sock
apply=false
fail(){ echo "$*" >&2; exit 1; }
while (($#)); do
  case "$1" in
    --binary|--sha256|--root|--controller-id|--target-id|--account|--controller-unit|--docker-binary|--docker-socket)
      (($#>=2)) || fail '参数缺少值'
      case "$1" in
        --binary) binary=$2;; --sha256) sha256=$2;; --root) root=$2;;
        --controller-id) controller_id=$2;; --target-id) target_id=$2;;
        --account) account=$2;; --controller-unit) controller_unit=$2;;
        --docker-binary) docker_binary=$2;; --docker-socket) docker_socket=$2;;
      esac
      shift 2;;
    --apply) apply=true;shift;;
    -h|--help) echo 'install-target.sh --binary FILE --sha256 HASH --root DIR --controller-id UUID --target-id UUID --account EXISTING_USER [--controller-unit NAME.service] [--apply]';exit 0;;
    *) fail '未知参数';;
  esac
done
[[ $account =~ ^[a-z_][a-z0-9_-]{0,31}$ ]] && id "$account" >/dev/null || fail '请明确选择已存在的专用连接账号'
[[ $root =~ ^/(opt|srv|var/lib)/[A-Za-z0-9_./-]+$ && $(realpath -m "$root") == "$root" ]] || fail '受管根目录不正确'
[[ ! -e $root && ! -L $root ]] || fail '目标根已存在，拒绝覆盖或重新接管'
[[ $controller_id =~ ^[a-f0-9-]{36}$ && $target_id =~ ^[a-f0-9-]{36}$ ]] || fail '控制器或目标ID不正确'
[[ $sha256 =~ ^[a-f0-9]{64}$ && -f $binary && ! -L $binary ]] || fail '请提供二进制及可信摘要'
[[ $(sha256sum "$binary" | cut -d' ' -f1) == "$sha256" && $("$binary" version) == 0.2.0 ]] || fail '二进制身份不符'
policy="/etc/sudoers.d/sfops-${target_id}"
[[ ! -e $policy && ! -L $policy ]] || fail '权限策略已存在，拒绝覆盖'
if [[ -n $controller_unit ]]; then
  [[ $controller_unit =~ ^[a-z][a-z0-9-]{2,28}\.service$ ]] || fail '控制器单元名不正确'
  [[ $(systemctl show "$controller_unit" -p User --value) == "$account" ]] || fail '本机单元账号与连接账号不一致'
  dropin="/etc/systemd/system/${controller_unit}.d/20-sfops-target.conf"
  [[ ! -e $dropin ]] || fail '本机执行权限例外已存在'
fi
printf '目标安装计划\n根目录: %s\n连接账号: %s\n权限: 仅 %s/executor target-exec\nDocker: %s / %s\n' "$root" "$account" "$root" "$docker_binary" "$docker_socket"
if [[ -n $controller_unit ]]; then
  printf '本机控制器例外: %s，NoNewPrivileges=false，增加受管根写路径；账号仍不加入docker组。\n' "$controller_unit"
fi
$apply || exit 0
[[ $EUID == 0 ]] || fail '--apply 需要root'
command -v visudo >/dev/null || fail '缺少visudo'
"$binary" target-init --root "$root" --controller-id "$controller_id" --target-id "$target_id" --docker-binary "$docker_binary" --docker-socket "$docker_socket"
tmp=$(mktemp /etc/sudoers.d/.sfops-XXXXXX)
trap 'rm -f -- "$tmp"' EXIT
printf '%s ALL=(root) NOPASSWD: %s/executor target-exec\n' "$account" "$root" > "$tmp"
chmod 0440 "$tmp"
visudo -cf "$tmp"
mv -T "$tmp" "$policy"
if [[ -n $controller_unit ]]; then
  install -d -m 0755 "/etc/systemd/system/${controller_unit}.d"
  printf '[Service]\nNoNewPrivileges=false\nReadWritePaths=%s\n' "$root" > "$dropin"
  chmod 0644 "$dropin"
  systemctl daemon-reload
  echo '接入文件已安装；在维护窗口重启控制器单元后核对目标。'
fi
