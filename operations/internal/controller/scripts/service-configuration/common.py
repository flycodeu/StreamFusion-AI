import ipaddress
import pathlib
import selectors
import stat
import subprocess
import time

root = pathlib.Path(os.environ['OPS_INSTALL_ROOT'])
sid = os.environ['OPS_SOFTWARE_ID']
job = os.environ['OPS_JOB_ID']
mode = os.environ['OPS_RUNTIME_KIND']
locator = os.environ['OPS_SOFTWARE_LOCATOR']
previous_state = os.environ.get('OPS_PREVIOUS_STATE', 'RUNNING')
backup = root / 'work' / ('service-config-' + job)


def fail(message):
    raise RuntimeError(message)


def safe(path, directory=False, missing=False):
    path = pathlib.Path(path)
    if not path.is_absolute() or '..' in path.parts:
        fail('配置路径无效')
    for parent in reversed((path,) + tuple(path.parents)):
        if parent.is_symlink():
            fail('配置路径不能经过符号链接')
    if path.exists():
        info = path.stat()
        if directory and not stat.S_ISDIR(info.st_mode) or not directory and not stat.S_ISREG(info.st_mode):
            fail('配置路径类型不符')
    elif not missing:
        fail('未找到现有配置，请先检查软件安装结果')
    return path


def read(path):
    path = safe(path)
    if path.stat().st_size > 2 * 1048576:
        fail('配置文件超过 2 MiB，请先整理配置')
    with path.open('r',encoding='utf-8',newline='') as stream:
        return stream.read()


def write(path, text):
    path = safe(path, missing=True)
    safe(path.parent, directory=True)
    # Existing Docker single-file bind mounts must retain their inode.
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o640)
    with os.fdopen(descriptor, 'w', encoding='utf-8', newline='\n') as target:
        target.write(text)
        target.flush()
        os.fsync(target.fileno())


