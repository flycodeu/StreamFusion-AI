record_owner
need curl
args=(run --detach --name "$container" --label "streamfusion.ops.software-id=$OPS_SOFTWARE_ID" --restart unless-stopped --memory "${OPS_MEMORY_MIB}m")
case "$app" in
  jenkins)
    chown 1000:1000 "$root/data"
    chmod 750 "$root/data"
    docker "${args[@]}" -p "$OPS_BIND_ADDRESS:$OPS_HOST_PORT:8080" -v "$root/data:/var/jenkins_home" '@@IMAGE_ID@@'
    printf '%s\n' '首次打开 Jenkins 后按官方设置向导创建管理员；初始密码保存在本次数据目录 secrets/initialAdminPassword'
    ;;
  postgresql)
    [[ "$OPS_PARAM_DB_USER" =~ ^[a-z_][a-z0-9_]{0,31}$ && "$OPS_PARAM_DB_NAME" =~ ^[a-z_][a-z0-9_]{0,62}$ ]] || fail '数据库和管理员名称请使用小写字母、数字或下划线'
    password="$(read_secret "$OPS_SECRET_DB_PASSWORD_FILE")"
    printf '%s\n' "$password" > "$root/config/db-password"
    chmod 600 "$root/config/db-password"
    docker "${args[@]}" -p "$OPS_BIND_ADDRESS:$OPS_HOST_PORT:5432" -e "POSTGRES_USER=$OPS_PARAM_DB_USER" -e "POSTGRES_DB=$OPS_PARAM_DB_NAME" -e POSTGRES_PASSWORD_FILE=/run/secrets/db-password -e PGDATA=/var/lib/postgresql/18/docker -v "$root/config/db-password:/run/secrets/db-password:ro" -v "$root/data:/var/lib/postgresql" '@@IMAGE_ID@@'
    unset password
    ;;
  gitlab)
    [[ "$OPS_PARAM_EXTERNAL_URL" =~ ^http://[A-Za-z0-9.-]+(:[0-9]{1,5})?/?$ ]] || fail '访问地址请填写 http://域名:端口；HTTPS 请在安装后配置证书与反向代理'
    url_port=80; [[ "$OPS_PARAM_EXTERNAL_URL" != *:*:* ]] || url_port="${OPS_PARAM_EXTERNAL_URL##*:}"; url_port="${url_port%/}"
    [[ "$url_port" == "$OPS_HOST_PORT" ]] || fail '访问地址的端口须与 HTTP 安装端口一致'
    password="$(read_secret "$OPS_SECRET_ROOT_PASSWORD_FILE")"
    [[ ${#password} -ge 8 ]] || fail 'GitLab root 密码至少需要 8 个字符'
    install -d -m 700 "$root/data/gitlab-config" "$root/data/gitlab-data" "$root/logs/gitlab"
    printf '%s' "$password" > "$root/data/gitlab-config/ops-initial-password"
    cat > "$root/data/gitlab-config/gitlab.rb" <<EOF
external_url '$OPS_PARAM_EXTERNAL_URL'
nginx['listen_port'] = 80
nginx['listen_https'] = false
gitlab_rails['gitlab_shell_ssh_port'] = $OPS_PORT_SSH
gitlab_rails['initial_root_password'] = File.read('/etc/gitlab/ops-initial-password')
letsencrypt['enable'] = false
EOF
    chmod 600 "$root/data/gitlab-config/"*
    docker "${args[@]}" --shm-size 256m -p "$OPS_BIND_ADDRESS:$OPS_HOST_PORT:80" -p "$OPS_BIND_ADDRESS:$OPS_PORT_SSH:22" -v "$root/data/gitlab-config:/etc/gitlab" -v "$root/data/gitlab-data:/var/opt/gitlab" -v "$root/logs/gitlab:/var/log/gitlab" '@@IMAGE_ID@@'
    unset password
    ;;
  prometheus)
    [[ "$OPS_PARAM_RETENTION" =~ ^[1-9][0-9]{0,4}[dhwy]$ ]] || fail '保留时间请使用 15d、4w 等格式'
    cat > "$root/config/prometheus.yml" <<EOF
global:
  scrape_interval: 15s
scrape_configs:
  - job_name: prometheus
    static_configs:
      - targets: ['127.0.0.1:9090']
EOF
    chmod 644 "$root/config/prometheus.yml"
    chown 65534:65534 "$root/data"
    chmod 750 "$root/data"
    docker "${args[@]}" -p "$OPS_BIND_ADDRESS:$OPS_HOST_PORT:9090" -v "$root/config/prometheus.yml:/etc/prometheus/prometheus.yml:ro" -v "$root/data:/prometheus" '@@IMAGE_ID@@' --config.file=/etc/prometheus/prometheus.yml --storage.tsdb.path=/prometheus --storage.tsdb.retention.time="$OPS_PARAM_RETENTION" --web.listen-address=0.0.0.0:9090
    ;;
  nacos)
    [[ "$OPS_PORT_GRPC" -eq $((OPS_HOST_PORT+1000)) && "$OPS_PORT_CLUSTER_GRPC" -eq $((OPS_HOST_PORT+1001)) ]] || fail 'Nacos gRPC 端口须分别为主端口 +1000、+1001'
    token="$(read_secret "$OPS_SECRET_AUTH_TOKEN_FILE")"; identity="$(read_secret "$OPS_SECRET_AUTH_IDENTITY_FILE")"
    [[ "$token" =~ ^[A-Za-z0-9+/]+=*$ && $(printf '%s' "$token" | base64 --decode | wc -c) -ge 32 ]] || fail 'Token 签名密钥需要 Base64 编码，解码后至少 32 字节'
    printf 'NACOS_AUTH_TOKEN=%s\nNACOS_AUTH_IDENTITY_KEY=ops-server\nNACOS_AUTH_IDENTITY_VALUE=%s\n' "$token" "$identity" > "$root/config/nacos.env"
    chmod 600 "$root/config/nacos.env"
    chown 1000:1000 "$root/data" "$root/logs"
    docker "${args[@]}" --env-file "$root/config/nacos.env" -e MODE=standalone -e NACOS_AUTH_ENABLE=true -e NACOS_AUTH_ADMIN_ENABLE=true -e NACOS_AUTH_CONSOLE_ENABLE=true -e JVM_XMS=256m -e "JVM_XMX=$((OPS_MEMORY_MIB*3/4))m" -p "$OPS_BIND_ADDRESS:$OPS_HOST_PORT:8848" -p "$OPS_BIND_ADDRESS:$OPS_PORT_CONSOLE:8080" -p "$OPS_BIND_ADDRESS:$OPS_PORT_GRPC:9848" -p "$OPS_BIND_ADDRESS:$OPS_PORT_CLUSTER_GRPC:9849" -v "$root/data:/home/nacos/data" -v "$root/logs:/home/nacos/logs" '@@IMAGE_ID@@'
    unset token identity
    ;;
esac
