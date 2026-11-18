set -Eeuo pipefail
umask 077
exec /usr/bin/python3 -I -S -B - <<'PY'
import hashlib
import json
import os
import pathlib
import re
import selectors
import shutil
import stat
import subprocess
import sys
import tempfile
import time


class ConfigError(Exception):
    def __init__(self, code, message, line=0):
        self.code, self.message, self.line = code, message, line


def fail(code, message, line=0):
    raise ConfigError(code, message, line)


root = pathlib.Path(os.environ['OPS_INSTALL_ROOT'])
config = pathlib.Path(os.environ['OPS_CONFIG_ROOT'])
data = pathlib.Path(os.environ['OPS_DATA_ROOT'])
work = pathlib.Path(os.environ['OPS_CONFIG_WORK_DIR'])
result = pathlib.Path(os.environ['OPS_CONFIG_RESULT_FILE'])
official = os.environ.get('OPS_NGINX_OFFICIAL') == 'true'
docker = os.environ['OPS_RUNTIME_KIND'] == 'DOCKER'
locator = os.environ['OPS_SOFTWARE_LOCATOR']
sid = os.environ['OPS_SOFTWARE_ID']
phase = os.environ.get('OPS_OPERATION_PHASE', 'CHECK')
action = os.environ['OPS_PARAM_FILE_ACTION']
name = os.environ.get('OPS_PARAM_SITE', '')
allowed_listen = os.environ.get('OPS_PARAM_LISTEN', '')
sites = config / 'ops-sites'
main = pathlib.Path('/etc/nginx/nginx.conf') if official else config / 'nginx.conf'
base = sites / 'base.conf.saved'
limit = 512 * 1024
body_limit = 32 * 1024
pattern = re.compile(r'^[A-Za-z0-9][A-Za-z0-9_-]{0,47}$')
manifest = work / 'nginx-files-transaction.json'
candidate_lines = None


def protected(path, missing=False):
    if not path.is_absolute():
        fail('CONFIG_FILE_UNSAFE', '配置路径不是完整路径')
    for item in (path, *path.parents):
        try:
            s = item.lstat()
        except FileNotFoundError:
            if missing:
                continue
            fail('CONFIG_FILE_UNREADABLE', '配置路径不存在')
        if stat.S_ISLNK(s.st_mode) or s.st_uid != 0 or s.st_mode & 0o022:
            fail('CONFIG_FILE_UNSAFE', '配置路径的所有权或权限不符合要求')


def read(path):
    protected(path)
    s = path.lstat()
    if not stat.S_ISREG(s.st_mode) or s.st_size > limit:
        fail('CONFIG_FILE_UNSUPPORTED', '配置文件超过大小限制或不是普通文件')
    raw = path.read_bytes()
    try:
        text = raw.decode('utf-8')
    except UnicodeError:
        fail('CONFIG_FILE_UNSUPPORTED', '配置文件不是 UTF-8 文本')
    if '\0' in text or '\r' in text or any(ord(c) < 32 and c not in '\n\t' for c in text):
        fail('CONFIG_FILE_INVALID', '配置文本包含无效控制字符')
    return text


def read_body(path):
    text = read(path)
    if len(text.encode('utf-8')) > body_limit:
        fail('CONFIG_FILE_LIMIT', '站点配置超过 32 KiB 编辑上限')
    return text


def save_bytes(path, value, mode=None, owner=None):
    protected(path, missing=True)
    previous = path.stat() if path.exists() else None
    if mode is None:
        mode = stat.S_IMODE(previous.st_mode) if previous else 0o600
    fd, temp = tempfile.mkstemp(prefix='.nginx-file-', dir=path.parent)
    try:
        if owner is not None or previous:
            os.fchown(fd, *(owner or (previous.st_uid, previous.st_gid)))
        os.fchmod(fd, mode)
        with os.fdopen(fd, 'wb') as stream:
            stream.write(value)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temp, path)
        sync_dir(path.parent)
    finally:
        if os.path.exists(temp):
            os.unlink(temp)


