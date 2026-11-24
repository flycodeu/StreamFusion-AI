#!/usr/bin/env bash
# StreamFusion Ops host terminal launcher
set -euo pipefail

usage() {
    cat <<'HELP'
用法
  sf-docker list                           列出本机容器和镜像
  sf-docker ports 容器                     查看实际发布端口
  sf-docker logs 容器                      查看最近 100 行日志
  sf-docker exec 容器 命令 [参数...]        在容器内执行指定程序
  sf-docker connect 容器 [mysql|redis|postgres] [客户端参数...]
  sf-docker java|mvn|python3 [参数...]      使用已安装的工具镜像
  sf-docker tool 工具 版本 [参数...]        选择同一工具的特定版本
  sf-docker run 本机镜像 命令 [参数...]     以当前用户和工作目录运行

容器可用名字或 ID。connect 使用宿主机客户端和发布端口，例如
  sf-docker connect my-db mysql --user=app
  sf-docker connect my-cache redis --askpass
  sf-docker connect my-pg postgres -U app -d app
HELP
}

fail() { printf '%s\n' "$*" >&2; exit 1; }
container_name() {
    [[ $1 =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] || fail "请提供本机的容器名称或 ID。"
}
tool_catalog() {
    cat <<'CATALOG'
@TOOL_IMAGES@
CATALOG
}

# Published addresses and bind mounts below refer to this host.
docker=(docker --host unix:///var/run/docker.sock)
if [[ ${DOCKER_HOST:-} == unix:///* ]]; then docker=(docker --host "$DOCKER_HOST"); fi
unset DOCKER_CONTEXT DOCKER_TLS_VERIFY DOCKER_CERT_PATH
stdio=(-i)
if [[ -t 0 && -t 1 ]]; then stdio+=(-t); fi

# Docker --mount accepts CSV fields. Quoted fields retain commas and quotes
# in real project directory names, including when an absolute path is passed.
mount_directory() {
    local source=${1//\"/\"\"} dest=${2//\"/\"\"}
    printf 'type=bind,"source=%s","target=%s"' "$source" "$dest"
}

mount_file_ro() {
    local source=${1//\"/\"\"} dest=${2//\"/\"\"}
    printf 'type=bind,"source=%s","target=%s",readonly' "$source" "$dest"
}

run_image() {
    local image=$1 program=$2 maven_cache=$3 work mount
    shift 3
    work=$(pwd -P)
    [[ $work != / ]] || fail "请先进入工作目录，运行镜像时会将该目录挂入容器。"
    mount=$(mount_directory "$work" "$work")
    local options=(--rm --pull=never "${stdio[@]}" --user "$(id -u):$(id -g)" --workdir "$work" --mount "$mount" --env HOME=/tmp)
    if [[ $maven_cache == yes ]]; then
        [[ ${HOME:-} == /* ]] || fail "无法确定当前账号的 Maven 缓存目录。"
        mkdir -p -- "$HOME/.m2"
        options+=(--mount "$(mount_directory "$HOME/.m2" /tmp/.m2)" --env MAVEN_CONFIG=/tmp/.m2 --env "MAVEN_OPTS=-Duser.home=/tmp -Djansi.graceful=true")
        local managed_settings="${SF_MAVEN_SETTINGS:-}"
        if [[ -z $managed_settings && -r /opt/streamfusion-ops-target/config/maven-settings.xml ]]; then
            managed_settings=/opt/streamfusion-ops-target/config/maven-settings.xml
        fi
        if [[ -n $managed_settings && -r $managed_settings ]]; then
            options+=(--mount "$(mount_file_ro "$managed_settings" /usr/share/maven/conf/settings.xml)")
        fi
    fi
    exec "${docker[@]}" run "${options[@]}" --entrypoint "$program" "$image" "$@"
}

tool() {
    local name=$1 selected=$2 key version image manifest id maven_cache=no
    shift 2
    case $name in java|mvn|python3) ;; *) fail "未知工具：$name。自定义镜像请使用 sf-docker run。" ;; esac
    # Inventory reports daemon/permission errors separately from a missing
    # version. A missing image must never trigger a registry pull.
    local available
    available=$("${docker[@]}" image ls --no-trunc --quiet)
    local versions=() images=()
    while IFS='|' read -r key version image manifest; do
        [[ $key == "$name" && ( -z $selected || $version == "$selected" ) ]] || continue
        [[ $image =~ ^sha256:[0-9a-f]{64}$ ]] || fail "工具目录中的镜像 ID 无效。"
        for id in "$image" "$manifest"; do
            [[ $id =~ ^sha256:[0-9a-f]{64}$ ]] || continue
            if [[ $'\n'$available$'\n' == *$'\n'"$id"$'\n'* ]]; then
                versions+=("$version")
                images+=("$id")
                break
            fi
        done
    done < <(tool_catalog)
    case ${#images[@]} in
        0) fail "本机没有相应的 $name 工具镜像。请用 sf-docker list 查看本机镜像。" ;;
        1) [[ $name != mvn ]] || maven_cache=yes
           run_image "${images[0]}" "$name" "$maven_cache" "$@" ;;
        *) fail "本机有多个 $name 版本（${versions[*]}）。请使用 sf-docker tool $name 版本 [参数...]。" ;;
    esac
}

connect() {
    local container=$1 protocol= mapping published host port key inside
    shift
    case ${1:-} in mysql|redis|postgres) protocol=$1; shift ;; esac
    container_name "$container"
    mapping=$("${docker[@]}" port "$container")
    local -A hosts=() ports=()
    while read -r inside arrow published; do
        [[ $arrow == '->' && $published == *:* ]] || continue
        case $inside in 3306/tcp) key=mysql ;; 6379/tcp) key=redis ;; 5432/tcp) key=postgres ;; *) continue ;; esac
        host=${published%:*}; port=${published##*:}
        host=${host#\[}; host=${host%\]}
        case "$host" in 0.0.0.0|'') host=127.0.0.1 ;; ::) host=::1 ;; esac
        [[ $port =~ ^[0-9]{1,5}$ && $host =~ ^[0-9a-fA-F:.]+$ ]] || continue
        (( 10#$port >= 1 && 10#$port <= 65535 )) || continue
        if [[ -z ${hosts[$key]:-} ]]; then hosts[$key]=$host; ports[$key]=$port; fi
    done <<< "$mapping"
    if [[ -z $protocol ]]; then
        case ${#hosts[@]} in
            0) fail "未找到数据库的 TCP 发布端口。请用 sf-docker ports $container 查看映射。" ;;
            1) for protocol in "${!hosts[@]}"; do :; done ;;
            *) fail "容器发布了多种数据库端口，请指定 mysql、redis 或 postgres。" ;;
        esac
    fi
    [[ -n ${hosts[$protocol]:-} ]] || fail "此容器未发布 $protocol 的标准容器端口。"
    host=${hosts[$protocol]}; port=${ports[$protocol]}
    case $protocol in
        mysql) exec mysql --protocol=TCP --host="$host" --port="$port" --password "$@" ;;
        redis) exec redis-cli -h "$host" -p "$port" "$@" ;;
        postgres) exec psql -h "$host" -p "$port" "$@" ;;
    esac
}

command=${0##*/}
case $command in
    java|mvn|python3)
        # A system package installed after our optional symlink takes priority.
        native=/usr/bin/$command
        if [[ -x $native && ! $native -ef $0 ]]; then exec "$native" "$@"; fi
        tool "$command" "" "$@" ;;
esac
command=${1:-help}
if (( $# )); then shift; fi
case $command in
    help|--help|-h) usage ;;
    list)
        (( $# == 0 )) || fail "用法：sf-docker list"
        "${docker[@]}" ps -a --format 'table {{.Names}}\t{{.ID}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}'
        exec "${docker[@]}" image ls --format 'table {{.Repository}}:{{.Tag}}\t{{.ID}}' ;;
    ports|logs)
        (( $# == 1 )) || fail "用法：sf-docker $command 容器"
        container_name "$1"
        if [[ $command == ports ]]; then exec "${docker[@]}" port "$1"; fi
        exec "${docker[@]}" logs --tail 100 "$1" ;;
    exec)
        (( $# >= 2 )) || fail "用法：sf-docker exec 容器 命令 [参数...]"
        container_name "$1"
        exec "${docker[@]}" exec "${stdio[@]}" "$@" ;;
    connect)
        (( $# >= 1 )) || fail "用法：sf-docker connect 容器 [mysql|redis|postgres] [客户端参数...]"
        connect "$@" ;;
    java|mvn|python3) tool "$command" "" "$@" ;;
    tool)
        (( $# >= 2 )) || fail "用法：sf-docker tool 工具 版本 [参数...]"
        tool "$@" ;;
    run)
        (( $# >= 2 )) && [[ -n $1 && $1 != -* && -n $2 ]] || fail "用法：sf-docker run 本机镜像 命令 [参数...]"
        image=$("${docker[@]}" image inspect --format '{{.Id}}' "$1")
        [[ $image =~ ^sha256:[0-9a-f]{64}$ ]] || fail "无法确认本机镜像 ID。"
        shift; program=$1; shift
        run_image "$image" "$program" no "$@" ;;
    *) usage >&2; exit 2 ;;
esac
