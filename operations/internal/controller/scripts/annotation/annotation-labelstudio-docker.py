container = 'ops-labelstudio-' + sid
old_locator = os.environ.get('OPS_PREVIOUS_LOCATOR', container)
previous_name = container + '-previous-' + job[:8]

def managed_files():
    return [meta]

def inspect(locator):
    format = '{"Id":{{json .Id}},"Name":{{json .Name}},"Image":{{json .Image}},"State":{"Running":{{json .State.Running}}},"Config":{"Env":{{json .Config.Env}},"Labels":{{json .Config.Labels}}},"HostConfig":{"Memory":{{json .HostConfig.Memory}},"PortBindings":{{json .HostConfig.PortBindings}}},"Mounts":{{json .Mounts}}}'
    value = json.loads(run(['docker', 'inspect', '--format', format, '--', locator]))
    if value['Config'].get('Labels', {}).get('streamfusion.ops.software-id') != sid:
        fail('容器归属不符')
    return value

def actual():
    info = inspect(container if action == 'verify' else old_locator)
    env = dict(item.split('=',1) for item in info['Config'].get('Env',[]) if '=' in item)
    ports = info['HostConfig']['PortBindings'].get('8080/tcp', [])
    if len(ports) != 1:
        fail('无法确认唯一网页端口')
    mounts = {item['Destination']:item for item in info['Mounts']}
    data = mounts['/label-studio/data']['Source']
    source = mounts.get('/datasets', {}).get('Source', '') if env.get('LABEL_STUDIO_LOCAL_FILES_SERVING_ENABLED') == 'true' else ''
    return dict(BIND_ADDRESS=ports[0]['HostIp'] or '0.0.0.0', WEB_PORT=int(ports[0]['HostPort']), MAX_UPLOAD_MIB=int(env['DATA_UPLOAD_MAX_MEMORY_SIZE'])//1048576, SOURCE_DIRECTORY=source, MEMORY_MIB=int(info['HostConfig']['Memory'])//1048576, DATA_DIRECTORY=data, PUBLIC_URL=env.get('LABEL_STUDIO_HOST',''))

def stop():
    info = inspect(old_locator)
    if not (backup/'docker.json').exists():
        save(backup/'docker.json', info)
    run(['docker', 'stop', '--time', '60', old_locator], 'docker stop --time 60 [原受管容器]')

def apply(value):
    original = load(backup/'docker.json')
    if original['Name'].lstrip('/') != container or original['Config']['Labels'].get('streamfusion.ops.software-id') != sid:
        fail('原容器名称或标签不符')
    run(['docker', 'rename', old_locator, previous_name], 'docker rename [原受管容器] [本次操作备份名称]')
    env = dict(item.split('=',1) for item in original['Config'].get('Env',[]) if '=' in item)
    env.update(LABEL_STUDIO_BASE_DATA_DIR='/label-studio/data', DATA_UPLOAD_MAX_MEMORY_SIZE=str(value['MAX_UPLOAD_MIB']*1048576), LABEL_STUDIO_HOST=value['PUBLIC_URL'], LABEL_STUDIO_LOCAL_FILES_SERVING_ENABLED='true' if value['SOURCE_DIRECTORY'] else 'false', LABEL_STUDIO_LOCAL_FILES_DOCUMENT_ROOT='/datasets' if value['SOURCE_DIRECTORY'] else '/label-studio/data')
    args = ['docker', 'create', '--name', container, '--restart', 'unless-stopped', '--memory', str(value['MEMORY_MIB'])+'m', '--entrypoint', '/label-studio/.venv/bin/label-studio']
    for key, item in original['Config']['Labels'].items():
        args += ['--label', key+'='+item]
    for key, item in env.items():
        args += ['-e', key+'='+item]
    for key, mappings in original['HostConfig'].get('PortBindings',{}).items():
        if key != '8080/tcp':
            for item in mappings or []:
                args += ['-p', (item['HostIp'] or '0.0.0.0')+':'+item['HostPort']+':'+key]
    args += ['-p', value['BIND_ADDRESS']+':'+str(value['WEB_PORT'])+':8080/tcp']
    for item in original['Mounts']:
        if item['Destination'] not in ('/label-studio/data','/datasets'):
            if item['Type'] != 'bind':
                fail('存在额外挂载类型，请先核对原容器配置')
            args += ['-v', item['Source']+':'+item['Destination']+('' if item['RW'] else ':ro')]
    args += ['-v', value['DATA_DIRECTORY']+':/label-studio/data']
    if value['SOURCE_DIRECTORY']:
        args += ['-v', value['SOURCE_DIRECTORY']+':/datasets:ro']
    args += [original['Image'], 'start', '--no-browser', '--internal-host', '0.0.0.0', '--port', '8080']
    run(args, 'docker create [保留原镜像、标签与环境] -p '+value['BIND_ADDRESS']+':'+str(value['WEB_PORT'])+':8080 -v '+value['DATA_DIRECTORY']+':/label-studio/data')

def start():
    if previous_state == 'RUNNING':
        run(['docker', 'start', container], 'docker start [新受管容器]')

def import_source(value, checking=False):
    if value['SOURCE_DIRECTORY']:
        source = safe(value['SOURCE_DIRECTORY'])
        # The recreated container checks readability as its fixed service UID.
        if previous_state == 'RUNNING' and not checking:
            run(['docker', 'exec', '--user', '1001', container, '/bin/sh', '-c', 'test -r /datasets && test -x /datasets'])

def health(value):
    wait_http(value, '/health')

def restore_container():
    original = load(backup/'docker.json')
    candidate = None
    try:
        candidate = inspect(container)
    except Exception:
        pass
    if candidate and candidate['Id'] != original['Id']:
        if candidate['Image'] != original['Image']:
            fail('回滚候选容器的镜像身份不符')
        run(['docker','rm','--force',candidate['Id']], 'docker rm --force [本次新容器]')
    previous = inspect(original['Id'])
    if previous['Name'].lstrip('/') != container:
        run(['docker','rename',original['Id'],container], 'docker rename [原容器ID] [原名称]')
    if previous_state == 'RUNNING':
        run(['docker','start',original['Id']], 'docker start [原容器ID]')
