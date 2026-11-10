def app_files():
    return [root/'config/application.properties', root/'bin/run']

def app_actual():
    text = read(root/'config/application.properties'); port = int(property_value(text, 'nacos.server.main.port'))
    return dict(BIND_ADDRESS=property_value(text, 'server.address'), WEB_PORT=port, CONSOLE_PORT=int(property_value(text, 'nacos.console.port')), GRPC_PORT=port+1000, CLUSTER_GRPC_PORT=port+1001)

def app_validate(values):
    if values['GRPC_PORT'] != values['WEB_PORT']+1000 or values['CLUSTER_GRPC_PORT'] != values['WEB_PORT']+1001:
        fail('Nacos gRPC 端口须分别为服务端口 +1000、+1001')
    if len({values[key] for key in ['WEB_PORT','CONSOLE_PORT','GRPC_PORT','CLUSTER_GRPC_PORT']}) != 4: fail('Nacos 各端口不能重复')

def app_apply(values):
    text = read(root/'config/application.properties')
    for key, value in [('nacos.server.main.port', values['WEB_PORT']), ('nacos.console.port', values['CONSOLE_PORT']), ('server.address', values['BIND_ADDRESS'])]: text = replace_property(text, key, value)
    text = re.sub(r'(?m)^nacos\.inetutils\.ip-address=.*\n?', '', text)
    if values['BIND_ADDRESS'] != '0.0.0.0': text = replace_property(text, 'nacos.inetutils.ip-address', values['BIND_ADDRESS'], False)
    write(root/'config/application.properties', text)
    args = set_flag(launch_args(), '-Dnacos.remote.grpc.listen.ip', values['BIND_ADDRESS'])
    if values['MEMORY_MIB']: args = ['-Xmx' + str(max(128, values['MEMORY_MIB']*3//4)) + 'm' if re.fullmatch(r'-Xmx[0-9]+[mMgG]', item) else item for item in args]
    write_launch(args)

def app_health(values):
    http_ready(values, '/nacos/v3/admin/core/state/readiness')
    http_ready(dict(values, WEB_PORT=values['CONSOLE_PORT']), '/v3/console/health/readiness')
