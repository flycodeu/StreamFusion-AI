check_owner
need systemctl
need useradd
if getent passwd "$service_user" >/dev/null; then
  [[ "$(getent passwd "$service_user" | cut -d: -f6)" == "$root" ]] || fail '服务账号已被其他目录使用'
else
  useradd --system --home-dir "$root" --no-create-home --shell /usr/sbin/nologin "$service_user"
fi
group=$(id -gn "$service_user")
chown "$service_user:$group" "$root/data" "$root/logs"
chown root:"$group" "$root" "$root/config"
chmod 750 "$root" "$root/config" "$root/data" "$root/logs"
if [[ -n "$OPS_PARAM_SOURCE_DIRECTORY" ]]; then
  runuser -u "$service_user" -- test -r "$OPS_PARAM_SOURCE_DIRECTORY" && runuser -u "$service_user" -- test -x "$OPS_PARAM_SOURCE_DIRECTORY" || fail "服务账号 $service_user 不能读取数据集目录，请由管理员分配读取权限"
fi
{
  printf 'export LABEL_STUDIO_BASE_DATA_DIR=%q\n' "$root/data"
  printf 'export DATA_UPLOAD_MAX_MEMORY_SIZE=%q\n' "$((OPS_PARAM_MAX_UPLOAD_MIB*1024*1024))"
  printf 'export LABEL_STUDIO_HOST=%q\n' "$OPS_PARAM_PUBLIC_URL"
  printf 'export LABEL_STUDIO_LOCAL_FILES_SERVING_ENABLED=%q\n' "$([[ -n "$OPS_PARAM_SOURCE_DIRECTORY" ]] && printf true || printf false)"
  printf 'export LABEL_STUDIO_LOCAL_FILES_DOCUMENT_ROOT=%q\n' "${OPS_PARAM_SOURCE_DIRECTORY:-$root/data}"
  printf 'export PYTHONDONTWRITEBYTECODE=1\nexport HOME=%q\n' "$root/data"
} > "$root/config/runtime.env"
chmod 640 "$root/config/runtime.env"
chown root:"$group" "$root/config/runtime.env"
{
  printf '#!/bin/bash\nset -euo pipefail\nsource %q\n' "$root/config/runtime.env"
  printf 'exec %q start --no-browser --internal-host %q --port %q\n' "$root/runtime/venv/bin/label-studio" "$OPS_BIND_ADDRESS" "$OPS_HOST_PORT"
} > "$root/bin/run"
chmod 755 "$root/bin/run"
path="/etc/systemd/system/$unit"
[[ ! -e "$path" && ! -L "$path" ]] || fail '服务单元已存在，请核对原任务'
readonly_directive=''
if [[ -n "$OPS_PARAM_SOURCE_DIRECTORY" ]]; then readonly_directive="ReadOnlyPaths=$OPS_PARAM_SOURCE_DIRECTORY"; fi
cat > "$path" <<EOF
[Unit]
Description=Ops Label Studio $OPS_SOFTWARE_ID
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
User=$service_user
WorkingDirectory=$root
ExecStart=/bin/bash $root/bin/run
Restart=on-failure
RestartSec=5
TimeoutStopSec=60
MemoryMax=${OPS_MEMORY_MIB}M
NoNewPrivileges=true
PrivateTmp=true
UMask=0027
$readonly_directive
[Install]
WantedBy=multi-user.target
EOF
chmod 644 "$path"
systemctl daemon-reload
systemctl enable --now "$unit"
