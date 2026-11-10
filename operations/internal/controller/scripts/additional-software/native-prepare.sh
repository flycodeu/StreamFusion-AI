need systemctl
need curl
need sha256sum
need tar
need useradd
[[ "$OPS_MEMORY_MIB" -ge @@MEMORY@@ ]] || fail '此软件至少需要 @@MEMORY@@ MiB 安装内存'
[[ ! -e "$root/config/ops-owner" ]] || fail '已有安装记录，请先核对原任务'
if [[ "$app" == postgresql || "$app" == gitlab ]]; then
  . /etc/os-release
  [[ "$ID" == ubuntu && "$VERSION_ID" == 24.04 && "$(dpkg --print-architecture)" == amd64 ]] || fail '此原生系统包方案支持 Ubuntu 24.04 amd64；其他系统可使用 Docker 或自行维护模板'
  dpkg --audit > "$root/work/package-audit" || fail '无法读取系统软件包状态，请先核对包管理器'
  [[ ! -s "$root/work/package-audit" ]] || fail '系统有未完成的软件包事务，请先修复后继续'
  if [[ "$app" == postgresql ]]; then
    validate_cidr "$OPS_PARAM_ALLOWED_CIDR"
    [[ "$OPS_PARAM_DB_USER" =~ ^[a-z_][a-z0-9_]{0,31}$ && "$OPS_PARAM_DB_NAME" =~ ^[a-z_][a-z0-9_]{0,62}$ ]] || fail '数据库和管理员名称请使用小写字母、数字或下划线'
    [[ -z "$(dpkg-query -W -f='${binary:Package} ${Status}\n' 'postgresql-[0-9]*' 2>/dev/null | grep 'install ok installed' || true)" ]] || fail '已存在 PostgreSQL 系统包，请登记已有服务或使用独立 Docker 安装'
    for path in /etc/postgresql/18 /var/lib/postgresql/18; do [[ ! -e "$path" && ! -L "$path" ]] || fail "发现已有目录：$path"; done
  else
    [[ "$(dpkg-query -W -f='${Status}' gitlab-ce gitlab-ee 2>/dev/null || true)" != *'install ok installed'* ]] || fail 'GitLab 系统包已安装，请登记已有服务'
    for path in /etc/gitlab /var/opt/gitlab /var/log/gitlab /opt/gitlab; do [[ ! -e "$path" && ! -L "$path" ]] || fail "发现已有 GitLab 目录：$path"; done
  fi
fi
if [[ "$app" == jenkins || "$app" == nacos ]]; then
  [[ -x "$OPS_PARAM_JAVA_BINARY" ]] || fail '所选 Java 可执行文件不存在，请先安装需要的 Java 版本'
  java="$(readlink -f "$OPS_PARAM_JAVA_BINARY")"
  [[ "$java" == /* && "$java" != *[[:space:]]* && "$java" != *\"* && "$java" != *\'* ]] || fail 'Java 路径格式不支持'
  major="$("$java" -version 2>&1 | sed -nE 's/.*version "([0-9]+).*/\1/p' | head -1)"
  if [[ "$app" == jenkins ]]; then [[ "$major" == 21 || "$major" == 25 ]] || fail 'Jenkins 2.580 需要 Java 21 或 25'; else [[ "$major" =~ ^[0-9]+$ && "$major" -ge 17 ]] || fail 'Nacos 3.3 需要 Java 17 以上'; fi
  printf '%s\n' "$java" > "$root/config/java-binary"
fi
record_owner
