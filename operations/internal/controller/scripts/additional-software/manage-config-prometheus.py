def app_files():
    return [root/'bin/run']

def app_actual():
    args = launch_args(); address, port = flag(args, '--web.listen-address').rsplit(':', 1)
    return dict(BIND_ADDRESS=address, WEB_PORT=int(port), RETENTION=flag(args, '--storage.tsdb.retention.time'))

def app_validate(values):
    if not re.fullmatch(r'[1-9][0-9]{0,4}[dhwy]', values['RETENTION']): fail('数据保留时间请使用 15d、4w 等格式')

def app_apply(values):
    args = set_flag(launch_args(), '--web.listen-address', values['BIND_ADDRESS'] + ':' + str(values['WEB_PORT']))
    write_launch(set_flag(args, '--storage.tsdb.retention.time', values['RETENTION']))

def app_health(values):
    http_ready(values, '/-/ready')
