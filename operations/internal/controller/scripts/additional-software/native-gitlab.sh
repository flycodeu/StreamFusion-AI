check_owner
[[ "$OPS_PARAM_EXTERNAL_URL" =~ ^http://[A-Za-z0-9.-]+(:[0-9]{1,5})?/?$ ]] || fail '访问地址请填写 http://域名:端口；HTTPS 请在安装后配置证书与反向代理'
url_port=80; [[ "$OPS_PARAM_EXTERNAL_URL" != *:*:* ]] || url_port="${OPS_PARAM_EXTERNAL_URL##*:}"; url_port="${url_port%/}"
[[ "$url_port" == "$OPS_HOST_PORT" ]] || fail '访问地址的端口须与 HTTP 安装端口一致'
password="$(read_secret "$OPS_SECRET_ROOT_PASSWORD_FILE")"
[[ ${#password} -ge 8 ]] || fail 'GitLab root 密码至少需要 8 个字符'
for path in /etc/gitlab /var/opt/gitlab /var/log/gitlab /opt/gitlab; do [[ ! -e "$path" && ! -L "$path" ]] || fail "发现已有 GitLab 目录：$path"; done
install -d -m 700 /etc/gitlab
printf '%s' "$password" > /etc/gitlab/ops-initial-password
chmod 600 /etc/gitlab/ops-initial-password
cat > /etc/gitlab/gitlab.rb <<EOF
external_url '$OPS_PARAM_EXTERNAL_URL'
nginx['listen_addresses'] = ['$OPS_BIND_ADDRESS']
nginx['listen_port'] = $OPS_HOST_PORT
nginx['listen_https'] = false
gitlab_rails['gitlab_shell_ssh_port'] = $OPS_PARAM_SSH_PORT
gitlab_rails['initial_root_password'] = File.read('/etc/gitlab/ops-initial-password')
letsencrypt['enable'] = false
EOF
chmod 600 /etc/gitlab/gitlab.rb
printf '%s\n' 'GitLab system paths' > "$root/config/system-paths-owned"
install_packages
[[ "$(dpkg-query -W -f='${Version}' gitlab-ce)" == 19.5.3-ce.0 ]] || fail 'GitLab 安装版本不符'
/opt/gitlab/bin/gitlab-ctl reconfigure
systemctl enable --now gitlab-runsvdir.service
for path in /etc/gitlab /var/opt/gitlab /var/log/gitlab; do
  [[ -d "$path" && ! -L "$path" ]] || fail "GitLab 数据目录不符合预期：$path"
  printf '%s %s\n' "$path" "$(stat -c '%d:%i' "$path")"
done > "$root/config/system-paths-owned"
unset password