def run(args, timeout=60, input=None):
    process = subprocess.Popen(args, stdin=subprocess.PIPE if input is not None else subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    output = bytearray()
    try:
        if input is not None:
            process.stdin.write(input.encode())
            process.stdin.close()
        selector = selectors.DefaultSelector()
        selector.register(process.stdout, selectors.EVENT_READ)
        deadline = time.monotonic() + timeout
        while selector.get_map():
            if time.monotonic() >= deadline:
                fail('命令执行超时，请检查服务状态')
            for key, _ in selector.select(0.2):
                data = os.read(key.fd, 4096)
                if not data:
                    selector.unregister(key.fileobj)
                output.extend(data)
                if len(output) > 65536:
                    fail('命令输出过多，请检查服务日志')
        if process.wait(timeout=max(1, deadline-time.monotonic())):
            fail('配置校验或服务操作失败，请查看软件日志')
        return output.decode('utf-8', errors='strict').strip()
    finally:
        if process.poll() is None:
            process.kill()
            process.wait(timeout=5)
        process.stdout.close()


def integer(key, low, high):
    value = os.environ.get('OPS_PARAM_' + key, '')
    if not value.isdecimal() or not low <= int(value) <= high:
        fail('配置项 ' + key + ' 超出允许范围')
    return int(value)


def desired():
    address = os.environ.get('OPS_PARAM_BIND_ADDRESS', '')
    try:
        if ipaddress.ip_address(address).version != 4:
            fail('当前配置支持 IPv4 监听地址')
    except ValueError:
        fail('请填写有效的 IPv4 监听地址')
    value = dict(BIND_ADDRESS=address, CONFIG_PORT=integer('CONFIG_PORT', 1, 65535), MEMORY_MIB=integer('MEMORY_MIB', 0, 16384))
    if 0 < value['MEMORY_MIB'] < 128:
        fail('内存上限请设置为 0（不限）或至少 128 MiB')
    for key, bounds in fields.items():
        value[key] = integer(key, *bounds)
    return value


def snapshot():
    safe(root, directory=True)
    safe(root/'work', directory=True)
    if not re.fullmatch(r'[a-f0-9-]{36}', job):
        fail('操作标识无效')
    backup.mkdir(mode=0o700)
    saved = []
    for index, path in enumerate(managed_files()):
        path = safe(path, missing=True)
        if path.exists():
            info = path.stat()
            text = read(path)
            write(backup / str(index), text)
            os.chmod(backup / str(index), 0o600)
            saved.append(dict(path=str(path), present=True, uid=info.st_uid, gid=info.st_gid, mode=stat.S_IMODE(info.st_mode)))
        else:
            saved.append(dict(path=str(path), present=False))
    write(backup/'files.json', json.dumps(saved))
    os.chmod(backup/'files.json', 0o600)
    if mode == 'DOCKER':
        write(backup/'docker.json', json.dumps(docker_inspect(locator)))
        os.chmod(backup/'docker.json', 0o600)


def restore_files():
    safe(backup, directory=True)
    for index, item in enumerate(json.loads(read(backup/'files.json'))):
        path = safe(item['path'], missing=True)
        if item['present']:
            write(path, read(backup/str(index)))
            os.chown(path, item['uid'], item['gid'])
            os.chmod(path, item['mode'])
        elif path.exists():
            path.unlink()


def current_container():
    return docker_inspect(container_name if action == 'verify' else locator)


def network_value():
    info = current_container()
    mappings = info['HostConfig'].get('PortBindings', {}).get(str(internal_port)+'/tcp') or []
    if len(mappings) != 1:
        fail('无法确认唯一服务入口，请先核对容器端口')
    return dict(BIND_ADDRESS=mappings[0]['HostIp'] or '0.0.0.0', CONFIG_PORT=int(mappings[0]['HostPort']), MEMORY_MIB=int(info['HostConfig'].get('Memory') or 0)//1048576)


def activate(value):
    if mode == 'DOCKER':
        original = json.loads(read(backup/'docker.json'))
        ports = copy.deepcopy(original['HostConfig'].get('PortBindings', {}))
        ports[str(internal_port)+'/tcp'] = [{'HostIp':value['BIND_ADDRESS'], 'HostPort':str(value['CONFIG_PORT'])}]
        candidate = docker_recreate(original, job, ports=ports, memory_mib=value['MEMORY_MIB'])
        if previous_state == 'RUNNING':
            _docker_api('POST', '/containers/' + candidate + '/start')
    elif previous_state == 'RUNNING':
        run(['systemctl', 'restart', locator], timeout=120)


def rollback():
    if not (backup/'changed').exists():
        print('配置保持原状')
        return
    restore_files()
    if mode == 'DOCKER':
        docker_restore(json.loads(read(backup/'docker.json')))
    else:
        run(['systemctl', 'daemon-reload'])
        if previous_state == 'RUNNING':
            run(['systemctl', 'restart', locator], timeout=120)
    if actual() != json.loads(read(backup/'before.json')):
        fail('原配置恢复后核对失败，请检查软件日志')
    print('已恢复原配置')


def native_memory():
    value = run(['systemctl', 'show', '--property=MemoryMax', '--value', locator])
    if value == 'infinity':
        return 0
    if not value.isdecimal():
        fail('无法读取服务内存上限')
    return int(value)//1048576


def memory_file():
    unit = re.fullmatch(r'[A-Za-z0-9_.@-]+\.service', locator)
    if not unit:
        fail('服务单元标识无效')
    return pathlib.Path('/etc/systemd/system') / (locator + '.d') / ('ops-config-' + sid + '.conf')


def save_memory(value):
    if mode != 'DOCKER':
        target = memory_file()
        if not target.parent.exists():
            safe(target.parent.parent, directory=True)
            target.parent.mkdir(mode=0o755)
        safe(target.parent, directory=True)
        write(target, '[Service]\nMemoryMax=%s\n' % (str(value['MEMORY_MIB'])+'M' if value['MEMORY_MIB'] else 'infinity'))
        run(['systemctl', 'daemon-reload'])


def table(value):
    print(json.dumps({'columns':['key','value'], 'rows':[{'key':key,'value':str(item)} for key,item in value.items()]}, ensure_ascii=False))


def flush_backup():
    descriptor=os.open(backup,os.O_RDONLY|os.O_DIRECTORY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def execute():
    if action == 'read-config':
        table(actual())
    elif action == 'configure':
        value = desired()
        before = actual()
        validate(value)
        snapshot()
        write(backup/'before.json', json.dumps(before))
        write(backup/'desired.json', json.dumps(value))
        write(backup/'changed', 'APPLY\n')
        flush_backup()
        apply(value)
        save_memory(value)
        activate(value)
        print('配置已保存，正在核验服务')
    elif action == 'verify':
        value = json.loads(read(backup/'desired.json'))
        check(value)
        found = actual()
        if any(str(found.get(key)) != str(item) for key,item in value.items()):
            fail('实际配置与本次设置不一致')
        if previous_state == 'RUNNING' and mode != 'DOCKER':
            run(['systemctl','is-active','--quiet',locator])
        if mode == 'DOCKER' and bool(current_container()['State']['Running']) != (previous_state=='RUNNING'):
            fail('容器运行状态与修改前不一致')
        if mode != 'DOCKER' and previous_state == 'STOPPED' and run(['systemctl','show','--property=ActiveState','--value',locator]) != 'inactive':
            fail('服务应保持停止状态')
        print('配置已生效')
    elif action == 'rollback':
        rollback()
    else:
        fail('配置操作无效')
