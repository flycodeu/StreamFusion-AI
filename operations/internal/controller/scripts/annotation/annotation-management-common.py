import hashlib, ipaddress, json, os, re, shlex, shutil, stat, subprocess, sys, tempfile, time, urllib.request
from pathlib import Path

root, app, mode, action = Path(sys.argv[1]), sys.argv[2], sys.argv[3], sys.argv[4]
sid, job = os.environ['OPS_SOFTWARE_ID'], os.environ['OPS_JOB_ID']
phase = os.environ.get('OPS_OPERATION_PHASE', 'APPLY')
previous_state = os.environ.get('OPS_PREVIOUS_STATE', 'RUNNING')
if not re.fullmatch(r'[a-f0-9-]{36}', sid) or not re.fullmatch(r'[a-f0-9-]{36}', job):
    sys.exit('软件或操作标识无效')
unit = ('ops-app-' if app == 'ytlabel' else 'ops-labelstudio-') + sid + '.service'
unit_path = Path('/etc/systemd/system') / unit
meta = root / 'config/annotation-management.json'
backup = root / 'config/management-backups' / job
limit = time.monotonic() + (25 if action in ('read-config', 'check-data-migration') else 1200)

def fail(message):
    raise RuntimeError(message)

def safe(path, exists=True):
    path = Path(path)
    if not path.is_absolute() or str(path) == '/' or not re.fullmatch(r'/[A-Za-z0-9_./+-]+', str(path)) or path.resolve() != path:
        fail('目录须为不含符号链接的完整绝对路径')
    if exists and not path.is_dir():
        fail('目录不存在或不可读取：' + str(path))
    return path

def read(path, maximum=65536):
    path = Path(path)
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or path.resolve() != path or info.st_size > maximum:
        fail('配置文件不可读取：' + str(path))
    return path.read_text(encoding='utf8')

def load(path):
    value = json.loads(read(path))
    if not isinstance(value, dict):
        fail('配置格式不正确')
    return value

def write(path, content):
    path = Path(path)
    info = path.lstat() if path.exists() else None
    if info and (not stat.S_ISREG(info.st_mode) or path.resolve() != path):
        fail('配置位置不安全')
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as target:
        name = target.name
        target.write(content.encode('utf8'))
        target.flush()
        os.fsync(target.fileno())
        os.fchmod(target.fileno(), stat.S_IMODE(info.st_mode) if info else 0o600)
        if info:
            os.fchown(target.fileno(), info.st_uid, info.st_gid)
    os.replace(name, path)

def save(path, value):
    write(path, json.dumps(value, ensure_ascii=False, sort_keys=True))

def run(args, display=None):
    if time.monotonic() >= limit:
        fail('操作超过限定时间，请核对当前进度')
    if display:
        print('$ ' + display, flush=True)
    with tempfile.TemporaryFile() as output, tempfile.TemporaryFile() as errors:
        child = subprocess.Popen(args, stdout=output, stderr=errors)
        deadline = min(limit, time.monotonic()+900)
        while child.poll() is None:
            if time.monotonic() > deadline or os.fstat(output.fileno()).st_size > 1048576 or os.fstat(errors.fileno()).st_size > 1048576:
                child.kill()
                child.wait(timeout=3)
                fail('命令超过时间或输出限制')
            time.sleep(0.05)
        if os.fstat(output.fileno()).st_size > 1048576 or os.fstat(errors.fileno()).st_size > 1048576:
            fail('命令输出超过限制')
        if child.returncode:
            fail('命令执行失败：' + args[0])
        output.seek(0)
        return output.read().decode('utf8')

def table(values):
    print(json.dumps({'columns': ['key', 'value'], 'rows': [{'key': k, 'value': str(v)} for k,v in values.items()]}, ensure_ascii=False))

def wait_http(value, path, marker=b''):
    if previous_state != 'RUNNING':
        return
    address = '127.0.0.1' if value['BIND_ADDRESS'] == '0.0.0.0' else value['BIND_ADDRESS']
    for _ in range(45):
        try:
            with urllib.request.urlopen('http://%s:%s%s' % (address,value['WEB_PORT'],path), timeout=3) as response:
                if response.status == 200 and (not marker or marker in response.read(1048576).lower()):
                    return
        except Exception:
            time.sleep(2)
    fail('配置后服务未就绪，请回滚并查看运行日志')

def owner():
    if read(root / 'config/ops-owner').strip() != app + ':' + sid:
        fail('安装目录归属不符')

def fields(text):
    return dict(line.split('=', 1) for line in text.splitlines() if '=' in line and not line.startswith('#'))

