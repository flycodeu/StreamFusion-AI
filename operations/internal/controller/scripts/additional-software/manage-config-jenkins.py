def app_files():
    return [root/'bin/run']

def app_actual():
    args = launch_args()
    return dict(BIND_ADDRESS=flag(args, '--httpListenAddress'), WEB_PORT=int(flag(args, '--httpPort')))

def app_apply(values):
    args = set_flag(set_flag(launch_args(), '--httpListenAddress', values['BIND_ADDRESS']), '--httpPort', values['WEB_PORT'])
    if values['MEMORY_MIB']:
        args = ['-Xmx' + str(max(128, values['MEMORY_MIB']*3//4)) + 'm' if re.fullmatch(r'-Xmx[0-9]+[mMgG]', item) else item for item in args]
    write_launch(args)

def app_health(values):
    http_ready(values, '/login')
