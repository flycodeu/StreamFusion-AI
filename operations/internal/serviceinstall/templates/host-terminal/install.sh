#!/bin/bash
set -euo pipefail
export PATH=/usr/sbin:/usr/bin:/sbin:/bin LC_ALL=C DEBIAN_FRONTEND=noninteractive
umask 022

fail() { echo "统一终端工具安装失败：$*" >&2; exit 1; }
stage() { printf 'SFOPS_TERMINAL_STAGE=%s\n' "$1" >&2; }
if (( $# )); then
    if [[ $# == 1 && $1 == --help ]]; then
        echo '在目标 Ubuntu 24.04 主机运行 sudo bash install-sf-docker.sh，安装 mysql、redis-cli、psql、curl 和 sf-docker。'
        exit 0
    fi
    fail '不支持的参数，请使用 --help。'
fi
stage PLATFORM
[[ $EUID == 0 ]] || fail '请在该主机使用 sudo bash 执行此文件。'
. /etc/os-release
[[ $ID == ubuntu && $VERSION_ID == 24.04 ]] || fail '系统客户端安装适用于 Ubuntu 24.04。'

# Use system-owned paths even when invoked from an SSH user's home directory.
stage LAUNCHER
bindir=/usr/local/bin
check_dir() {
    local owner mode
    [[ -d $1 && ! -L $1 ]] || fail "$1 不是系统目录。"
    read -r owner mode < <(stat -c '%u %a' -- "$1")
    [[ $owner == 0 ]] && (( (8#$mode & 022) == 0 )) || fail "$1 需要由 root 所有且只允许 root 写入。"
}
check_dir /usr
check_dir /usr/local
if [[ ! -e $bindir && ! -L $bindir ]]; then mkdir -m 0755 -- "$bindir"; fi
check_dir "$bindir"
# flock on an already opened directory creates no persistent lock or PID file.
exec 9<"$bindir"
flock -w 30 9 || fail '另一项主机终端工具准备仍在运行。'
launcher=$bindir/sf-docker
if [[ -e $launcher || -L $launcher ]]; then
    [[ -f $launcher && ! -L $launcher && $(stat -c %u -- "$launcher") == 0 &&
       $(sed -n '2p' -- "$launcher") == '# StreamFusion Ops host terminal launcher' ]] ||
       fail "$launcher 已被其他程序占用，请核对后重试。"
fi

temporary=$(mktemp -- "$bindir/.sf-docker.XXXXXXXX")
trap 'rm -f -- "$temporary"' EXIT
cat > "$temporary" <<'SFOPS_HOST_TERMINAL_LAUNCHER_EOF'
@LAUNCHER@
SFOPS_HOST_TERMINAL_LAUNCHER_EOF
/bin/bash -n "$temporary"
chmod 0755 -- "$temporary"
if [[ ! -f $launcher ]] || ! cmp -s -- "$temporary" "$launcher" ||
   [[ $(stat -c %a -- "$launcher") != 755 ]]; then
    mv -fT -- "$temporary" "$launcher"
fi
for shortcut in java mvn python3; do
    if [[ ! -e $bindir/$shortcut && ! -L $bindir/$shortcut ]] &&
       ! PATH="$bindir:$PATH" command -v "$shortcut" >/dev/null; then
        ln -s -- sf-docker "$bindir/$shortcut"
    fi
done

clients=(mysql redis-cli psql curl)
packages=(mysql-client-core-8.0 redis-tools postgresql-client-16 curl)
client_ready() {
    local i=$1 state
    "/usr/bin/${clients[i]}" --version >/dev/null 2>&1 || return 1
    state=$(/usr/bin/dpkg-query -W -f='${db:Status-Status}' "${packages[i]}" 2>/dev/null) || return 0
    [[ $state == installed || $state == not-installed || $state == config-files ]]
}
stage DPKG
missing=()
for i in "${!clients[@]}"; do
    if ! client_ready "$i"; then missing+=("${packages[i]}"); fi
done
if (( ${#missing[@]} )); then
    echo "本次准备客户端软件包：${missing[*]}"
    audit=$(/usr/bin/dpkg --audit)
    [[ -z $audit ]] || fail '有未完成的 dpkg 操作，请先修复后重试。'
    apt=(/usr/bin/apt-get -o DPkg::Lock::Timeout=30 -o Acquire::Retries=1
         -o Acquire::http::Timeout=20 -o Acquire::https::Timeout=20
         -o Acquire::AllowInsecureRepositories=false -o APT::Get::AllowUnauthenticated=false)
    stage APT_UPDATE
    "${apt[@]}" -o APT::Update::Error-Mode=any update
    stage APT_PLAN
    options=(--no-install-recommends --no-remove --no-upgrade -o APT::Get::AutomaticRemove=false)
    plan=$("${apt[@]}" --simulate "${options[@]}" install -- "${missing[@]}")
    if ! approved=$(awk -v clients=" ${missing[*]} " '
        $1 == "Remv" || $1 == "Purg" { exit 1 }
        $1 == "Inst" {
            name=$2
            sub(/:.*/, "", name)
            runtime=(name ~ /^lib/ || name ~ /^(adduser|netbase|perl|perl-base|perl-modules-[0-9.]+|postgresql-client-common|readline-common|sensible-utils|mysql-common|ca-certificates|openssl)$/)
            version=($3 ~ /^\[/ ? $4 : $3)
            if (version !~ /^\(/ || (!index(clients, " " name " ") && !runtime)) {
                print "超出客户端及运行库范围的软件包：" name > "/dev/stderr"
                exit 1
            }
            printf " %s=%s ", name, substr(version, 2)
        }
    ' <<< "$plan"); then
        fail '请核对上面列出的软件包或系统更新，再重新准备。'
    fi
    if [[ -n $approved ]]; then
        export OPS_APPROVED_CLIENT_PACKAGES="$approved"
        guard='while IFS= read -r archive; do
            key=$(/usr/bin/dpkg-deb --show --showformat="\${Package}=\${Version}" "$archive") || exit 1
            case "$OPS_APPROVED_CLIENT_PACKAGES" in
                *" $key "*) ;;
                *) echo "APT 的实际软件包或版本已变化，请重新准备。" >&2; exit 1 ;;
            esac
        done'
        stage APT_INSTALL
        "${apt[@]}" --assume-yes "${options[@]}" -o "DPkg::Pre-Install-Pkgs::=$guard" install -- "${missing[@]}"
    fi
fi
stage VERIFY
for i in "${!clients[@]}"; do
    client_ready "$i" || fail "${clients[i]} 状态检查失败。请核对软件包安装输出。"
done
echo 'mysql、redis-cli、psql、curl 已就绪。运行 sf-docker list 查看本机容器。'
echo SFOPS_TERMINAL_READY