def requested(before):
    value = dict(before)
    for key in ['BIND_ADDRESS', 'WEB_PORT', 'MAX_UPLOAD_MIB', 'SOURCE_DIRECTORY', 'MEMORY_MIB'] + (['PUBLIC_URL'] if app != 'ytlabel' else []):
        value[key] = os.environ.get('OPS_PARAM_' + key, str(before[key]))
    address = ipaddress.IPv4Address(value['BIND_ADDRESS'])
    if address.is_multicast:
        fail('监听地址不能为组播地址')
    for key, high in [('WEB_PORT', 65535), ('MAX_UPLOAD_MIB', 16384 if app == 'ytlabel' else 2048), ('MEMORY_MIB', 16384)]:
        value[key] = int(value[key])
        if value[key] < (128 if key == 'MEMORY_MIB' else 1) or value[key] > high:
            fail('配置数值超出允许范围：' + key)
    if value['SOURCE_DIRECTORY']:
        source = safe(value['SOURCE_DIRECTORY'])
        data = safe(value['DATA_DIRECTORY'])
        if source == data or source in data.parents or data in source.parents or source == root or root in source.parents:
            fail('素材目录不能与实例或数据目录重叠')
    validate_application(value)
    return value

def file_digest(path):
    digest = hashlib.sha256()
    with path.open('rb') as source:
        for chunk in iter(lambda: source.read(1048576), b''):
            if time.monotonic() > limit:
                fail('数据摘要核验超时')
            digest.update(chunk)
    return digest.hexdigest()

def tree(directory, hashes=False):
    directory = safe(directory)
    result, size, seen, links = {}, 0, 0, {}
    if hashes:
        info = directory.stat()
        result['.'] = (stat.S_IMODE(info.st_mode), info.st_uid, info.st_gid, 0, '', '')
    for parent, dirs, files in os.walk(directory, followlinks=False):
        dirs.sort()
        for name in sorted(dirs + files):
            path = Path(parent) / name
            info = path.lstat()
            seen += 1
            if seen > 100000 or time.monotonic() > limit:
                fail('目录文件数量或检查时间超过限制')
            if path.is_symlink() or not (stat.S_ISDIR(info.st_mode) or stat.S_ISREG(info.st_mode)) or path.is_mount():
                fail('目录包含链接、嵌套挂载或特殊文件')
            size += info.st_size if path.is_file() else 0
            if size > 128 * 1024**3:
                fail('单次数据操作最多处理 128 GiB')
            if hashes:
                digest, link = '', ''
                if path.is_file():
                    digest = file_digest(path)
                    identity = (info.st_dev, info.st_ino)
                    link = links.setdefault(identity, str(path.relative_to(directory)))
                result[str(path.relative_to(directory))] = (stat.S_IMODE(info.st_mode), info.st_uid, info.st_gid, info.st_size if path.is_file() else 0, digest, link)
    return result, size, seen

def migration(before):
    source = safe(before['DATA_DIRECTORY'])
    target = safe(os.environ['OPS_PARAM_DATA_DIRECTORY'], False)
    if not str(target).startswith(('/opt/', '/srv/', '/var/lib/', '/data/', '/mnt/')) or target.exists() or target == root or root in target.parents or target in root.parents or source == target or source in target.parents or target in source.parents:
        fail('请选择 /opt、/srv、/var/lib、/data 或 /mnt 下尚不存在且独立的数据目录')
    if before['SOURCE_DIRECTORY']:
        materials = safe(before['SOURCE_DIRECTORY'])
        if target == materials or target in materials.parents or materials in target.parents:
            fail('新数据目录不能与素材目录重叠')
    parent = target.parent
    while not parent.exists():
        parent = parent.parent
    safe(parent)
    for ancestor in [parent] + list(parent.parents):
        info = ancestor.stat()
        if info.st_uid != 0 or info.st_mode & 0o022:
            fail('目标目录的上级目录须由管理员保护')
    _, size, count = tree(source)
    free = shutil.disk_usage(parent).free
    if free < size + 64*1024**2:
        fail('目标目录空间不足')
    return source, target, size, count, free

def snapshot(before):
    if backup.exists():
        fail('本次操作备份已存在，请核对原任务')
    backup.mkdir(mode=0o700, parents=True)
    save(backup / 'before.json', before)
    for index, path in enumerate(managed_files()):
        if path.exists():
            read(path)
            shutil.copy2(path, backup / str(index))
            info = path.stat()
            os.chown(backup / str(index), info.st_uid, info.st_gid)
    save(backup / 'files.json', [str(p) for p in managed_files()])

def restore_files():
    paths = json.loads(read(backup / 'files.json'))
    if paths != [str(p) for p in managed_files()]:
        fail('回滚文件清单不符')
    for index, name in enumerate(paths):
        path, original = Path(name), backup / str(index)
        if original.exists():
            shutil.copy2(original, path)
            info = original.stat()
            os.chown(path, info.st_uid, info.st_gid)
        elif path.exists():
            read(path)
            path.unlink()
