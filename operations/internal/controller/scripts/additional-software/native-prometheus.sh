check_owner
create_service_user
[[ "$OPS_PARAM_RETENTION" =~ ^[1-9][0-9]{0,4}[dhwy]$ ]] || fail '保留时间请使用 15d、4w 等格式'
self_address=$OPS_BIND_ADDRESS
[[ "$self_address" != 0.0.0.0 ]] || self_address=127.0.0.1
cat > "$root/config/prometheus.yml" <<EOF
global:
  scrape_interval: 15s
scrape_configs:
  - job_name: prometheus
    static_configs:
      - targets: ['$self_address:$OPS_HOST_PORT']
EOF
"$root/runtime/promtool" check config "$root/config/prometheus.yml"
chown root:"$(id -gn "$service_user")" "$root/config/prometheus.yml"
chmod 640 "$root/config/prometheus.yml"
cat > "$root/bin/run" <<EOF
#!/bin/bash
exec '$root/runtime/prometheus' --config.file='$root/config/prometheus.yml' --storage.tsdb.path='$root/data' --storage.tsdb.retention.time='$OPS_PARAM_RETENTION' --web.listen-address='$OPS_BIND_ADDRESS:$OPS_HOST_PORT'
EOF
write_unit