def sync_dir(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def remove_file(path):
    protected(path, missing=True)
    if path.exists():
        path.unlink()
        sync_dir(path.parent)


def digest(path):
    protected(path, missing=True)
    if not path.exists():
        return 'ABSENT'
    if not path.is_file() or path.stat().st_size > limit:
        fail('CONFIG_FILE_LIMIT', '配置文件超过大小限制或不是普通文件')
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_json(path, value):
    protected(path, missing=True)
    save_bytes(path, json.dumps(value, ensure_ascii=False).encode(), 0o600)


def emit(value, public):
    write_json(result, value)
    print(public)


def run(args, timeout=30, check_names=False):
    try:
        process = subprocess.Popen(args, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                   stdin=subprocess.DEVNULL, env={'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'LC_ALL': 'C'})
        deadline = time.monotonic() + timeout
        output = bytearray()
        try:
            with selectors.DefaultSelector() as selector:
                selector.register(process.stdout, selectors.EVENT_READ)
                while True:
                    remaining = deadline - time.monotonic()
                    if remaining <= 0 or not selector.select(remaining):
                        raise subprocess.TimeoutExpired(args, timeout)
                    chunk = os.read(process.stdout.fileno(), 4096)
                    if not chunk:
                        break
                    output.extend(chunk)
                    if len(output) > 64 * 1024:
                        fail('CONFIG_FILE_LIMIT', '服务返回的信息超过检查上限')
            exit_code = process.wait(timeout=max(.01, deadline - time.monotonic()))
        finally:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=5)
            process.stdout.close()
    except (OSError, subprocess.TimeoutExpired):
        fail('CONFIG_FILE_COMMAND_FAILED', '配置检查或服务操作未完成，请查看原任务')
    output = output.decode('utf-8', errors='replace')
    if check_names and any('[warn]' in line and 'conflicting server name' in line for line in output.splitlines()):
        fail('CONFIG_FILE_CONFLICT', '同一监听入口已使用此域名，请修改域名后重新检查')
    if exit_code:
        source = re.search(r'\bin\s+(\S+):(\d+)(?:\s|$)', output)
        if output.startswith(('unshare:', 'mount:')):
            fail('CONFIG_FILE_UNSUPPORTED', '服务器不支持隔离检查，尚未修改当前配置')
        reasons = (('duplicate default server', '同一监听地址已存在默认站点'),
                   ('is duplicate', '存在重复配置项'), ('invalid number of arguments', '配置项参数数量不正确'),
                   ('host not found in upstream', '后端地址无法解析'), ('unknown directive', 'Nginx 无法识别此配置项'),
                   ('cannot load certificate', '证书无法读取'), ('open() ', '引用的文件不能打开'),
                   ('not allowed here', '配置项不适用于当前配置块'))
        message = next((zh for en, zh in reasons if en in output), 'Nginx 配置检查或服务操作未通过')
        line = 0
        if source:
            path, actual = source[1], int(source[2])
            if official and path == str(site_path(name)):
                line = actual
            elif not official and path.endswith(('candidate.conf', 'nginx-candidate.conf')) and candidate_lines:
                start, end = candidate_lines
                if start <= actual <= end:
                    line = actual - start + 1
        fail('CONFIG_FILE_INVALID', message, line)
    return output


def native_nginx(conf, action):
    return [str(root / 'bin/nginx'), '-p', str(root), '-c', str(conf), *action]


def test_live(previous):
    if docker:
        if previous == 'RUNNING':
            verify_live_content()
            run(['/usr/bin/docker', 'exec', locator, 'nginx', '-t'])
    elif official:
        run(['/usr/sbin/nginx', '-t'])
    else:
        run(native_nginx(main, ['-t']))


def site_path(site, enabled=True):
    if not pattern.fullmatch(site):
        fail('CONFIG_FILE_INVALID', '站点文件名只能包含字母、数字、下划线和短横线')
    if enabled and official:
        return pathlib.Path('/etc/nginx/conf.d') / ('ops-' + sid + '-' + site + '.conf')
    return sites / (site + '.conf' + ('' if enabled else '.disabled'))


def entries():
    values = {}
    protected(sites, missing=True)
    paths = list(sites.glob('*.conf')) + list(sites.glob('*.conf.disabled')) if sites.exists() else []
    if official:
        paths = list(pathlib.Path('/etc/nginx/conf.d').glob('ops-' + sid + '-*.conf')) + [p for p in paths if p.name.endswith('.disabled')]
    if len(paths) > 199:
        fail('CONFIG_FILE_LIMIT', '站点文件超过 199 个，请先整理配置')
    for path in paths:
        disabled = path.name.endswith('.disabled')
        site = path.name.removesuffix('.disabled').removesuffix('.conf')
        if official and not disabled:
            site = site.removeprefix('ops-' + sid + '-')
        if not pattern.fullmatch(site):
            continue
        protected(path)
        if official and not disabled:
            canonical = sites / (site + '.conf')
            if digest(canonical) != digest(path):
                fail('CONFIG_FILE_CHANGED', '站点源文件与生效文件不一致，请核对原操作')
        if site in values:
            fail('CONFIG_FILE_CONFLICT', '同一站点同时存在启用和停用文件，请核对服务器配置')
        values[site] = dict(path=path, enabled=not disabled)
    if official and sites.exists():
        canonical = list(sites.glob('*.conf'))
        if len(canonical) > 199:
            fail('CONFIG_FILE_LIMIT', '站点源文件超过编辑器管理上限，请先整理配置')
        for path in canonical:
            site = path.name.removesuffix('.conf')
            if pattern.fullmatch(site) and (site not in values or not values[site]['enabled']):
                fail('CONFIG_FILE_CHANGED', '站点源文件缺少对应的生效文件，请核对原操作')
    return values


def revision(site, current):
    found = site in current
    state = [(key, value['enabled'], digest(value['path']), digest(sites / (key + '.conf')) if official else '')
             for key, value in sorted(current.items())]
    baseline = official_snapshot() if official else [digest(main), digest(base)]
    raw = json.dumps([site if found else '', baseline, state], separators=(',', ':'))
    return ('' if found else 'ABSENT:') + hashlib.sha256(raw.encode()).hexdigest()


def require_revision(current):
    expected = os.environ.get('OPS_PARAM_BASE_SHA256', '')
    if revision(name, current) != expected:
        fail('CONFIG_FILE_CHANGED', '配置已被其他操作修改，请重新读取后再保存')


def official_snapshot():
    # Nginx follows the distribution's symlinks during its own syntax check.
    # Record link text and in-tree content here; never open external targets
    # such as package module files and private TLS key material.
    directory = main.parent
    protected(directory)
    total, records = 0, []
    for path in directory.rglob('*'):
        info = path.lstat()
        record = [str(path.relative_to(directory)), info.st_mode, info.st_uid, info.st_gid]
        if stat.S_ISREG(info.st_mode):
            total += info.st_size
            if total > 8 * 1024 * 1024:
                fail('CONFIG_FILE_LIMIT', 'Nginx 配置目录超过隔离检查的大小限制')
            protected(path)
            fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
            with os.fdopen(fd, 'rb') as stream:
                raw = stream.read(info.st_size + 1)
            if len(raw) != info.st_size:
                fail('CONFIG_FILE_CHANGED', 'Nginx 配置目录在读取期间发生变化，请重新检查')
            record.append(hashlib.sha256(raw).hexdigest())
        elif stat.S_ISLNK(info.st_mode):
            protected(path.parent)
            record.append(os.readlink(path))
        elif stat.S_ISDIR(info.st_mode):
            protected(path)
        else:
            fail('CONFIG_FILE_UNSUPPORTED', 'Nginx 配置目录包含无法隔离检查的文件')
        records.append(record)
        if len(records) > 1000:
            fail('CONFIG_FILE_LIMIT', 'Nginx 配置目录超过隔离检查的文件数限制')
    return hashlib.sha256(json.dumps(sorted(records), separators=(',', ':')).encode()).hexdigest()


def tokens(text):
    found, value, index, line, start = [], '', 0, 1, 1
    while index < len(text):
        char = text[index]
        if char in ' \t\n':
            if value:
                found.append((value, start, False))
                value = ''
            line += char == '\n'
        elif char == '#' and not value:
            end = text.find('\n', index)
            index = len(text) if end < 0 else end
            continue
        elif char.isspace():
            fail('CONFIG_FILE_UNSUPPORTED', '配置分隔符请使用普通空格或换行', line)
        elif char in '{};':
            if char == '}' and value:
                fail('CONFIG_FILE_INVALID', '配置块结束前缺少分隔符', line)
            if value:
                found.append((value, start, False))
                value = ''
            found.append((char, line, True))
        elif char in ('"', "'"):
            if value:
                fail('CONFIG_FILE_UNSUPPORTED', '请在带引号参数前保留空格', line)
            quote, start = char, line
            index += 1
            while index < len(text) and text[index] != quote:
                if text[index] in ('\\', '\n'):
                    fail('CONFIG_FILE_UNSUPPORTED', '带引号参数不支持换行或转义，请使用普通配置值', line)
                value += text[index]
                index += 1
            if index == len(text):
                fail('CONFIG_FILE_INVALID', '配置引号没有闭合', line)
            found.append((value, start, False))
            value = ''
            if index + 1 < len(text) and text[index+1] not in ' \t\n;{':
                fail('CONFIG_FILE_UNSUPPORTED', '请在带引号参数后保留空格', line)
        elif char == '\\':
            fail('CONFIG_FILE_UNSUPPORTED', '转义形式的配置需要通过管理模板维护', line)
        else:
            if not value:
                start = line
            value += char
        index += 1
    if value:
        found.append((value, start, False))
    return found


def inspect_container():
    raw = run(['/usr/bin/docker', 'inspect', '--format', '{{json .Mounts}}', locator])
    try:
        mounts = json.loads(raw)
    except ValueError:
        fail('CONFIG_FILE_UNREADABLE', '无法读取容器挂载信息')
    if not isinstance(mounts, list):
        fail('CONFIG_FILE_UNREADABLE', '无法读取容器挂载信息')
    return mounts


def verify_container_config():
    if docker and not any(m.get('Type') == 'bind' and m.get('Source') == str(main) and
                          m.get('Destination') == '/etc/nginx/nginx.conf' and m.get('RW') is False
                          for m in inspect_container()):
        fail('CONFIG_FILE_UNSAFE', '无法确认容器正在使用此实例的主配置挂载')


def verify_live_content():
    if not docker:
        return
    expected = digest(main)
    actual = run(['/usr/bin/docker', 'exec', locator, 'sha256sum', '/etc/nginx/nginx.conf']).split()
    if not actual or actual[0] != expected:
        fail('CONFIG_FILE_CHANGED', '容器没有读到实例当前主配置，请核对挂载后重试')


def allowed_root(value, line):
    if not re.fullmatch(r'/[A-Za-z0-9_./-]+', value) or '..' in pathlib.PurePosixPath(value).parts:
        fail('CONFIG_FILE_UNSUPPORTED', '静态目录需要使用已管理的完整路径', line)
    if docker:
        mount = '/usr/share/nginx/html'
        requested, scope = pathlib.PurePosixPath(value), pathlib.PurePosixPath(mount)
        if not (requested == scope or scope in requested.parents) or not any(
                m.get('Type') == 'bind' and m.get('Source') == str(data) and m.get('Destination') == mount and m.get('RW') is False
                for m in inspect_container()):
            fail('CONFIG_FILE_UNSUPPORTED', '静态目录必须位于容器已有的挂载目录中', line)
        requested, allowed = data.joinpath(*requested.relative_to(scope).parts), data
    else:
        requested = pathlib.Path(value)
        allowed = pathlib.Path('/var/www/html') if official else data / 'www'
    try:
        actual, scope = requested.resolve(strict=True), allowed.resolve(strict=True)
    except OSError:
        fail('CONFIG_FILE_INVALID', '静态目录不存在或无法读取', line)
    if actual != requested or not (actual == scope or scope in actual.parents) or not actual.is_dir():
        fail('CONFIG_FILE_UNSUPPORTED', '静态目录必须位于此实例的数据目录中', line)


def validate_site_access(server):
    def safe_static(context):
        if not (context.get('symlinks') or server.get('symlinks')) or not (context.get('autoindex') or server.get('autoindex')):
            fail('CONFIG_FILE_UNSUPPORTED', '静态站点需明确配置 disable_symlinks on 和 autoindex off', context['line'])

    if server.get('root'):
        safe_static(server)
    elif server.get('index') or server.get('tryfiles'):
        fail('CONFIG_FILE_UNSUPPORTED', '使用 index 或 try_files 前须指定此实例的静态目录', server['line'])
    if not server.get('root') and not server.get('terminal') and not any(item['catchall'] for item in server['locations']):
        fail('CONFIG_FILE_UNSUPPORTED', '无静态目录的站点需要 location / 代理或返回状态码作为兜底', server['line'])
    for item in server['locations']:
        has_root = item.get('root') or server.get('root')
        if not server.get('root') and item['catchall'] and not (item.get('proxy') or item.get('terminal')):
            fail('CONFIG_FILE_UNSUPPORTED', '无服务级静态目录时，location / 须用代理或返回状态码兜底', item['line'])
        if has_root:
            safe_static(item)
        elif item.get('tryfiles') or item.get('index') or not (item.get('proxy') or item.get('terminal') or server.get('terminal')):
            fail('CONFIG_FILE_UNSUPPORTED', '此 location 需指定静态目录，或明确使用 proxy_pass、return 结束请求', item['line'])
        if item.get('proxy') and not (item.get('ignore_redirect') or server.get('ignore_redirect')):
            fail('CONFIG_FILE_UNSUPPORTED', '代理需配置 proxy_ignore_headers X-Accel-Redirect，避免上游触发内部文件跳转', item['line'])


def validate(text, enforce_safety=True):
    ts, stack, head, servers = tokens(text), [], [], 0
    listeners, contexts = [], []
    common = {'index', 'default_type', 'client_max_body_size', 'send_timeout', 'keepalive_timeout', 'charset',
              'gzip', 'gzip_types', 'gzip_vary', 'gzip_min_length', 'gzip_comp_level', 'gzip_proxied',
              'add_header', 'expires', 'etag', 'absolute_redirect', 'port_in_redirect', 'server_name_in_redirect'}
    location = {'try_files', 'proxy_pass', 'proxy_http_version', 'proxy_set_header', 'proxy_read_timeout',
                'proxy_connect_timeout', 'proxy_send_timeout', 'proxy_buffering', 'proxy_request_buffering',
                'proxy_buffer_size', 'proxy_buffers', 'proxy_busy_buffers_size', 'proxy_redirect', 'proxy_cache'}
    headers = {'host', 'x-real-ip', 'x-forwarded-for', 'x-forwarded-proto', 'upgrade', 'connection',
               'x-forwarded-host', 'x-forwarded-port'}
    for token, line, delimiter in ts:
        if delimiter and token == '{':
            if not head:
                fail('CONFIG_FILE_INVALID', '配置块缺少名称', line)
            key, args = head[0][0], [t[0] for t in head[1:]]
            if key == 'server' and not stack and not args:
                servers += 1
                listeners.append(0)
                contexts.append(dict(locations=[], line=line))
            elif key == 'location' and stack == ['server'] and 1 <= len(args) <= 2:
                if len(args) == 2 and args[0] not in ('=', '^~') or not re.fullmatch(r'/[A-Za-z0-9_./-]*|@[A-Za-z0-9_-]+', args[-1]):
                    fail('CONFIG_FILE_UNSUPPORTED', '当前编辑器支持普通路径、精确路径和命名 location', line)
                item = dict(catchall=args == ['/'] or args == ['^~', '/'], line=line)
                contexts[-1]['locations'].append(item)
                contexts.append(item)
            else:
                fail('CONFIG_FILE_UNSUPPORTED', '此编辑器只管理 server 和 location 配置块', line)
            stack.append(key); head = []
        elif delimiter and token == '}':
            if head or not stack:
                fail('CONFIG_FILE_INVALID', '配置块没有正确闭合', line)
            if stack == ['server'] and listeners[-1] != 1:
                fail('CONFIG_FILE_UNSUPPORTED', '每个站点必须明确声明一个已登记的监听入口', line)
            if stack == ['server'] and enforce_safety:
                validate_site_access(contexts[-1])
            stack.pop()
            contexts.pop()
        elif delimiter and token == ';':
            if not head or not stack:
                fail('CONFIG_FILE_INVALID', '配置项不在站点块内', line)
            key, args, first = head[0][0], [t[0] for t in head[1:]], head[0][1]
            head = []
            if key == 'listen' and stack == ['server']:
                if not args or args[0] != allowed_listen or any(x not in ('default_server',) for x in args[1:]):
                    fail('CONFIG_FILE_UNSUPPORTED', '监听地址和端口须使用此实例已登记的入口', first)
                listeners[-1] += 1
            elif key == 'server_name' and stack == ['server']:
                if not args or not all(re.fullmatch(r'[A-Za-z0-9_*.\-]+', x) for x in args):
                    fail('CONFIG_FILE_INVALID', '域名格式无效', first)
            elif key == 'alias':
                if enforce_safety:
                    fail('CONFIG_FILE_UNSUPPORTED', '此编辑器使用 root 配置静态目录；alias 请通过专用管理模板维护', first)
                if len(args) != 1:
                    fail('CONFIG_FILE_INVALID', '静态目录格式无效', first)
                allowed_root(args[0], first)
                contexts[-1]['root'] = True
            elif key == 'root':
                if len(args) != 1:
                    fail('CONFIG_FILE_INVALID', '静态目录格式无效', first)
                allowed_root(args[0], first)
                contexts[-1]['root'] = True
            elif key == 'disable_symlinks':
                if args != ['on']:
                    fail('CONFIG_FILE_UNSUPPORTED', '站点编辑仅支持 disable_symlinks on', first)
                contexts[-1]['symlinks'] = True
            elif key == 'proxy_ignore_headers':
                if args != ['X-Accel-Redirect']:
                    fail('CONFIG_FILE_UNSUPPORTED', '站点编辑仅支持忽略 X-Accel-Redirect 内部跳转头', first)
                contexts[-1]['ignore_redirect'] = True
            elif key == 'proxy_pass' and stack[-1] == 'location':
                if len(args) != 1 or not re.fullmatch(r'https?://[A-Za-z0-9_.:\[\]/-]+', args[0]):
                    fail('CONFIG_FILE_UNSUPPORTED', '代理地址须为不含账号和变量的 HTTP 或 HTTPS 地址', first)
                contexts[-1]['proxy'] = True
            elif key == 'proxy_set_header' and stack[-1] == 'location':
                if len(args) != 2 or args[0].lower() not in headers:
                    fail('CONFIG_FILE_UNSUPPORTED', '代理请求头不在允许编辑的范围内', first)
            elif key == 'add_header':
                if not args or args[0].lower() in ('authorization', 'proxy-authorization', 'set-cookie'):
                    fail('CONFIG_FILE_UNSUPPORTED', '认证信息需要通过凭据模板维护', first)
            elif key == 'index':
                if not args or not all(re.fullmatch(r'[A-Za-z0-9_.-]+', x) and not x.startswith('.') for x in args):
                    fail('CONFIG_FILE_UNSUPPORTED', '首页只允许填写本目录中的文件名', first)
                contexts[-1]['index'] = True
            elif key == 'try_files':
                if not args or not all(x in ('$uri', '$uri/') or re.fullmatch(r'=[1-5][0-9]{2}|/[A-Za-z0-9_./-]*|@[A-Za-z0-9_-]+', x) for x in args) or any('..' in x.split('/') for x in args):
                    fail('CONFIG_FILE_UNSUPPORTED', '文件匹配只允许当前站点内的路径', first)
                contexts[-1]['tryfiles'] = True
            elif key == 'autoindex':
                if args != ['off']:
                    fail('CONFIG_FILE_UNSUPPORTED', '禁止通过站点编辑器开启目录浏览', first)
                contexts[-1]['autoindex'] = True
            elif key == 'return':
                if not args or not re.fullmatch(r'[1-5][0-9]{2}', args[0]) or len(args) > 2 or any('$' in x for x in args):
                    fail('CONFIG_FILE_UNSUPPORTED', 'return 需要指定 HTTP 状态码', first)
                contexts[-1]['terminal'] = True
            elif key == 'proxy_cache':
                if args != ['off']:
                    fail('CONFIG_FILE_UNSUPPORTED', '缓存存储需要通过配置模板管理', first)
            elif key not in common and not (stack[-1] == 'location' and key in location):
                fail('CONFIG_FILE_UNSUPPORTED', '此配置项需要通过管理模板维护：' + key, first)
            if not args or any('\0' in x or len(x) > 2048 for x in args):
                fail('CONFIG_FILE_INVALID', '配置参数无效', first)
            if key not in ('proxy_set_header', 'try_files') and any('$' in x for x in args):
                fail('CONFIG_FILE_UNSUPPORTED', '变量只能用于指定的代理请求头和文件匹配配置', first)
        else:
            head.append((token, line))
    if stack or head or not 1 <= servers <= 20:
        fail('CONFIG_FILE_INVALID', '配置需要包含完整的 server 块')


def state():
    if docker:
        value = run(['/usr/bin/docker', 'inspect', '--format', '{{.State.Status}}', locator]).strip()
        if value == 'running':
            return 'RUNNING'
        if value in ('exited', 'created'):
            return 'STOPPED'
    else:
        value = run(['/usr/bin/systemctl', 'show', '--property=ActiveState', '--value', '--', locator]).strip()
        if value == 'active':
            return 'RUNNING'
        if value == 'failed':
            return 'FAILED'
        if value == 'inactive':
            return 'STOPPED'
    fail('CONFIG_FILE_STATE_UNCONFIRMED', '服务状态尚未确认，请先刷新软件状态')


def workers():
    if docker:
        output = run(['/usr/bin/docker', 'top', locator, '-eo', 'pid,args'])
    else:
        pid = run(['/usr/bin/systemctl', 'show', '--property=MainPID', '--value', '--', locator]).strip()
        if not pid.isdecimal() or pid == '0':
            fail('CONFIG_FILE_STATE_UNCONFIRMED', '无法确认 Nginx 主进程')
        output = run(['/bin/ps', '--ppid', pid, '-o', 'pid=,args='])
    return sorted(match[1] for line in output.splitlines()
                  if (match := re.fullmatch(r'\s*([0-9]+)\s+nginx: worker process\s*', line)))


def reload():
    before = workers()
    if docker:
        verify_container_config()
        verify_live_content()
        run(['/usr/bin/docker', 'exec', locator, 'nginx', '-s', 'reload'])
    elif official:
        run(['/usr/bin/systemctl', 'reload', '--', locator])
    else:
        run(native_nginx(main, ['-s', 'reload']))
    deadline = time.monotonic() + 15
    while time.monotonic() < deadline:
        actual = workers()
        generation = sorted(set(actual) - set(before))
        if generation:
            time.sleep(.25)
            if set(generation).issubset(workers()):
                return generation
        time.sleep(.25)
    fail('CONFIG_FILE_RELOAD_FAILED', 'Nginx 未确认加载新配置，正在恢复原配置')


def render(current, replacements):
    global candidate_lines
    original = read(base) if base.exists() else read(main)
    if not base.exists() and current:
        fail('CONFIG_FILE_UNSUPPORTED', '缺少原始主配置基线，请先核对现有站点记录')
    closing = original.rfind('}')
    if closing < 0 or original[closing + 1:].strip() or sid not in original:
        fail('CONFIG_FILE_UNSUPPORTED', '主配置结尾无法安全合并，请通过配置模板维护')
    fragments = {}
    for key, value in current.items():
        if value['enabled']:
            fragments[key] = read(value['path'])
    def ordered(values):
        return sorted(values.items(), key=lambda item: item[0] + '.conf')

    def merge(values):
        fragments_text = '\n'.join(v for k, v in ordered(values) if v is not None)
        return original[:closing] + '\n' + fragments_text + '\n}\n'
    live = read(main)
    if base.exists() and live != merge(fragments):
        # Older templates concatenate each fragment directly after stripping
        # the final brace from the baseline's last line with sed.
        legacy_base = re.sub(r'}[ \t]*(\n)?\Z', lambda match: match[1] or '', original)
        legacy = legacy_base + ''.join(v for _, v in ordered(fragments)) + '\n}\n'
        if live != legacy:
            fail('CONFIG_FILE_CHANGED', '主配置与当前站点文件不一致，请核对服务器上的修改')
    fragments.update(replacements)
    before = original[:closing] + '\n'
    for key, value in ordered(fragments):
        if value is None:
            continue
        if key == name:
            start = before.count('\n') + 1
            candidate_lines = (start, start + len(value.splitlines()) - 1)
            break
        before += value + '\n'
    merged = merge(fragments)
    if len(merged.encode()) > limit:
        fail('CONFIG_FILE_LIMIT', '合并后的 Nginx 配置超过 512 KiB')
    return original, merged


def candidate_check(current, content, enabled, deleted=False, check_disabled=True):
    validate_enabled = enabled or check_disabled and action == 'save'
    replacements = {name: content if validate_enabled and not deleted else None}
    candidate = work / 'nginx-candidate.conf'
    candidate_text = None
    if official:
        shadow = work / 'nginx-shadow'
        if shadow.exists():
            protected(shadow)
            shutil.rmtree(shadow)
        baseline = official_snapshot()
        includes = [t[0] for t in tokens(read(main))]
        if not any(includes[i:i+3] == ['include', '/etc/nginx/conf.d/*.conf', ';'] for i in range(len(includes)-2)):
            fail('CONFIG_FILE_UNSUPPORTED', '主配置未直接引用 conf.d 站点目录，请先核对 include 配置')
        protected(pathlib.Path('/etc/nginx/conf.d'))
        shutil.copytree('/etc/nginx', shadow, symlinks=True)
        target = shadow / 'conf.d' / site_path(name).name
        protected(shadow / 'conf.d')
        protected(target, missing=True)
        if validate_enabled and not deleted:
            target.write_text(content, encoding='utf-8')
        else:
            target.unlink(missing_ok=True)
        try:
            run(['/usr/bin/unshare', '--mount', '--propagation', 'private', '/bin/sh', '-c',
                 'mount --bind "$1" /etc/nginx && exec /usr/sbin/nginx -t', 'ops-nginx-check', str(shadow)], check_names=True)
            if official_snapshot() != baseline:
                fail('CONFIG_FILE_CHANGED', 'Nginx 配置目录在检查期间发生变化，请重新检查')
        finally:
            shutil.rmtree(shadow)
    else:
        _, candidate_text = render(current, replacements)
        save_bytes(candidate, candidate_text.encode())
        try:
            if docker:
                docker_check(candidate)
            else:
                run(native_nginx(candidate, ['-t']), check_names=True)
        finally:
            candidate.unlink(missing_ok=True)
    return candidate_text


def docker_check(candidate):
    if state() == 'RUNNING':
        verify_live_content()
        remote_dir = run(['/usr/bin/docker', 'exec', locator, 'mktemp', '-d', '/tmp/.ops-nginx-check.XXXXXX']).strip()
        if not re.fullmatch(r'/tmp/\.ops-nginx-check\.[A-Za-z0-9]{6,20}', remote_dir):
            fail('CONFIG_FILE_UNSAFE', '容器未返回安全的配置检查目录')
        remote = remote_dir + '/candidate.conf'
        try:
            run(['/usr/bin/docker', 'cp', str(candidate), locator + ':' + remote])
            run(['/usr/bin/docker', 'exec', locator, 'nginx', '-t', '-c', remote], check_names=True)
        finally:
            run(['/usr/bin/docker', 'exec', locator, 'rm', '-f', remote])
            run(['/usr/bin/docker', 'exec', locator, 'rmdir', remote_dir])
        return
    image = run(['/usr/bin/docker', 'inspect', '--format', '{{.Image}}', locator]).strip()
    if not re.fullmatch(r'sha256:[a-f0-9]{64}', image):
        fail('CONFIG_FILE_UNREADABLE', '无法确认原容器镜像或候选挂载路径')
    job_id = os.environ['OPS_JOB_ID']
    if not re.fullmatch(r'[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}', job_id):
        fail('CONFIG_FILE_UNSAFE', '缺少本次检查的独立作业标识')
    helper = 'ops-nginx-check-' + job_id
    try:
        run(['/usr/bin/docker', 'run', '--name', helper, '--rm', '--network', 'none',
             '--volumes-from', locator + ':ro', '--mount', 'type=bind,src=' + str(candidate) + ',dst=/tmp/ops-candidate.conf,readonly',
             '--entrypoint', 'nginx', image, '-t', '-c', '/tmp/ops-candidate.conf'], check_names=True)
    finally:
        subprocess.run(['/usr/bin/docker', 'rm', '-f', helper], stdout=subprocess.DEVNULL,
                       stderr=subprocess.DEVNULL, timeout=15)


def transaction_files(current):
    paths = [site_path(name), site_path(name, False)]
    if official:
        paths.append(sites / (name + '.conf'))
    if not official:
        paths += [main, base]
    return list(dict.fromkeys(paths))


def same_files(values, field):
    return all(digest(pathlib.Path(item['path'])) == item[field] for item in values)


def original_metadata(values):
    for item in values:
        if item['before'] == 'ABSENT':
            continue
        info = pathlib.Path(item['path']).stat()
        if [stat.S_IMODE(info.st_mode), info.st_uid, info.st_gid] != item['metadata']:
            return False
    return True


def overwrite(path, text, metadata=None):
    if not (docker and path == main):
        mode = metadata[0] if metadata else (None if path.exists() else 0o600 if path == base else 0o644)
        owner = (metadata[1], metadata[2]) if metadata else None
        save_bytes(path, text.encode(), mode, owner)
        return
    protected(path, missing=True)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o644)
    with os.fdopen(fd, 'w', encoding='utf-8') as stream:
        if metadata:
            os.fchown(stream.fileno(), metadata[1], metadata[2])
            os.fchmod(stream.fileno(), metadata[0])
        stream.write(text)
        stream.flush()
        os.fsync(stream.fileno())


def rollback():
    if not manifest.exists():
        emit(dict(status='PASSED', message='当前文件未修改'), '当前文件未修改')
        return
    record = json.loads(read(manifest))
    verify_record(record)
    if not all(digest(pathlib.Path(item['path'])) in (item['before'], item['after']) for item in record['files']):
        fail('CONFIG_FILE_CHANGED', '配置在任务后发生变化，已保留备份，请先核对原操作')
    if state() != record['state']:
        fail('CONFIG_FILE_STATE_CHANGED', '服务状态在任务后发生变化，已保留备份，请先核对原操作')
    backups = {}
    for item in record['files']:
        if item['before'] != 'ABSENT':
            text = read(work / item['backup'])
            if hashlib.sha256(text.encode()).hexdigest() != item['before']:
                fail('CONFIG_FILE_ROLLBACK_UNCONFIRMED', '原配置备份摘要不一致，请核对原任务')
            backups[item['path']] = text
    for item in record['files']:
        path = pathlib.Path(item['path'])
        if item['before'] == 'ABSENT':
            remove_file(path)
        else:
            overwrite(path, backups[item['path']], item['metadata'])
    if record['state'] == 'RUNNING':
        test_live(record['state'])
        reload()
    if not same_files(record['files'], 'before') or not original_metadata(record['files']) or state() != record['state']:
        fail('CONFIG_FILE_ROLLBACK_UNCONFIRMED', '原文件已恢复，服务状态仍需核对')
    record['restored'] = True
    write_json(manifest, record)
    emit(dict(status='PASSED', message='已恢复原配置'), '已恢复原配置')


def verify_record(record):
    if not isinstance(record, dict) or record.get('locator') != locator or record.get('softwareId') != sid or record.get('site') != name or record.get('action') != action:
        fail('CONFIG_FILE_UNCONFIRMED', '配置修改记录与当前软件不一致')
    expected_paths = {str(p) for p in transaction_files({})}
    if not isinstance(record.get('files'), list) or not all(isinstance(item, dict) for item in record['files']) or {item.get('path') for item in record['files']} != expected_paths:
        fail('CONFIG_FILE_UNCONFIRMED', '配置备份范围与当前操作不一致')
    for item in record['files']:
        if not isinstance(item.get('backup'), str) or not re.fullmatch(r'nginx-backup-[0-9]\.conf', item['backup']) or not all(re.fullmatch(r'[a-f0-9]{64}|ABSENT', item.get(key, '')) for key in ('before', 'after')):
            fail('CONFIG_FILE_UNCONFIRMED', '配置备份记录不完整，请核对原任务')
        metadata = item.get('metadata')
        if item['before'] != 'ABSENT' and (not isinstance(metadata, list) or len(metadata) != 3 or not all(type(x) is int for x in metadata) or metadata[0] < 0 or metadata[0] > 0o7777 or metadata[0] & 0o022 or metadata[1] != 0 or metadata[2] < 0):
            fail('CONFIG_FILE_UNCONFIRMED', '配置备份权限记录不完整，请核对原任务')


def execute():
    protected(root, missing=official)
    protected(config, missing=official)
    protected(work)
    protected(result, missing=True)
    verify_container_config()
    if phase == 'ROLLBACK' and action in ('save', 'enable', 'disable', 'delete'):
        # An interrupted write can leave only one of the official file copies;
        # reconcile the journal before attempting to list the live files.
        rollback()
        return
    current = entries()
    if action == 'list':
        files = [dict(id=key, name=key + '.conf', path=str(value['path']), enabled=value['enabled'],
                      managed=True, editable=True) for key, value in sorted(current.items())]
        files.insert(0, dict(id='@main', name='nginx.conf', path=str(main), enabled=True, managed=False, editable=False))
        if official:
            active = pathlib.Path('/etc/nginx/sites-enabled')
            protected(active, missing=True)
            protected(pathlib.Path('/etc/nginx/sites-available'), missing=True)
            external = [p for p in pathlib.Path('/etc/nginx/conf.d').glob('*.conf')
                        if not p.name.startswith('ops-' + sid + '-')]
            external.extend(p for p in active.glob('*') if p.is_file() and not p.name.startswith('.'))
            for p in pathlib.Path('/etc/nginx/sites-available').glob('*'):
                if p.is_file() and not p.name.startswith('.') and not (active / p.name).exists():
                    external.append(p)
            for path in sorted(external)[:max(0, 200 - len(files))]:
                identifier = '@external-' + hashlib.sha256(str(path).encode()).hexdigest()
                files.append(dict(id=identifier, name=path.name, path=str(path), enabled=path.parent.name != 'sites-available', managed=False, editable=False))
        while len(json.dumps(files, ensure_ascii=False).encode()) > 96 * 1024:
            files.pop()
        emit(dict(files=files, canCreate=len(current) < 199, revision=revision('', current), listen=allowed_listen), '已读取配置文件列表')
        return
    if name == '@main' or name.startswith('@external-'):
        if action != 'read':
            fail('CONFIG_FILE_READ_ONLY', '此配置文件需先确认接管后再编辑')
        emit(dict(content='', revision='', editable=False, message='此配置仅展示位置，尚未接管'), '已读取配置文件位置')
        return
    site_path(name)
    if action == 'read':
        if name not in current:
            fail('CONFIG_FILE_MISSING', '站点配置不存在，请刷新列表')
        try:
            text = read_body(current[name]['path'])
            validate(text, enforce_safety=False)
        except ConfigError:
            emit(dict(content='', revision=revision(name, current), editable=False,
                      message='现有配置包含模板外的配置项，请使用原管理模板维护'), '已读取配置文件位置')
            return
        emit(dict(content=text, revision=revision(name, current), editable=True), '已读取配置文件')
        return
    if action not in ('save', 'enable', 'disable', 'delete'):
        fail('CONFIG_FILE_INVALID', '配置文件操作无效')
    if phase in ('VERIFY', 'VERIFY_RESTORED'):
        if not manifest.exists():
            fail('CONFIG_FILE_UNCONFIRMED', '缺少本次配置修改记录')
        record = json.loads(read(manifest))
        verify_record(record)
        if phase == 'VERIFY_RESTORED':
            if not record.get('restored') or not same_files(record['files'], 'before') or not original_metadata(record['files']) or state() != record['state']:
                fail('CONFIG_FILE_UNCONFIRMED', '无法确认原配置已恢复')
            emit(dict(status='PASSED', message='已恢复原配置'), '已恢复原配置')
            return
        if record.get('restored'):
            fail('CONFIG_FILE_UNCONFIRMED', '原配置已恢复，未应用本次修改')
        if not same_files(record['files'], 'after') or state() != record['state']:
            fail('CONFIG_FILE_CHANGED', '配置文件或服务状态与原任务不一致')
        if record['state'] == 'RUNNING':
            actual = set(workers())
            recorded = set(record.get('workers', []))
            previous = set(record.get('beforeWorkers', []))
            if not recorded or not recorded.issubset(actual) or not recorded.difference(previous):
                fail('CONFIG_FILE_UNCONFIRMED', '无法确认本次重载后的工作进程')
        emit(dict(status='PASSED', message='配置与运行状态已验证'), '配置与运行状态已验证')
        return
    require_revision(current)
    if action != 'save' and name not in current:
        fail('CONFIG_FILE_MISSING', '站点配置不存在，请刷新列表')
    if action == 'save':
        body = pathlib.Path(os.environ['OPS_CONFIG_CONTENT_FILE'])
        try:
            body.relative_to(work)
        except ValueError:
            fail('CONFIG_FILE_UNSAFE', '配置内容临时文件位置不安全')
        content = read_body(body)
    else:
        content = read_body(current[name]['path'])
    enabled = current.get(name, dict(enabled=True))['enabled']
    if action == 'enable':
        enabled = True
    if action == 'disable':
        enabled = False
    if action in ('save', 'enable'):
        validate(content)
    if phase == 'CHECK':
        state()
        candidate_check(current, content, enabled, action == 'delete')
        emit(dict(status='PASSED', message='完整配置检查通过'), '完整配置检查通过')
        return
    if phase != 'APPLY':
        fail('CONFIG_FILE_INVALID', '配置文件操作阶段无效')
    previous = state()
    expected_state = os.environ.get('OPS_PREVIOUS_STATE', '')
    if expected_state and previous != expected_state:
        fail('CONFIG_FILE_STATE_CHANGED', '服务状态已变化，请重新检查')
    candidate_text = candidate_check(current, content, enabled, action == 'delete')
    if not enabled and action == 'save':
        candidate_text = candidate_check(current, content, enabled, action == 'delete', check_disabled=False)
    current = entries()
    require_revision(current)
    if state() != previous:
        fail('CONFIG_FILE_STATE_CHANGED', '服务状态在检查过程中发生变化，请重新检查')
    protected(sites, missing=True)
    if official:
        config.mkdir(mode=0o750, parents=True, exist_ok=True)
    sites.mkdir(mode=0o750, exist_ok=True)
    target, disabled = site_path(name), site_path(name, False)
    changes = {str(target): content if enabled and action != 'delete' else None,
               str(disabled): content if not enabled and action != 'delete' else None}
    if official:
        changes[str(sites / (name + '.conf'))] = content if enabled and action != 'delete' else None
    else:
        changes[str(main)] = candidate_text
        changes[str(base)] = read(base) if base.exists() else read(main)
    files = []
    for index, path in enumerate(transaction_files(current)):
        protected(path, missing=True)
        item = dict(path=str(path), before=digest(path), backup='nginx-backup-' + str(index) + '.conf')
        if path.exists():
            info = path.stat()
            item['metadata'] = [stat.S_IMODE(info.st_mode), info.st_uid, info.st_gid]
            save_bytes(work / item['backup'], path.read_bytes())
        after = changes[str(path)]
        item['after'] = hashlib.sha256(after.encode()).hexdigest() if after is not None else 'ABSENT'
        files.append(item)
    record = dict(state=previous, files=files, site=name, action=action, locator=locator, softwareId=sid,
                  beforeWorkers=workers() if previous == 'RUNNING' else [], restored=False)
    write_json(manifest, record)
    if not same_files(files, 'before'):
        fail('CONFIG_FILE_CHANGED', '配置文件在保存前发生变化，请重新读取')
    for item in files:
        path = pathlib.Path(item['path'])
        value = changes[str(path)]
        if value is None:
            remove_file(path)
        else:
            overwrite(path, value)
    if not same_files(files, 'after'):
        fail('CONFIG_FILE_UNCONFIRMED', '保存后的配置摘要不一致，请核对原操作')
    test_live(previous)
    if previous == 'RUNNING':
        record['workers'] = reload()
    write_json(manifest, record)
    emit(dict(status='PASSED', message='配置已加载' if previous == 'RUNNING' else '配置已保存，启动后生效'),
         '配置已加载' if previous == 'RUNNING' else '配置已保存，启动后生效')


try:
    protected(work)
    protected(result, missing=True)
    if sys.version_info < (3, 9):
        fail('CONFIG_FILE_PYTHON_REQUIRED', '配置文件编辑需要服务器已有 Python 3.9 或更新版本')
    execute()
except ConfigError as exc:
    emit(dict(status='FAILED', code=exc.code, message=exc.message, line=exc.line), '配置操作未完成')
    raise SystemExit(1)
except (OSError, ValueError, KeyError, TypeError, subprocess.TimeoutExpired):
    emit(dict(status='FAILED', code='CONFIG_FILE_UNCONFIRMED', message='无法确认配置操作结果，请核对原任务'), '配置操作未完成')
    raise SystemExit(1)
PY
