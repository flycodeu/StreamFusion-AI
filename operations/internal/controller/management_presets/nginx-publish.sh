[[ "$OPS_PARAM_SERVER_NAME" =~ ^([a-zA-Z0-9*][a-zA-Z0-9*.-]*|_)$ ]] || { echo '域名格式无效'; exit 1; }
[[ "$OPS_PARAM_PORT" =~ ^[0-9]+$ && "$OPS_PARAM_PORT" -ge 1 && "$OPS_PARAM_PORT" -le 65535 ]] || exit 1
listen=$OPS_PARAM_PORT
if [[ "$OPS_RUNTIME_KIND" == DOCKER ]]; then
  mapfile -t mapping < <(docker inspect --format '{{range $port,$bindings := .NetworkSettings.Ports}}{{range $bindings}}{{printf "%s|%s|%s\n" $port .HostIp .HostPort}}{{end}}{{end}}' "$OPS_SOFTWARE_LOCATOR" | awk -F'|' -v bind="$OPS_PARAM_BIND_ADDRESS" -v port="$OPS_PARAM_PORT" '$2==bind && $3==port {print $1}')
  [[ ${#mapping[@]} == 1 && "${mapping[0]}" =~ ^([0-9]{1,5})/tcp$ ]] || { echo '请填写该容器已有的服务器监听地址和端口'; exit 1; }
  listen=${BASH_REMATCH[1]}
else
  [[ "$OPS_PARAM_BIND_ADDRESS" =~ ^[0-9.]+$ ]] || exit 1
  listen="$OPS_PARAM_BIND_ADDRESS:$listen"
fi
if [[ "$OPS_PARAM_SERVER_NAME" == _ ]]; then listen="$listen default_server"; fi
{
  printf '# ops-port %s\nserver {\n listen %s;\n server_name %s;\n' "$OPS_PARAM_PORT" "$listen" "$OPS_PARAM_SERVER_NAME"
  printf ' location = /__sfops_health { default_type text/plain; return 200 "%s"; }\n' "$OPS_SOFTWARE_ID"
  if [[ -n "${OPS_PARAM_STATIC_ROOT:-}" ]]; then
    [[ "$OPS_PARAM_STATIC_ROOT" =~ ^/[a-zA-Z0-9_./-]+$ && "$OPS_PARAM_STATIC_ROOT" != *'/../'* && -d "$OPS_PARAM_STATIC_ROOT" ]] || { echo '静态文件目录不可用'; exit 1; }
    root=$(realpath "$OPS_PARAM_STATIC_ROOT")
    if [[ "$OPS_RUNTIME_KIND" == DOCKER ]]; then
      mapped=''
      longest=0
      while IFS='|' read -r source destination; do
        if [[ ( "$root" == "$source" || "$root" == "$source/"* ) && ${#source} -gt "$longest" ]]; then mapped="$destination${root#"$source"}"; longest=${#source}; fi
      done < <(docker inspect --format '{{range .Mounts}}{{printf "%s|%s\n" .Source .Destination}}{{end}}' "$OPS_SOFTWARE_LOCATOR")
      [[ -n "$mapped" && "$mapped" =~ ^/[a-zA-Z0-9_./-]+$ ]] || { echo '静态目录必须位于该容器已有的挂载目录中'; exit 1; }
      root=$mapped
      docker exec --user nginx "$OPS_SOFTWARE_LOCATOR" test -d "$root"
    fi
    [[ "$OPS_PARAM_INDEX" =~ ^[a-zA-Z0-9_.-]+$ && "$OPS_PARAM_INDEX" != .* ]] || exit 1
    if [[ "$OPS_RUNTIME_KIND" == DOCKER ]]; then
      docker exec --user nginx "$OPS_SOFTWARE_LOCATOR" test -r "$root/$OPS_PARAM_INDEX" || { echo 'Nginx无法读取首页文件'; exit 1; }
    else
      worker=nobody
      if [[ "$official" == true ]]; then worker=$(awk '$1=="user" {gsub(";","",$2); print $2; exit}' /etc/nginx/nginx.conf); fi
      [[ "$worker" =~ ^[a-zA-Z_][a-zA-Z0-9_-]*$ ]] && runuser -u "$worker" -- test -r "$root/$OPS_PARAM_INDEX" || { echo 'Nginx无法读取首页文件'; exit 1; }
    fi
    fallback='=404'
    [[ "$OPS_PARAM_SPA" != true ]] || fallback="/$OPS_PARAM_INDEX"
    printf ' root %s;\n index %s;\n location / { try_files $uri $uri/ %s; }\n' "$root" "$OPS_PARAM_INDEX" "$fallback"
  else
    [[ "$OPS_PARAM_UPSTREAM" =~ ^https?://[][a-zA-Z0-9_.:/-]+$ && "$OPS_PARAM_LOCATION" =~ ^/[a-zA-Z0-9_./-]*$ ]] || { echo '后端地址或代理路径格式无效'; exit 1; }
    [[ "$OPS_PARAM_READ_TIMEOUT" =~ ^[0-9]+$ && "$OPS_PARAM_READ_TIMEOUT" -ge 1 && "$OPS_PARAM_READ_TIMEOUT" -le 3600 ]] || exit 1
    upstream=${OPS_PARAM_UPSTREAM%/}
    [[ "$OPS_PARAM_STRIP_PREFIX" != true ]] || upstream="$upstream/"
    printf ' location %s {\n proxy_pass %s;\n proxy_http_version 1.1;\n proxy_set_header Host $host;\n proxy_set_header X-Real-IP $remote_addr;\n proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n proxy_set_header X-Forwarded-Proto $scheme;\n proxy_read_timeout %ss;\n' "$OPS_PARAM_LOCATION" "$upstream" "$OPS_PARAM_READ_TIMEOUT"
    [[ "$OPS_PARAM_WEBSOCKET" != true ]] || printf ' proxy_set_header Upgrade $http_upgrade;\n proxy_set_header Connection "upgrade";\n'
    [[ "$OPS_PARAM_SSE" != true ]] || printf ' proxy_buffering off;\n proxy_cache off;\n gzip off;\n'
    printf ' }\n'
  fi
  printf '}\n'
} > "$stage/site.new"
cat "$stage/site.new" > "$site"
rm -f -- "$disabled"
render
