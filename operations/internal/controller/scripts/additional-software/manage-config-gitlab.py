gitlab_config = Path('/etc/gitlab/gitlab.rb') if mode == 'NATIVE' else root/'data/gitlab-config/gitlab.rb'

def app_files():
    return [gitlab_config]

def gitlab_value(key):
    text = read(gitlab_config)
    pattern = r"(?m)^external_url\s+['\"]([^'\"]+)['\"]\s*$" if key == 'external_url' else r'(?m)^' + re.escape(key) + r'\s*=\s*(.+)\s*$'
    values = re.findall(pattern, text)
    if len(values) != 1: fail('GitLab 配置项重复或缺失：' + key)
    return values[0].strip()

def app_actual():
    address = gitlab_value("nginx['listen_addresses']")
    parsed = re.fullmatch(r"\[['\"]([0-9.]+)['\"]\]", address)
    if not parsed: fail('当前 GitLab 监听方式不支持安全修改')
    return dict(BIND_ADDRESS=parsed[1], WEB_PORT=int(gitlab_value("nginx['listen_port']")), EXTERNAL_URL=gitlab_value('external_url'), SSH_PORT=int(gitlab_value("gitlab_rails['gitlab_shell_ssh_port']")))

def app_validate(values):
    if not re.fullmatch(r'http://[A-Za-z0-9.-]+(?::[0-9]{1,5})?/?', values['EXTERNAL_URL']): fail('访问地址请填写 http://域名:端口，HTTPS 请使用现有反向代理')
    url = urllib.parse.urlsplit(values['EXTERNAL_URL'])
    if (url.port or 80) != values['WEB_PORT']: fail('访问地址端口须与 HTTP 服务端口一致')
    try:
        int(gitlab_value("nginx['listen_port']")); int(gitlab_value("gitlab_rails['gitlab_shell_ssh_port']"))
    except ValueError: fail('现有 GitLab 端口配置含自定义表达式，请先核对')
    without_initial_password(read(gitlab_config))

def gitlab_set(text, key, value):
    pattern = r'(?m)^' + re.escape(key) + r'\s*=.*$'
    if len(re.findall(pattern, text)) != 1: fail('GitLab 配置项重复或缺失：' + key)
    return re.sub(pattern, lambda _: key + ' = ' + value, text)

def without_initial_password(text):
    result = []
    for line in text.splitlines(keepends=True):
        if 'initial_root_password' not in line or line.lstrip().startswith('#'):
            result.append(line)
        elif not re.fullmatch(r"gitlab_rails\['initial_root_password'\]\s*=\s*File\.read\('/etc/gitlab/ops-initial-password'\)\s*", line):
            fail('初始密码配置已自定义，请管理员先核对该配置')
    return ''.join(result)

def app_apply(values):
    text = read(gitlab_config)
    int(gitlab_value("nginx['listen_port']")); int(gitlab_value("gitlab_rails['gitlab_shell_ssh_port']"))
    text = re.sub(r"(?m)^external_url\s+['\"][^'\"]+['\"]\s*$", lambda _: "external_url '" + values['EXTERNAL_URL'] + "'", text)
    text = gitlab_set(text, "nginx['listen_port']", str(values['WEB_PORT'] if mode == 'NATIVE' else 80))
    if mode == 'NATIVE': text = gitlab_set(text, "nginx['listen_addresses']", "['" + values['BIND_ADDRESS'] + "']")
    text = gitlab_set(text, "gitlab_rails['gitlab_shell_ssh_port']", str(values['SSH_PORT']))
    # The initial password directive belongs to first installation only.
    text = without_initial_password(text)
    write(gitlab_config, text)
    if mode == 'NATIVE': gitlab_reconfigure()

def gitlab_reconfigure():
    content = read(gitlab_config)
    safe_content = without_initial_password(content)
    write(gitlab_config, safe_content)
    try: run(['/opt/gitlab/bin/gitlab-ctl', 'reconfigure'], 1200)
    finally: write(gitlab_config, content)

def app_health(values):
    http_ready(values, '/-/readiness')
