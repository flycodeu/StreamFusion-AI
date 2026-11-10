internal_port = 6379
fields = dict(MAX_MEMORY_MIB=(0,16384), MAX_CLIENTS=(1,100000))
config = pathlib.Path('/etc/redis/redis.conf') if official else root/'config/redis.conf'


def redis_values(text):
    values = {}
    for line in text.splitlines():
        parts = line.strip().split()
        if not parts or parts[0].startswith('#'):
            continue
        if parts[0] in ('bind','port','maxmemory','maxclients'):
            if parts[0] in values:
                fail('Redis 配置项重复，请先整理：'+parts[0])
            values[parts[0]] = parts[1:]
    return values


def actual():
    values = redis_values(read(config))
    address = values.get('bind',['127.0.0.1'])[0]
    if address.startswith('-'):
        address = address[1:]
    memory = values.get('maxmemory',['0'])[0].lower()
    parsed = re.fullmatch(r'(\d+)(kb|mb|gb|k|m|g)?',memory)
    if not parsed:
        fail('Redis 内存配置格式无法识别')
    result = dict(BIND_ADDRESS=address, CONFIG_PORT=int(values.get('port',['6379'])[0]), MEMORY_MIB=native_memory() if mode!='DOCKER' else 128, MAX_MEMORY_MIB=int(parsed[1])*{'':1,None:1,'k':1000,'kb':1024,'m':1000000,'mb':1048576,'g':1000000000,'gb':1073741824}[parsed[2]]//1048576, MAX_CLIENTS=int(values.get('maxclients',['10000'])[0]))
    if mode=='DOCKER':
        result.update(network_value())
    return result


def managed_files():
    return [config]+([] if mode=='DOCKER' else [memory_file()])


def validate(value):
    redis_values(read(config))
    if value['MEMORY_MIB'] and value['MAX_MEMORY_MIB'] > value['MEMORY_MIB']:
        fail('Redis 数据内存不能超过服务内存上限')
    if mode=='DOCKER':
        docker_plan(docker_inspect(locator),memory_mib=value['MEMORY_MIB'])


def apply(value):
    text = read(config)
    old=redis_values(text)
    before=actual()
    addresses=old.get('bind',['127.0.0.1'])
    addresses[0]='0.0.0.0' if mode=='DOCKER' else value['BIND_ADDRESS']
    values = dict(bind=' '.join(addresses), port=str(internal_port if mode=='DOCKER' else value['CONFIG_PORT']), maxmemory=str(value['MAX_MEMORY_MIB'])+'mb', maxclients=str(value['MAX_CLIENTS']))
    if value['MAX_MEMORY_MIB']==before['MAX_MEMORY_MIB']:
        values.pop('maxmemory')
    if value['MAX_CLIENTS']==before['MAX_CLIENTS']:
        values.pop('maxclients')
    for key,item in values.items():
        pattern = r'(?m)^\s*'+re.escape(key)+r'\s+[^\n]*$'
        text,count = re.subn(pattern,key+' '+item,text)
        if not count:
            text += '\n'+key+' '+item+'\n'
    write(config,text)


def check(value):
    if previous_state=='STOPPED':
        return
    host = '127.0.0.1' if value['BIND_ADDRESS']=='0.0.0.0' else value['BIND_ADDRESS']
    deadline = time.monotonic()+40
    while time.monotonic()<deadline:
        try:
            with socket.create_connection((host,value['CONFIG_PORT']),timeout=2) as connection:
                connection.sendall(b'*1\r\n$4\r\nPING\r\n')
                reply = connection.recv(4096)
                if reply.startswith(b'+PONG') or reply.startswith(b'-NOAUTH'):
                    return
        except OSError:
            pass
        time.sleep(1)
    fail('Redis 端口未返回正常协议响应，请检查服务日志')
