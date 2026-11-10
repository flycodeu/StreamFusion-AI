internal_port = 80
fields = dict(MAX_UPLOAD_MIB=(0,16384), MAX_CONNECTIONS=(16,65536))
main = pathlib.Path('/etc/nginx/nginx.conf') if official else root/'config/nginx.conf'
site = pathlib.Path('/etc/nginx/sites-available/default') if official else main
saved_base = root/'config/ops-sites/base.conf.saved'


def nginx_nodes(text):
    pattern = r'''"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|\#[^\n]*|[{};]|(?:\\.|[^\s{};\#"'])+'''
    tokens = [(item.group(),item.start(),item.end()) for item in re.finditer(pattern,text) if not item.group().startswith('#')]
    stack, head, nodes = [], [], []
    for value,start,end in tokens:
        if value == '{':
            if not head:
                fail('Nginx 配置格式无法识别')
            node = dict(head=head, children=[], start=head[0][1], close=0, end=0)
            (stack[-1]['children'] if stack else nodes).append(node)
            stack.append(node)
            head = []
        elif value == '}':
            if head or not stack:
                fail('Nginx 配置结构无法识别')
            stack[-1].update(close=start,end=end)
            stack.pop()
        elif value == ';':
            if not head:
                fail('Nginx 配置结构无法识别')
            (stack[-1]['children'] if stack else nodes).append(dict(head=head,start=head[0][1],end=end))
            head = []
        else:
            head.append((value,start,end))
    if stack or head:
        fail('Nginx 配置尚未完整，请先检查配置文件')
    return nodes


def block(nodes,name):
    found = [node for node in nodes if node['head'][0][0] == name and 'children' in node]
    if len(found) != 1:
        fail('无法确认唯一的 Nginx ' + name + ' 配置段')
    return found[0]


def nginx_context(text, webtext):
    nodes = nginx_nodes(text)
    http = block(nodes,'http')
    servers = [node for node in (nginx_nodes(webtext) if official else http['children']) if node['head'][0][0] == 'server' and 'children' in node]
    if not official:
        servers = [node for node in servers if '/__sfops_health' in webtext[node['start']:node['end']] and sid in webtext[node['start']:node['end']]]
    if len(servers) != 1:
        fail('无法确认原默认站点；其他站点配置保持不变')
    return block(nodes,'events'), http, servers[0]


def directive(node,name,default):
    found = [item for item in node['children'] if item['head'][0][0] == name]
    if len(found)>1:
        fail('Nginx 配置项重复：' + name)
    return found[0]['head'][1][0] if found and len(found[0]['head'])==2 else default


def listeners(server):
    found = [item for item in server['children'] if item['head'][0][0]=='listen']
    ipv4 = [item for item in found if len(item['head'])>=2 and not item['head'][1][0].startswith('[')]
    if len(ipv4)!=1 or any(token[0] in ('ssl','quic') for token in ipv4[0]['head']):
        fail('默认站点存在多个入口或 TLS，请在站点管理中处理')
    value = ipv4[0]['head'][1][0]
    address,port = ('0.0.0.0',value) if value.isdecimal() else value.rsplit(':',1)
    ipaddress.IPv4Address(address)
    return found,address,int(port)


def actual():
    text,webtext = read(main),read(site)
    events,http,server = nginx_context(text,webtext)
    _,address,port = listeners(server)
    upload = directive(http,'client_max_body_size','1m').lower()
    size = re.fullmatch(r'(\d+)([kmg]?)',upload)
    if not size:
        fail('上传大小配置格式无法识别')
    upload = int(size[1]) * {'':1,'k':1024,'m':1048576,'g':1073741824}[size[2]] // 1048576
    values = dict(BIND_ADDRESS=address,CONFIG_PORT=port,MEMORY_MIB=native_memory() if mode!='DOCKER' else 128,MAX_UPLOAD_MIB=upload,MAX_CONNECTIONS=int(directive(events,'worker_connections','512')))
    if mode=='DOCKER':
        values.update(network_value())
    return values


def managed_files():
    files = [main,site]
    if not official and saved_base.exists():
        files.append(saved_base)
    return list(dict.fromkeys(files+([] if mode=='DOCKER' else [memory_file()])))


def validate(value):
    nginx_context(read(main),read(site))
    if mode=='DOCKER':
        docker_plan(docker_inspect(locator), memory_mib=value['MEMORY_MIB'])


def edit_directive(node,name,value,changes):
    found = [item for item in node['children'] if item['head'][0][0]==name]
    if len(found)>1:
        fail('Nginx 配置项重复：'+name)
    if found:
        changes.append((found[0]['start'],found[0]['end'],name+' '+value+';'))
    else:
        changes.append((node['close'],node['close'],'\n  '+name+' '+value+';\n'))


def patched(text,changes):
    for start,end,value in sorted(changes,reverse=True):
        text = text[:start]+value+text[end:]
    return text


def updated_nginx(text,webtext,value,before,same_file):
    events,http,server = nginx_context(text,webtext)
    edits,webedits = [],[]
    if value['MAX_CONNECTIONS']!=before['MAX_CONNECTIONS']:
        edit_directive(events,'worker_connections',str(value['MAX_CONNECTIONS']),edits)
    if value['MAX_UPLOAD_MIB']!=before['MAX_UPLOAD_MIB']:
        edit_directive(http,'client_max_body_size',str(value['MAX_UPLOAD_MIB'])+'m',edits)
    network_changed=any(value[key]!=before[key] for key in ('BIND_ADDRESS','CONFIG_PORT'))
    if mode!='DOCKER' and network_changed:
        found,_,_ = listeners(server)
        for item in found:
            if item['head'][1][0].startswith('['):
                continue
            args = [token[0] for token in item['head'][2:]]
            endpoint = value['BIND_ADDRESS']+':'+str(value['CONFIG_PORT'])
            webedits.append((item['start'],item['end'],'listen '+endpoint+(' '+' '.join(args) if args else '')+';'))
    return patched(text,edits+webedits if same_file else edits),patched(webtext,webedits)


def apply(value):
    before=actual()
    content,web=updated_nginx(read(main),read(site),value,before,main==site)
    write(main,content)
    if main!=site:
        write(site,web)
    elif saved_base.exists():
        base=read(saved_base)
        content,_=updated_nginx(base,base,value,before,True)
        write(saved_base,content)
    if mode=='DOCKER':
        if previous_state=='RUNNING':
            run(['docker','exec',locator,'nginx','-t'])
    else:
        binary = '/usr/sbin/nginx' if official else str(root/'bin/nginx')
        run([binary,'-t'] if official else [binary,'-p',str(root)+'/', '-c',str(main),'-t'])


def check(value):
    if previous_state=='STOPPED':
        return
    import urllib.request
    host = '127.0.0.1' if value['BIND_ADDRESS']=='0.0.0.0' else value['BIND_ADDRESS']
    url = 'http://'+host+':'+str(value['CONFIG_PORT'])+('/' if official else '/__sfops_health')
    deadline = time.monotonic()+40
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    while time.monotonic()<deadline:
        try:
            with opener.open(url,timeout=2) as response:
                content = response.read(4097)
                if response.status==200 and (official or sid.encode() in content):
                    return
        except Exception:
            pass
        time.sleep(1)
    fail('Nginx 配置已保存，但网页验证未通过')
