def import_samples(root, data, source, checking=False, deadline=None):
    import hashlib, os, shutil, stat, time
    from pathlib import Path
    root, data, source = Path(root), Path(data), Path(source)
    deadline = deadline or time.monotonic()+1200
    def reject(message):
        raise RuntimeError(message)
    def digest(path):
        value = hashlib.sha256()
        with path.open('rb') as incoming:
            for chunk in iter(lambda: incoming.read(1048576), b''):
                if time.monotonic() > deadline:
                    reject('素材摘要检查超时')
                value.update(chunk)
        return value.hexdigest()
    def directory(path, create=False):
        for current in reversed([path]+list(path.parents)):
            if current.exists() or current.is_symlink():
                if current.is_symlink() or not current.is_dir() or current.resolve() != current:
                    reject('素材目标路径包含链接或非目录')
            elif create:
                current.mkdir(mode=0o750)
                os.chown(current, data.stat().st_uid, data.stat().st_gid)
    if source.resolve() != source or data.resolve() != data or source == data or source in data.parents or data in source.parents:
        reject('素材和数据目录须独立且不含链接')
    target = data/'uploads/samples'
    directory(target)
    pending, total, count = [], 0, 0
    for parent, dirs, files in os.walk(source, followlinks=False):
        for name in dirs+files:
            current = Path(parent)/name
            info = current.lstat()
            count += 1
            if count > 100000 or time.monotonic() > deadline:
                reject('素材数量或检查时间超过限制')
            if current.is_symlink() or not (stat.S_ISDIR(info.st_mode) or stat.S_ISREG(info.st_mode)):
                reject('素材包含链接或特殊文件')
            if not current.is_file():
                continue
            total += info.st_size
            if total > 128*1024**3:
                reject('单次素材导入最多 128 GiB')
            relative = str(current.relative_to(source))
            destination = target/relative
            directory(destination.parent)
            value = digest(current)
            if destination.exists() or destination.is_symlink():
                if destination.is_symlink() or not destination.is_file() or digest(destination) != value:
                    reject('素材与已有文件重名，未覆盖：'+relative)
            else:
                pending.append((relative, value, info.st_size))
    if shutil.disk_usage(data).free < sum(item[2] for item in pending)+67108864:
        reject('素材导入空间不足')
    if checking:
        return
    for relative, value, _ in pending:
        destination = target/relative
        directory(destination.parent, True)
        with (source/relative).open('rb') as incoming, destination.open('xb') as outgoing:
            shutil.copyfileobj(incoming, outgoing, 1048576)
        os.chown(destination, data.stat().st_uid, data.stat().st_gid)
        os.chmod(destination, 0o640)
        if digest(destination) != value:
            reject('素材内容在复制时变化，源文件和已复制文件均保留')
