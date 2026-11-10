import copy, ipaddress, json, os, re, shlex, socket, stat, subprocess, sys, tempfile, time, urllib.parse, urllib.request
from pathlib import Path

root, action = Path(sys.argv[1]), sys.argv[2]
app, mode = '@@APP@@', '@@MODE@@'
sid, job = os.environ['OPS_SOFTWARE_ID'], os.environ['OPS_JOB_ID']
previous = os.environ.get('OPS_PREVIOUS_STATE', 'RUNNING')
unit = 'gitlab-runsvdir.service' if app == 'gitlab' else 'ops-app-' + sid + '.service'
unit_path = Path('/etc/systemd/system') / unit
backup = root / 'config/management-backups' / job
deadline = time.monotonic() + (25 if action == 'read-config' else 1500)

def fail(message):
    raise RuntimeError(message)

def read(path):
    path = Path(path)
    info = path.lstat()
    if path.resolve() != path or not stat.S_ISREG(info.st_mode) or info.st_size > 262144:
        fail('配置文件不可安全读取：' + str(path))
    with path.open('r', encoding='utf8', newline='') as source:
        return source.read()

def write(path, content):
    path = Path(path)
    if path.parent.resolve() != path.parent:
        fail('配置目录包含符号链接')
    info = path.lstat() if path.exists() else None
    if info and (not stat.S_ISREG(info.st_mode) or path.resolve() != path):
        fail('配置文件位置不安全')
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as target:
        target.write(content.encode('utf8')); target.flush(); os.fsync(target.fileno())
        os.fchmod(target.fileno(), stat.S_IMODE(info.st_mode) if info else 0o600)
        if info: os.fchown(target.fileno(), info.st_uid, info.st_gid)
        name = target.name
    os.replace(name, path)

def save(path, value):
    write(path, json.dumps(value, ensure_ascii=False))

def sync_directory(path):
    descriptor = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try: os.fsync(descriptor)
    finally: os.close(descriptor)

def load(path):
    return json.loads(read(path))

def run(args, timeout=120):
    with tempfile.TemporaryFile() as output, tempfile.TemporaryFile() as errors:
        child = subprocess.Popen(args, stdout=output, stderr=errors)
        until = min(deadline, time.monotonic() + timeout)
        while child.poll() is None:
            if time.monotonic() > until or max(os.fstat(output.fileno()).st_size, os.fstat(errors.fileno()).st_size) > 1048576:
                child.kill(); child.wait(timeout=3); fail('命令超过时间或输出限制，请查看运行日志')
            time.sleep(.05)
        if child.returncode: fail('配置命令未完成：' + Path(args[0]).name)
        output.seek(0)
        return output.read(1048576).decode('utf8')

def property_value(text, key):
    values = [line.split('=', 1)[1] for line in text.splitlines() if line.startswith(key + '=')]
    if len(values) != 1: fail('无法确认唯一配置项：' + key)
    return values[0]

def replace_property(text, key, value, required=True):
    pattern = r'(?m)^' + re.escape(key) + r'=.*$'
    count = len(re.findall(pattern, text))
    if count > 1 or required and count != 1: fail('配置项重复或缺失：' + key)
    return re.sub(pattern, lambda _: key + '=' + str(value), text) if count else text.rstrip() + '\n' + key + '=' + str(value) + '\n'

def launch_args():
    lines = [line for line in read(root/'bin/run').splitlines() if line.startswith('exec ')]
    if len(lines) != 1 or any(value in lines[0] for value in '$`|&;<>'):
        fail('启动命令已自定义，请先核对启动脚本')
    return shlex.split(lines[0][5:])

def flag(args, key):
    values = [item[len(key)+1:] for item in args if item.startswith(key + '=')]
    if len(values) != 1: fail('启动参数重复或缺失：' + key)
    return values[0]

def set_flag(args, key, value):
    flag(args, key)
    return [key + '=' + str(value) if item.startswith(key + '=') else item for item in args]

def write_launch(args):
    content = re.sub(r'(?m)^exec .*$', lambda _: 'exec ' + shlex.join(args), read(root/'bin/run'))
    write(root/'bin/run', content)

