internal_port = 3306
fields = dict(MAX_CONNECTIONS=(10,10000), BUFFER_POOL_MIB=(64,12288))
config = pathlib.Path('/etc/mysql/mysql.conf.d/mysqld.cnf') if official else root/'config/my.cnf'
if mode=='DOCKER':
    config = root/'config/service-settings.cnf'
auto = pathlib.Path('/var/lib/mysql/mysqld-auto.cnf') if official else root/'data/mysqld-auto.cnf'


def mysql_values(text):
    values,section = {},''
    for line in text.splitlines():
        line=line.strip()
        if not line or line[0] in '#;':
            continue
        if line.startswith('['):
            section=line.lower()
        elif section=='[mysqld]' and '=' in line:
            key,item=line.split('=',1)
            key=key.strip().replace('_','-')
            if key in ('port','bind-address','max-connections','innodb-buffer-pool-size'):
                if key in values:
                    fail('MySQL 配置项重复：'+key)
                values[key]=item.split('#',1)[0].strip()
    return values


def mysql_query(sql):
    password_file=os.environ.get('OPS_SECRET_ADMIN_PASSWORD_FILE')
    account=os.environ.get('OPS_PARAM_ADMIN_USER','root')
    if not re.fullmatch(r'[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,31}',account):
        fail('MySQL 管理账号格式无效')
    options=None
    if password_file:
        password=read(password_file)
        if len(password)>8192 or any(char in password for char in '\r\n\x00'):
            fail('MySQL 管理密码格式无效')
        password=password.replace('\\','\\\\').replace('"','\\"')
        options='[client]\nuser="'+account+'"\npassword="'+password+'"\nprotocol=socket\n'
    elif account!='root':
        fail('使用其他管理账号时请选择管理密码')
    if mode=='DOCKER':
        args=['docker','exec','-i',container_name if action=='verify' else locator,'mysql','--defaults-extra-file='+('/dev/stdin' if options else '/run/secrets/mysql-health.cnf'),'--protocol=socket']
    elif official:
        args=['/usr/bin/mysql']+(['--defaults-extra-file=/dev/stdin'] if options else ['--user=root'])+['--protocol=socket']
    else:
        args=[str(root/'runtime/mysql/bin/mysql'),'--defaults-extra-file='+('/dev/stdin' if options else str(root/'config/health.cnf')),'--protocol=socket','--socket='+str(root/'data/mysql.sock')]
    return run(args+['--batch','--skip-column-names','--connect-timeout=5','--execute='+sql],timeout=15,input=options)


def actual():
    values=mysql_values(read(config)) if config.exists() else {}
    result=dict(ADMIN_USER=os.environ.get('OPS_PARAM_ADMIN_USER','root'),BIND_ADDRESS=values.get('bind-address','127.0.0.1'),CONFIG_PORT=int(values.get('port','3306')),MEMORY_MIB=native_memory() if mode!='DOCKER' else 128,MAX_CONNECTIONS=int(values.get('max-connections','151')))
    raw=values.get('innodb-buffer-pool-size','128M')
    parsed=re.fullmatch(r'(\d+)([KMGkmg]?)',raw)
    if not parsed:
        fail('MySQL 缓冲池大小无法识别')
    result['BUFFER_POOL_MIB']=int(parsed[1])*{'':1,'K':1024,'M':1048576,'G':1073741824}[parsed[2].upper()]//1048576
    if previous_state=='RUNNING':
        # Read only the public allowlist; credentials and account rows never
        # enter management output or the execution log.
        parts=mysql_query('SELECT @@port,@@bind_address,@@max_connections,@@innodb_buffer_pool_size;').split('\t')
        if len(parts)!=4:
            fail('无法读取 MySQL 实际配置，请核对管理凭据')
        result.update(CONFIG_PORT=int(parts[0]),BIND_ADDRESS=parts[1],MAX_CONNECTIONS=int(parts[2]),BUFFER_POOL_MIB=int(parts[3])//1048576)
    if mode=='DOCKER':
        result.update(network_value())
    return result


def managed_files():
    return [config,auto]+([] if mode=='DOCKER' else [memory_file()])


def validate(value):
    if value['MEMORY_MIB'] and value['BUFFER_POOL_MIB'] > value['MEMORY_MIB']*3//4:
        fail('缓冲池不能超过服务内存上限的 75%')
    if config.exists():
        mysql_values(read(config))
    if auto.exists():
        data=json.loads(read(auto))
        if not isinstance(data.get('mysql_server'),dict):
            fail('MySQL 持久化配置格式无法确认')
    if mode=='DOCKER':
        docker_plan(docker_inspect(locator),memory_mib=value['MEMORY_MIB'])


def apply(value):
    if previous_state=='RUNNING':
        if mode=='DOCKER':
            _docker_api('POST','/containers/'+locator+'/stop?t=60')
        else:
            run(['systemctl','stop',locator])
    values={'port':str(internal_port if mode=='DOCKER' else value['CONFIG_PORT']),'bind-address':'0.0.0.0' if mode=='DOCKER' else value['BIND_ADDRESS'],'max-connections':str(value['MAX_CONNECTIONS']),'innodb-buffer-pool-size':str(value['BUFFER_POOL_MIB'])+'M'}
    text=read(config) if config.exists() else '[mysqld]\n'
    lines=text.splitlines(keepends=True)
    section=''
    output=[]
    found=set()
    inserted=False
    for line in lines+['[__ops_end__]\n']:
        stripped=line.strip()
        if stripped.startswith('['):
            if section=='[mysqld]':
                output += [key+'='+item+'\n' for key,item in values.items() if key not in found]
                inserted=True
            section=stripped.lower()
        if section=='[mysqld]' and '=' in stripped and not stripped.startswith(('#',';')):
            key=stripped.split('=',1)[0].strip().replace('_','-')
            if key in values:
                line=key+'='+values[key]+'\n'
                found.add(key)
        if stripped!='[__ops_end__]':
            output.append(line)
    if not inserted:
        fail('未找到 MySQL 服务配置段')
    fresh=not config.exists()
    write(config,''.join(output))
    if mode=='DOCKER' and fresh:
        group=safe(root/'config/health.cnf').stat().st_gid
        os.chown(config,0,group)
        os.chmod(config,0o640)
    if auto.exists():
        data=json.loads(read(auto))
        for key in values:
            data['mysql_server'].pop(key.replace('-','_'),None)
        write(auto,json.dumps(data))
    if mode=='DOCKER':
        original=json.loads(read(backup/'docker.json'))
        destination='/etc/mysql/conf.d/zz-ops-service.cnf'
        mounts=original.get('Mounts',[])
        if any(item['Destination']==destination and item['Source']!=str(config) for item in mounts):
            fail('MySQL 配置挂载已被其他文件使用')
        host=original['HostConfig']
        binds=host.get('Binds') or []
        if not any(item['Destination']==destination for item in mounts):
            host['Binds']=binds+[str(config)+':'+destination+':ro']
        write(backup/'docker.json',json.dumps(original))
    else:
        binary='/usr/sbin/mysqld' if official else str(root/'runtime/mysql/bin/mysqld')
        run([binary,'--defaults-file='+str(config),'--validate-config'])


def check(value):
    if previous_state=='STOPPED':
        return
    deadline=time.monotonic()+60
    while time.monotonic()<deadline:
        try:
            if mysql_query('SELECT 1;')=='1':
                return
        except RuntimeError:
            pass
        time.sleep(1)
    fail('MySQL 尚未通过连接验证，请检查服务日志')
