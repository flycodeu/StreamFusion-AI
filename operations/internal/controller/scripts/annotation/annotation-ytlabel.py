def managed_files():
    return [root/'config/ytlabel.json', unit_path, root/'bin/ytlabel-launch.py', meta]

def validate_application(value):
    pass

def actual():
    settings = load(root/'config/ytlabel.json')
    service = fields(read(unit_path))
    if service.get('Description') != 'Ops ytlabel ' + sid:
        fail('服务单元归属不符')
    source = settings.get('sourceDirectory', '')
    if not source:
        mount = service.get('BindReadOnlyPaths', '')
        if mount.endswith(':'+str(root/'data/uploads/samples')):
            source = mount.split(':', 1)[0]
    memory = service.get('MemoryMax', '')
    if not re.fullmatch(r'[0-9]+M', memory):
        fail('无法读取服务内存配置')
    return dict(BIND_ADDRESS=settings['host'], WEB_PORT=settings['port'], MAX_UPLOAD_MIB=settings['maxUploadMiB'], SOURCE_DIRECTORY=source, MEMORY_MIB=int(memory[:-1]), DATA_DIRECTORY=settings.get('dataDirectory', str(root/'data')))

def stop():
    run(['systemctl', 'stop', unit], 'systemctl stop ' + unit)

def start():
    run(['systemctl', 'daemon-reload'], 'systemctl daemon-reload')
    if previous_state == 'RUNNING':
        run(['systemctl', 'start', unit], 'systemctl start ' + unit)

def apply(value):
    settings = load(root/'config/ytlabel.json')
    settings.update(host=value['BIND_ADDRESS'], port=value['WEB_PORT'], maxUploadMiB=value['MAX_UPLOAD_MIB'], dataDirectory=value['DATA_DIRECTORY'], sourceDirectory=value['SOURCE_DIRECTORY'])
    save(root/'config/ytlabel.json', settings)
    launcher = read(root/'bin/ytlabel-launch.py')
    launcher = launcher.replace("os.chdir(root / 'data')", "os.chdir(Path(settings.get('dataDirectory', str(root / 'data'))))")
    write(root/'bin/ytlabel-launch.py', launcher)
    lines = []
    for line in read(unit_path).splitlines():
        if line.startswith(('BindReadOnlyPaths=', 'ExecStartPre=')) and '/uploads/samples' in line:
            continue
        if line.startswith('WorkingDirectory='):
            line = 'WorkingDirectory=' + value['DATA_DIRECTORY']
        elif line.startswith('ReadWritePaths='):
            line = 'ReadWritePaths=' + value['DATA_DIRECTORY'] + ' ' + str(root/'logs')
        elif line.startswith('MemoryMax='):
            line = 'MemoryMax=' + str(value['MEMORY_MIB']) + 'M'
        lines.append(line)
    write(unit_path, '\n'.join(lines)+'\n')

def import_source(value, checking=False):
    if not value['SOURCE_DIRECTORY']:
        return
    mounted = '/uploads/samples' in fields(read(unit_path)).get('BindReadOnlyPaths', '')
    if not mounted and value['SOURCE_DIRECTORY'] == actual()['SOURCE_DIRECTORY']:
        return
    import_samples(root, safe(value['DATA_DIRECTORY']), safe(value['SOURCE_DIRECTORY']), checking, limit)
    if not checking:
        print('素材导入完成，未覆盖同名文件，源文件已保留', flush=True)

def health(value):
    wait_http(value, '/', b'ytlabel')