def http_ready(values, path):
    if previous != 'RUNNING': return
    address = '127.0.0.1' if values['BIND_ADDRESS'] == '0.0.0.0' else values['BIND_ADDRESS']
    for _ in range(60):
        try:
            with urllib.request.urlopen('http://%s:%s%s' % (address, values['WEB_PORT'], path), timeout=3) as response:
                if response.status == 200: return
        except urllib.error.HTTPError as error:
            if app == 'jenkins' and error.code == 403: return
        except OSError: pass
        time.sleep(2)
    fail('配置后服务未就绪，将恢复原配置')

def requested(before):
    values = {key: os.environ.get('OPS_PARAM_' + key, str(value)) for key, value in before.items()}
    address = ipaddress.IPv4Address(values['BIND_ADDRESS'])
    if address.is_multicast: fail('监听地址不能为组播地址')
    for key in ['WEB_PORT', 'SSH_PORT', 'CONSOLE_PORT', 'GRPC_PORT', 'CLUSTER_GRPC_PORT']:
        if key in values:
            values[key] = int(values[key])
            if not 1 <= values[key] <= 65535: fail('端口须在 1 至 65535 之间')
    values['MEMORY_MIB'] = int(values['MEMORY_MIB'])
    if values['MEMORY_MIB'] != 0 and not 128 <= values['MEMORY_MIB'] <= 1048576: fail('内存上限须为 0（不限）或 128 至 1048576 MiB')
    app_validate(values)
    return values

def app_validate(values):
    pass

def snapshot(before):
    if backup.exists(): fail('原操作已有备份，请核对原任务')
    backup.mkdir(parents=True, mode=0o700)
    if backup.resolve() != backup: fail('备份目录包含链接')
    save(backup/'before.json', before)
    files = []
    for index, path in enumerate(runtime_files()):
        path = Path(path); exists = path.exists()
        files.append({'path': str(path), 'exists': exists})
        if exists:
            info = path.stat(); write(backup/str(index), read(path))
            os.chmod(backup/str(index), stat.S_IMODE(info.st_mode)); os.chown(backup/str(index), info.st_uid, info.st_gid)
    save(backup/'files.json', files)
    runtime_snapshot()

def restore_files():
    files = load(backup/'files.json')
    if [item['path'] for item in files] != [str(path) for path in runtime_files()]: fail('恢复文件清单不符')
    for index, item in enumerate(files):
        path = Path(item['path'])
        if item['exists']:
            write(path, read(backup/str(index)))
            info = (backup/str(index)).stat(); os.chown(path, info.st_uid, info.st_gid); os.chmod(path, stat.S_IMODE(info.st_mode))
        elif path.exists(): read(path); path.unlink()

def main():
    if not re.fullmatch(r'[a-f0-9-]{36}', sid) or not re.fullmatch(r'[a-f0-9-]{36}', job): fail('软件或任务标识无效')
    if root.resolve() != root or read(root/'config/ops-owner').strip() != app + ':' + sid: fail('安装目录归属不符')
    if previous not in ('RUNNING', 'STOPPED'): fail('请先确认服务当前运行状态')
    if action == 'read-config':
        print(json.dumps({'columns': ['key','value'], 'rows': [{'key': key, 'value': str(value)} for key,value in runtime_actual().items()]}, ensure_ascii=False)); return
    if action == 'configure':
        before = runtime_actual(); values = requested(before)
        runtime_validate(values); snapshot(before)
        save(backup/'mutation-ready.json', {'softwareId':sid, 'jobId':job})
        for directory in [backup, backup.parent, backup.parent.parent]: sync_directory(directory)
        runtime_stop(); runtime_apply(values)
        save(backup/'expected.json', values); runtime_start(); print('配置已保存，正在验证服务'); return
    if action == 'verify':
        expected, current = load(backup/'expected.json'), runtime_actual()
        if {key:str(value) for key,value in current.items()} != {key:str(value) for key,value in expected.items()}: fail('实际配置与本次设置不符')
        runtime_verify(current); print('配置与服务状态已确认'); return
    if action == 'rollback':
        if not (backup/'mutation-ready.json').exists(): print('配置尚未写入'); return
        if load(backup/'mutation-ready.json') != {'softwareId':sid, 'jobId':job}: fail('恢复标记与原操作不符')
        runtime_restore(); runtime_verify(load(backup/'before.json')); print('已恢复原配置与运行状态'); return
    fail('不支持此配置操作')
