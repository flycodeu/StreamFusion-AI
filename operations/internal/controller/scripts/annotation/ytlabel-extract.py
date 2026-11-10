import hashlib, re, shutil, sys, tarfile
from pathlib import Path

archive, destination, mode = Path(sys.argv[1]), Path(sys.argv[2]), sys.argv[3]
prefix = 'ytlabel-@@COMMIT@@/'
def digest(path):
    result = hashlib.sha256()
    with path.open('rb') as source:
        for chunk in iter(lambda: source.read(1 << 20), b''):
            result.update(chunk)
    return result.hexdigest()

with tarfile.open(archive, 'r:gz') as bundle:
    entries = bundle.getmembers()
    if len(entries) > 1024 or sum(item.size for item in entries) > (2 << 30):
        sys.exit('安装材料展开后过大或文件过多')
    paths = set()
    files = {}
    for item in entries:
        name = item.name
        if mode == 'source':
            if name == prefix.rstrip('/') and item.isdir():
                continue
            if not name.startswith(prefix):
                sys.exit('源码包目录与固定版本不一致')
            name = name[len(prefix):]
        if not name:
            continue
        if name in paths or not re.fullmatch(r'[A-Za-z0-9_.+/-]+', name) or name.startswith('/') or any(part in ['', '.', '..'] for part in name.rstrip('/').split('/')):
            sys.exit('安装包包含不安全路径或重复文件')
        paths.add(name)
        if not (item.isdir() or item.isfile()):
            sys.exit('安装包不能包含链接或特殊文件')
        if mode == 'offline' and not (name in ['source.tar.gz', 'SHA256SUMS', 'wheelhouse'] or re.fullmatch(r'wheelhouse/[A-Za-z0-9_.+-]+\.whl', name)):
            sys.exit('离线包应包含 source.tar.gz、wheelhouse/*.whl 和 SHA256SUMS')
        target = destination / name
        if item.isdir():
            target.mkdir(parents=True, exist_ok=True)
        else:
            target.parent.mkdir(parents=True, exist_ok=True)
            with bundle.extractfile(item) as source, target.open('xb') as output:
                shutil.copyfileobj(source, output, 1 << 20)
            files[name] = target
    if mode == 'offline':
        if 'source.tar.gz' not in files or 'SHA256SUMS' not in files or not any(name.endswith('.whl') for name in files):
            sys.exit('离线包缺少固定源码、依赖 wheel 或摘要清单')
        seen = set()
        for line in files['SHA256SUMS'].read_text(encoding='ascii').splitlines():
            match = re.fullmatch(r'([a-fA-F0-9]{64})  ([A-Za-z0-9_.+/-]+)', line)
            if not match or match[2] in seen or match[2] not in files or match[2] == 'SHA256SUMS':
                sys.exit('离线摘要清单有重复或不匹配条目')
            seen.add(match[2])
            if digest(files[match[2]]) != match[1].lower():
                sys.exit('离线文件摘要不一致：' + match[2])
        if seen != set(files) - {'SHA256SUMS'}:
            sys.exit('离线摘要清单未完整覆盖所有文件')
