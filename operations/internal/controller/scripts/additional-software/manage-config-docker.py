container = 'sfops-' + sid
port_keys = {'WEB_PORT': @@PORT@@}
if app == 'gitlab': port_keys['SSH_PORT'] = 22
if app == 'nacos': port_keys.update(CONSOLE_PORT=8080, GRPC_PORT=9848, CLUSTER_GRPC_PORT=9849)

def current_container():
    return docker_inspect(container if action == 'verify' else os.environ['OPS_SOFTWARE_LOCATOR'])

def runtime_files():
    return app_files() if app == 'gitlab' else []

def runtime_actual():
    info = current_container(); values = {}
    addresses = set()
    for key, number in port_keys.items():
        bindings = info['HostConfig'].get('PortBindings', {}).get(str(number) + '/tcp') or []
        if len(bindings) != 1: fail('无法确认唯一服务端口，请核对原容器映射')
        values[key] = int(bindings[0]['HostPort']); addresses.add(bindings[0]['HostIp'] or '0.0.0.0')
    if len(addresses) != 1: fail('当前服务端口使用不同监听地址，请先单独核对')
    values.update(BIND_ADDRESS=addresses.pop(), MEMORY_MIB=int(info['HostConfig']['Memory'])//1048576)
    if app == 'gitlab': values['EXTERNAL_URL'] = gitlab_value('external_url')
    if app == 'prometheus': values['RETENTION'] = flag(info['Config']['Cmd'], '--storage.tsdb.retention.time')
    return values

def desired_container(values):
    original = current_container(); ports = copy.deepcopy(original['HostConfig']['PortBindings']); config = copy.deepcopy(original['Config'])
    for key, number in port_keys.items(): ports[str(number) + '/tcp'] = [{'HostIp': values['BIND_ADDRESS'], 'HostPort': str(values[key])}]
    if app == 'prometheus': config['Cmd'] = set_flag(config['Cmd'], '--storage.tsdb.retention.time', values['RETENTION'])
    if app == 'nacos' and values['MEMORY_MIB']:
        config['Env'] = ['JVM_XMX=' + str(max(128, values['MEMORY_MIB']*3//4)) + 'm' if item.startswith('JVM_XMX=') else item for item in config.get('Env', [])]
    return original, ports, config

def runtime_validate(values):
    original, ports, config = desired_container(values)
    if original['Name'] != '/' + container: fail('原容器名称不符')
    if app == 'gitlab' and not any(item.get('Destination') == '/etc/gitlab' and item.get('Type') == 'bind' and item.get('Source') == str(root/'data/gitlab-config') for item in original.get('Mounts', [])): fail('GitLab 配置挂载已变化，请核对原容器')
    docker_plan(original, ports, values['MEMORY_MIB'], config)

def runtime_snapshot():
    save(backup/'container.json', current_container())

def runtime_stop():
    pass

def runtime_apply(values):
    original, ports, config = desired_container(values)
    if app == 'gitlab': app_apply(values)
    docker_recreate(original, job, ports, values['MEMORY_MIB'], config)

def runtime_start():
    if previous == 'RUNNING': _docker_api('POST', '/containers/' + docker_inspect(container)['Id'] + '/start')

def runtime_verify(values):
    info = docker_inspect(container)
    if bool(info['State']['Running']) != (previous == 'RUNNING'): fail('容器运行状态与操作前不符')
    if previous == 'RUNNING':
        if app == 'postgresql':
            for _ in range(60):
                try:
                    run(['docker', 'exec', info['Id'], 'pg_isready', '-h', '127.0.0.1', '-p', '5432'])
                    break
                except RuntimeError: time.sleep(2)
            else: fail('PostgreSQL 尚未接受连接，将恢复原配置')
        else: app_health(values)

def runtime_restore():
    if app == 'gitlab': _docker_api('POST', '/containers/' + docker_inspect(container)['Id'] + '/stop?t=60')
    restore_files(); docker_restore(load(backup/'container.json'))
