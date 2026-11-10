def managed_files():
    return [root/'config/runtime.env', root/'bin/run', unit_path, meta]

def environment():
    value = {}
    for line in read(root/'config/runtime.env').splitlines():
        words = shlex.split(line)
        if len(words) == 2 and words[0] == 'export' and '=' in words[1]:
            key, item = words[1].split('=', 1)
            value[key] = item
    return value

def actual():
    env, service = environment(), fields(read(unit_path))
    if service.get('Description') != 'Ops Label Studio ' + sid:
        fail('服务单元归属不符')
    words = shlex.split(next(line[5:] for line in read(root/'bin/run').splitlines() if line.startswith('exec ')))
    memory = service.get('MemoryMax', '')
    if not re.fullmatch(r'[0-9]+M', memory):
        fail('无法读取服务内存配置')
    source = env.get('LABEL_STUDIO_LOCAL_FILES_DOCUMENT_ROOT', '') if env.get('LABEL_STUDIO_LOCAL_FILES_SERVING_ENABLED') == 'true' else ''
    return dict(BIND_ADDRESS=words[words.index('--internal-host')+1], WEB_PORT=int(words[words.index('--port')+1]), MAX_UPLOAD_MIB=int(env['DATA_UPLOAD_MAX_MEMORY_SIZE'])//1048576, SOURCE_DIRECTORY=source, MEMORY_MIB=int(memory[:-1]), DATA_DIRECTORY=env['LABEL_STUDIO_BASE_DATA_DIR'], PUBLIC_URL=env.get('LABEL_STUDIO_HOST',''))

def stop():
    run(['systemctl', 'stop', unit], 'systemctl stop ' + unit)

def start():
    run(['systemctl', 'daemon-reload'], 'systemctl daemon-reload')
    if previous_state == 'RUNNING':
        run(['systemctl', 'start', unit], 'systemctl start ' + unit)

def apply(value):
    env = environment()
    env.update(LABEL_STUDIO_BASE_DATA_DIR=value['DATA_DIRECTORY'], HOME=value['DATA_DIRECTORY'], DATA_UPLOAD_MAX_MEMORY_SIZE=str(value['MAX_UPLOAD_MIB']*1048576), LABEL_STUDIO_HOST=value['PUBLIC_URL'], LABEL_STUDIO_LOCAL_FILES_SERVING_ENABLED='true' if value['SOURCE_DIRECTORY'] else 'false', LABEL_STUDIO_LOCAL_FILES_DOCUMENT_ROOT=value['SOURCE_DIRECTORY'] or value['DATA_DIRECTORY'])
    write(root/'config/runtime.env', ''.join('export '+key+'='+shlex.quote(item)+'\n' for key,item in env.items()))
    script = '#!/bin/bash\nset -euo pipefail\nsource '+shlex.quote(str(root/'config/runtime.env'))+'\nexec '+shlex.join([str(root/'runtime/venv/bin/label-studio'), 'start', '--no-browser', '--internal-host', value['BIND_ADDRESS'], '--port', str(value['WEB_PORT'])])+'\n'
    write(root/'bin/run', script)
    lines = [line for line in read(unit_path).splitlines() if not line.startswith('ReadOnlyPaths=')]
    lines = [('MemoryMax='+str(value['MEMORY_MIB'])+'M') if line.startswith('MemoryMax=') else line for line in lines]
    if value['SOURCE_DIRECTORY']:
        index = lines.index('[Install]') if '[Install]' in lines else len(lines)
        lines.insert(index, 'ReadOnlyPaths='+value['SOURCE_DIRECTORY'])
    write(unit_path, '\n'.join(lines)+'\n')

def import_source(value, checking=False):
    if value['SOURCE_DIRECTORY']:
        user = fields(read(unit_path))['User']
        run(['runuser', '-u', user, '--', 'test', '-r', value['SOURCE_DIRECTORY']])
        run(['runuser', '-u', user, '--', 'test', '-x', value['SOURCE_DIRECTORY']])

def health(value):
    wait_http(value, '/health')
