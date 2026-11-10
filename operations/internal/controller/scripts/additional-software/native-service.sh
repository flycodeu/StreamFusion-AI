create_service_user() {
  if getent passwd "$service_user" >/dev/null; then
    [[ "$(getent passwd "$service_user" | cut -d: -f6)" == "$root" ]] || fail '服务账号已被其他目录使用'
  else
    useradd --system --home-dir "$root" --no-create-home --shell /usr/sbin/nologin "$service_user"
  fi
  chown "$service_user:" "$root/data" "$root/logs"
  chown root:"$(id -gn "$service_user")" "$root"
  chmod 750 "$root" "$root/data" "$root/logs"
}
write_unit() {
  local path="/etc/systemd/system/$unit"
  [[ ! -e "$path" && ! -L "$path" ]] || fail '服务单元已存在'
  cat > "$path" <<EOF
[Unit]
Description=Ops $app $OPS_SOFTWARE_ID
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
User=$service_user
WorkingDirectory=$root
ExecStart=/bin/bash $root/bin/run
Restart=on-failure
RestartSec=5
TimeoutStopSec=90
UMask=0027
NoNewPrivileges=true
PrivateTmp=true
LimitNOFILE=65536
MemoryMax=${OPS_MEMORY_MIB}M
[Install]
WantedBy=multi-user.target
EOF
  chmod 644 "$path"
  chmod 755 "$root/bin" "$root/bin/run"
  chmod 750 "$root/config"
  chown root:"$(id -gn "$service_user")" "$root" "$root/config"
  systemctl daemon-reload
  systemctl enable --now "$unit"
}
