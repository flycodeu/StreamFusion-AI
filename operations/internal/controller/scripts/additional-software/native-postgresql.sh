check_owner
[[ "$OPS_PARAM_DB_USER" =~ ^[a-z_][a-z0-9_]{0,31}$ && "$OPS_PARAM_DB_NAME" =~ ^[a-z_][a-z0-9_]{0,62}$ ]] || fail '数据库和管理员名称请使用小写字母、数字或下划线'
validate_cidr "$OPS_PARAM_ALLOWED_CIDR"
# Suppress the distribution's automatic shared main cluster. Restore the exact original config afterwards.
cluster_config=/etc/postgresql-common/createcluster.conf
install -d -m 755 /etc/postgresql-common
[[ ! -L "$cluster_config" ]] || fail 'PostgreSQL 初始化配置是链接，请管理员先核对'
cluster_config_existed=false
if [[ -f "$cluster_config" ]]; then cp -p "$cluster_config" "$root/work/createcluster.conf.before"; cluster_config_existed=true; fi
restore_cluster_config() { if [[ "$cluster_config_existed" == true ]]; then cp -p "$root/work/createcluster.conf.before" "$cluster_config"; else rm -f -- "$cluster_config"; fi; }
trap restore_cluster_config EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
printf '\ncreate_main_cluster = false\n' >> "$cluster_config"
chmod 644 "$cluster_config"
install_packages
restore_cluster_config
trap - EXIT
trap - HUP INT TERM
[[ "$(/usr/lib/postgresql/18/bin/postgres --version)" == *'18.6'* ]] || fail 'PostgreSQL 安装版本不符'
create_service_user
password="$(read_secret "$OPS_SECRET_DB_PASSWORD_FILE")"
trap 'rm -f -- "$root/config/init-password" "$root/config/init-pgpass"' EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
printf '%s\n' "$password" > "$root/config/init-password"
chown "$service_user:" "$root/config/init-password"
chmod 600 "$root/config/init-password"
chown root:"$(id -gn "$service_user")" "$root/config"
chmod 750 "$root/config"
runuser -u "$service_user" -- /usr/lib/postgresql/18/bin/initdb -D "$root/data" -U "$OPS_PARAM_DB_USER" --auth-local=scram-sha-256 --auth-host=scram-sha-256 --pwfile="$root/config/init-password" --encoding=UTF8 --locale=C.UTF-8
rm -f -- "$root/config/init-password"
install -d -o "$service_user" -g "$(id -gn "$service_user")" -m 700 "$root/data/run"
cat > "$root/config/postgresql.conf" <<EOF
data_directory = '$root/data'
hba_file = '$root/config/pg_hba.conf'
ident_file = '$root/data/pg_ident.conf'
listen_addresses = '$OPS_BIND_ADDRESS'
port = $OPS_HOST_PORT
unix_socket_directories = '$root/data/run'
password_encryption = 'scram-sha-256'
max_connections = 100
shared_buffers = '$((OPS_MEMORY_MIB/4))MB'
EOF
printf 'local all all scram-sha-256\nhost all all %s scram-sha-256\n' "$OPS_PARAM_ALLOWED_CIDR" > "$root/config/pg_hba.conf"
chown root:"$(id -gn "$service_user")" "$root/config/postgresql.conf" "$root/config/pg_hba.conf"
chmod 640 "$root/config/postgresql.conf" "$root/config/pg_hba.conf"
cat > "$root/bin/run" <<EOF
#!/bin/bash
exec /usr/lib/postgresql/18/bin/postgres -D '$root/data' -c config_file='$root/config/postgresql.conf'
EOF
write_unit
for attempt in {1..30}; do /usr/lib/postgresql/18/bin/pg_isready -h "$root/data/run" -p "$OPS_HOST_PORT" >/dev/null && break; sleep 2; done
escaped="${password//\\/\\\\}"; escaped="${escaped//:/\\:}"
printf '*:*:*:%s:%s\n' "$OPS_PARAM_DB_USER" "$escaped" > "$root/config/init-pgpass"
chown "$service_user:" "$root/config/init-pgpass"; chmod 600 "$root/config/init-pgpass"
runuser -u "$service_user" -- env "PGPASSFILE=$root/config/init-pgpass" /usr/lib/postgresql/18/bin/createdb -h "$root/data/run" -p "$OPS_HOST_PORT" -U "$OPS_PARAM_DB_USER" "$OPS_PARAM_DB_NAME"
rm -f -- "$root/config/init-pgpass"
trap - EXIT
trap - HUP INT TERM
unset password escaped
