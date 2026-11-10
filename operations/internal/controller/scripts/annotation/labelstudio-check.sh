need curl
need sha256sum
if [[ "$OPS_RUNTIME_MODE" == NATIVE ]]; then
  need systemctl
  systemd_version=$(systemctl --version | awk 'NR==1 {print $2}')
  [[ "$systemd_version" =~ ^[0-9]+$ && "$systemd_version" -ge 231 ]] || fail '原生安装需要 systemd 231 以上，以应用内存与只读目录配置；此主机可选择 Docker 方式'
fi
[[ "$OPS_PARAM_MAX_UPLOAD_MIB" =~ ^[0-9]+$ && "$OPS_PARAM_MAX_UPLOAD_MIB" -ge 1 && "$OPS_PARAM_MAX_UPLOAD_MIB" -le 2048 ]] || fail '上传上限须为 1 至 2048 MiB'
[[ ! -e "$root/config/ops-owner" ]] || fail '已有安装记录，请核对原任务'
if [[ -n "$OPS_PARAM_SOURCE_DIRECTORY" ]]; then
  [[ "$OPS_PARAM_SOURCE_DIRECTORY" =~ ^/[A-Za-z0-9_./-]+$ ]] || fail '数据集目录请使用绝对路径，仅含字母、数字、下划线、横线、点和斜线'
  source_path=$(realpath -e "$OPS_PARAM_SOURCE_DIRECTORY") || fail '只读数据集目录不存在'
  [[ -d "$source_path" && "$source_path" == "$OPS_PARAM_SOURCE_DIRECTORY" && "$source_path" != / ]] || fail '数据集目录必须是实际目录，不能是符号链接'
  [[ "$source_path" != "$root" && "$source_path" != "$root/"* && "$root" != "$source_path/"* ]] || fail '只读数据集目录不能与本次安装目录重叠'
  case "$source_path" in /etc|/etc/*|/proc|/proc/*|/sys|/sys/*|/dev|/dev/*|/root|/root/*|/run|/run/*|/boot|/boot/*|/usr|/usr/*|/bin|/bin/*|/sbin|/sbin/*|/lib|/lib/*|/lib64|/lib64/*) fail '请选择专用数据集目录';; esac
  [[ -r "$source_path" && -x "$source_path" ]] || fail '数据集目录不可读取'
fi
if [[ -n "$OPS_PARAM_PUBLIC_URL" ]]; then
  [[ "$OPS_PARAM_PUBLIC_URL" =~ ^https?://[A-Za-z0-9.-]+(:[0-9]{1,5})?(/[A-Za-z0-9_./%-]*)?$ ]] || fail '访问地址须为不含认证信息的 HTTP 或 HTTPS 地址'
fi
printf '%s\n' "label-studio:$OPS_SOFTWARE_ID" > "$root/config/ops-owner"
printf '%s\n' '安装参数已核对'
