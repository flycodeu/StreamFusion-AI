def gitlab_memory_path():
    return unit_path.parent/(unit + '.d')/('streamfusion-ops-' + sid + '.conf')

def runtime_files():
    return app_files() + ([root/'config/gitlab-memory.conf', gitlab_memory_path()] if app == 'gitlab' else [unit_path])

def runtime_actual():
    values = app_actual()
    memory = run(['systemctl', 'show', '--property=MemoryMax', '--value', unit]).strip()
    values['MEMORY_MIB'] = 0 if memory == 'infinity' else int(memory)//1048576
    if app != 'gitlab' and property_value(read(unit_path), 'Description') != 'Ops ' + app + ' ' + sid: fail('服务单元归属不符')
    return values

def runtime_validate(values):
    if app == 'gitlab':
        memory = root/'config/gitlab-memory.conf'
        override = gitlab_memory_path()
        if override.exists() and (not memory.exists() or read(override) != read(memory)): fail('内存配置片段已被其他操作修改')

def runtime_snapshot():
    pass

def runtime_stop():
    if app == 'gitlab': run(['/opt/gitlab/bin/gitlab-ctl', 'stop'], 180)
    run(['systemctl', 'stop', unit], 180)

def runtime_apply(values):
    app_apply(values)
    memory = str(values['MEMORY_MIB']) + 'M' if values['MEMORY_MIB'] else 'infinity'
    if app == 'gitlab':
        path = root/'config/gitlab-memory.conf'
        write(path, '[Service]\nMemoryMax=' + memory + '\n')
        directory = gitlab_memory_path().parent
        directory.mkdir(mode=0o755, exist_ok=True)
        destination = gitlab_memory_path()
        if destination.exists() and read(destination) != read(path) and not read(destination).startswith('[Service]\nMemoryMax='): fail('内存配置片段已被其他操作修改')
        write(destination, read(path))
    else:
        write(unit_path, replace_property(read(unit_path), 'MemoryMax', memory))

def runtime_start():
    run(['systemctl', 'daemon-reload'])
    if previous == 'RUNNING':
        run(['systemctl', 'start', unit], 180)
        if app == 'gitlab': run(['/opt/gitlab/bin/gitlab-ctl', 'start'], 180)
    elif app == 'gitlab':
        run(['/opt/gitlab/bin/gitlab-ctl', 'stop'], 180); run(['systemctl', 'stop', unit], 180)

def runtime_verify(values):
    state = run(['systemctl', 'show', '--property=ActiveState', '--value', unit]).strip()
    if state != ('active' if previous == 'RUNNING' else 'inactive'): fail('服务运行状态与操作前不符')
    if previous == 'RUNNING': app_health(values)

def runtime_restore():
    runtime_stop(); restore_files()
    if app == 'gitlab':
        gitlab_reconfigure()
    runtime_start()
