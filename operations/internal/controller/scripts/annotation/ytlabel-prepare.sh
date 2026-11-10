for command in systemctl tar sha256sum useradd runuser findmnt curl readlink; do need "$command"; done
[[ ! -e "$root/config/ops-owner" ]] || fail '已有安装记录，请先核对原任务'
systemd_version=$(systemctl --version | head -n 1 | awk '{print $2}')
[[ "$systemd_version" =~ ^[0-9]+$ && "$systemd_version" -ge 239 ]] || fail 'ytlabel 隔离运行需要 systemd 239 以上'
python=$(readlink -f "$OPS_PARAM_PYTHON_BINARY")
case "$python" in /home/*|/root/*|/run/user/*) fail '服务隔离禁止使用用户主目录中的 Python，请选择系统或 Ops 运行目录的解释器';; esac
[[ -x "$python" && "$python" =~ ^/[A-Za-z0-9_./-]+$ ]] || fail '请选择可执行的 Python 3.9 或 3.10'
"$python" -I - <<'PY'
import sys
if sys.version_info[:2] not in [(3, 9), (3, 10)]:
    sys.exit('上游 OpenCV 固定依赖需要 Python 3.9 或 3.10，请选择对应解释器路径')
try:
    import venv, ensurepip
except ImportError:
    sys.exit('所选 Python 缺少 venv/ensurepip，请先安装对应的 python-venv 系统组件')
from ctypes import CDLL
for name in ['libGL.so.1', 'libglib-2.0.so.0', 'libgthread-2.0.so.0']:
    try:
        CDLL(name)
    except OSError:
        sys.exit('上游 opencv-python 缺少系统库 ' + name + '，请先由管理员安装对应系统依赖')
PY
[[ "$OPS_PARAM_MAX_UPLOAD_MIB" =~ ^[0-9]+$ && "$OPS_PARAM_MAX_UPLOAD_MIB" -ge 1 && "$OPS_PARAM_MAX_UPLOAD_MIB" -le 16384 ]] || fail '上传上限须为 1 至 16384 MiB'
source_directory=${OPS_PARAM_SOURCE_DIRECTORY:-}
if [[ -n "$source_directory" ]]; then
  [[ "$source_directory" =~ ^/[A-Za-z0-9_./-]+$ && "$source_directory" != / && -d "$source_directory" && "$(readlink -f "$source_directory")" == "$source_directory" ]] || fail '素材目录须为已存在且不含符号链接的完整目录'
  [[ "$source_directory" != "$root" && "$source_directory" != "$root/"* && "$root" != "$source_directory/"* ]] || fail '只读素材目录不能与本实例安装目录重叠'
fi
record_owner
create_service_user
runuser -u "$service_user" -- "$python" -I -c 'import venv, ensurepip' || fail '服务账号无法读取所选 Python，请选择可读的系统或 Ops 运行目录解释器'
group=$(id -gn "$service_user")
chown root:"$group" "$root/config"
chmod 750 "$root/config"
printf '%s\n' "$python" > "$root/config/python-binary"
chmod 640 "$root/config/python-binary"
chown root:"$group" "$root/config/python-binary"
printf '%s\n' 'ytlabel 上游无登录认证，请在受限网络使用。'
