check_owner
python=$(cat "$root/config/python-binary")
group=$(id -gn "$service_user")
"$python" -I - "$root/config/ytlabel.json" <<'PY'
import json, os, sys
from ipaddress import IPv4Address
host = os.environ['OPS_BIND_ADDRESS']
IPv4Address(host)
port = int(os.environ['OPS_HOST_PORT'])
size = int(os.environ['OPS_PARAM_MAX_UPLOAD_MIB'])
if not 1 <= port <= 65535 or not 1 <= size <= 16384:
    sys.exit('端口或上传大小超出范围')
with open(sys.argv[1], 'x', encoding='utf8') as output:
    json.dump({'host': host, 'port': port, 'maxUploadMiB': size, 'dataDirectory': os.path.join(os.environ['OPS_INSTALL_ROOT'], 'data'), 'sourceDirectory': os.environ.get('OPS_PARAM_SOURCE_DIRECTORY', '')}, output)
PY
cat > "$root/bin/ytlabel-launch.py" <<'PY'
@@LAUNCH@@
PY
cat > "$root/bin/run" <<EOF
#!/bin/bash
exec '$root/runtime/venv/bin/python' -I '$root/bin/ytlabel-launch.py'
EOF
unit_path="/etc/systemd/system/$unit"
[[ ! -e "$unit_path" && ! -L "$unit_path" ]] || fail '服务单元已存在'
source_directory=${OPS_PARAM_SOURCE_DIRECTORY:-}
if [[ -n "$source_directory" ]]; then
  [[ -d "$source_directory" && "$(readlink -f "$source_directory")" == "$source_directory" && "$source_directory" =~ ^/[A-Za-z0-9_./-]+$ ]] || fail '素材目录已变化或不可读取'
  [[ ! -e "$root/data/uploads/samples" ]] || fail '图片导入位置已存在'
  install -d -m 750 -o "$service_user" -g "$group" "$root/data/uploads" "$root/data/uploads/samples"
  annotation_manage import-source "$source_directory" <<'PY'
import_samples(root, root/'data', Path(sys.argv[5]))
PY
fi
cat > "$unit_path" <<EOF
[Unit]
Description=Ops ytlabel $OPS_SOFTWARE_ID
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
User=$service_user
Group=$group
WorkingDirectory=$root/data
ExecStart=/bin/bash $root/bin/run
Restart=on-failure
RestartSec=5
TimeoutStopSec=60
UMask=0027
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=$root/data $root/logs
MemoryMax=${OPS_MEMORY_MIB}M
LimitNOFILE=65536
EOF
printf '\n[Install]\nWantedBy=multi-user.target\n' >> "$unit_path"
chmod 644 "$unit_path"
chmod 750 "$root/bin" "$root/bin/run" "$root/bin/ytlabel-launch.py"
chown root:"$group" "$root/bin" "$root/bin/run" "$root/bin/ytlabel-launch.py" "$root/config/ytlabel.json"
chmod 640 "$root/config/ytlabel.json"
systemctl daemon-reload
systemctl enable --now "$unit"
