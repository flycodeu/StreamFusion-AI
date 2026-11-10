def app_files():
    return [root/'config/postgresql.conf']

def pg_value(key):
    matches = re.findall(r'(?m)^\s*' + key + r'\s*=\s*([^\n#]+)', read(root/'config/postgresql.conf'))
    if len(matches) != 1: fail('PostgreSQL 配置项重复或缺失：' + key)
    return matches[0].strip().strip("'")

def app_actual():
    return dict(BIND_ADDRESS=pg_value('listen_addresses'), WEB_PORT=int(pg_value('port')))

def pg_set(text, key, value):
    pattern = r'(?m)^\s*' + key + r'\s*=.*$'
    if len(re.findall(pattern, text)) != 1: fail('PostgreSQL 配置项重复或缺失：' + key)
    return re.sub(pattern, lambda _: key + ' = ' + value, text)

def app_apply(values):
    content = pg_set(read(root/'config/postgresql.conf'), 'listen_addresses', "'" + values['BIND_ADDRESS'] + "'")
    content = pg_set(content, 'port', str(values['WEB_PORT']))
    if values['MEMORY_MIB']: content = pg_set(content, 'shared_buffers', "'" + str(max(16, values['MEMORY_MIB']//4)) + "MB'")
    write(root/'config/postgresql.conf', content)

def app_health(values):
    run(['/usr/lib/postgresql/18/bin/pg_isready', '-h', str(root/'data/run'), '-p', str(values['WEB_PORT'])])
