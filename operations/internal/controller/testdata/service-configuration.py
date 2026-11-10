import contextlib
import copy
import hashlib
import io
import json
import os
import pathlib
import tempfile
import sys

sources=pathlib.Path(sys.argv[1]).resolve()
sid='11111111-1111-4111-8111-111111111111'
job='22222222-2222-4222-8222-222222222222'
if not hasattr(os,'O_NOFOLLOW'):
    os.O_NOFOLLOW=0
if not hasattr(os,'chown'):
    os.chown=lambda *args:None


def source(name):
    return (sources/(name+'.py')).read_text(encoding='utf-8')


def digest(root):
    return {str(path.relative_to(root)):hashlib.sha256(path.read_bytes()).hexdigest() for path in root.rglob('*') if path.is_file()}


def scenario(app,mode='NATIVE',official=False,stopped=False,failure=False):
    with tempfile.TemporaryDirectory(prefix='ops-core-config-') as directory:
        root=pathlib.Path(directory)
        for name in ('config','work','data','bin'):
            (root/name).mkdir()
        os.environ.update(OPS_INSTALL_ROOT=str(root),OPS_SOFTWARE_ID=sid,OPS_JOB_ID=job,OPS_RUNTIME_KIND=mode,OPS_SOFTWARE_LOCATOR='a'*64 if mode=='DOCKER' else app+'.service',OPS_PREVIOUS_STATE='STOPPED' if stopped else 'RUNNING',OPS_PARAM_ADMIN_USER='root')
        os.environ.pop('OPS_SECRET_ADMIN_PASSWORD_FILE',None)
        namespace=dict(app=app,official=official,action='read-config')
        exec(compile(source('docker')+'\n'+source('common')+'\n'+source(app),app,'exec'),namespace)
        namespace['memory_file']=lambda:root/'config/memory.conf'
        if os.name=='nt':
            namespace['flush_backup']=lambda:None
        if app=='nginx':
            namespace['main']=root/'config/nginx.conf'
            namespace['site']=root/'config/default.conf' if official else namespace['main']
            default='server { listen 127.0.0.1:80; listen [::]:80; root /var/www; location = /__sfops_health { return 200 "'+sid+'"; } }'
            if official:
                namespace['main'].write_text('events { worker_connections 512; } http { include /etc/nginx/sites-enabled/*; }',encoding='utf-8')
                namespace['site'].write_text(default.replace('127.0.0.1:80','80')+'\n# custom note',encoding='utf-8')
            else:
                namespace['main'].write_text('events { worker_connections 512; } http { '+default+' server { listen 9001; server_name other; proxy_pass http://127.0.0.1:8081; } }',encoding='utf-8')
                namespace['saved_base'].parent.mkdir()
                namespace['saved_base'].write_text('events { worker_connections 512; } http { '+default+' }',encoding='utf-8')
        elif app=='redis':
            namespace['config']=root/'config/redis.conf'
            namespace['config'].write_text('bind 127.0.0.1\nport 6379\nmaxmemory 256mb\nmaxclients 10000\nappendonly yes\nsave 900 1\nrequirepass do-not-return-this-secret\naclfile /data/users.acl\n',encoding='utf-8')
        else:
            namespace['config']=root/'config/service-settings.cnf' if mode=='DOCKER' else root/'config/my.cnf'
            namespace['auto']=root/'data/mysqld-auto.cnf'
            namespace['config'].write_text('[mysqld]\nport=3306\nbind-address=127.0.0.1\nmax_connections=151\ninnodb_buffer_pool_size=128M\nlog-bin=mysql-bin\n[client]\npassword=do-not-return-this-secret\n',encoding='utf-8')
            namespace['auto'].write_text(json.dumps({'Version':2,'mysql_server':{'max_connections':{'Value':'151'},'long_query_time':{'Value':'5'}}}),encoding='utf-8')
            (root/'config/health.cnf').write_text('[client]\nuser=root\npassword=do-not-return-this-secret\n',encoding='utf-8')
            (root/'data/database.frm').write_text('preserved database and account data',encoding='utf-8')
            if mode=='NATIVE' and not official:
                secret=root/'work/admin-password'
                secret.write_text('do-not-return-native-password',encoding='utf-8')
                os.environ['OPS_SECRET_ADMIN_PASSWORD_FILE']=str(secret)
        original={'Id':'a'*64,'Name':'/sfops-service-'+sid,'Image':'sha256:'+'1'*64,'State':{'Running':not stopped},'Config':{'Env':['SECRET=keep-me'],'Labels':{'com.streamfusion.ops.software':sid,'com.streamfusion.ops.job':'old-job'},'Cmd':['original-command'],'Entrypoint':['original-entry'],'User':'999'},'HostConfig':{'Memory':0,'MemorySwap':0,'PortBindings':{str(namespace['internal_port'])+'/tcp':[{'HostIp':'127.0.0.1','HostPort':str(namespace['internal_port'])}],'9000/tcp':[{'HostIp':'127.0.0.1','HostPort':'19000'}]},'Binds':[str(root/'data')+':/data'],'Mounts':None,'RestartPolicy':{'Name':'unless-stopped'},'SecurityOpt':['no-new-privileges'],'LogConfig':{'Type':'json-file','Config':{'max-size':'10m'}}},'Mounts':[{'Type':'volume','Name':'original-anonymous','Destination':'/extra','RW':True,'Source':'/var/lib/docker/volumes/original-anonymous/_data'}],'NetworkSettings':{'Networks':{'default':{'Aliases':['user-alias','a'*12]}}}}
        containers={original['Id']:copy.deepcopy(original)}
        calls=[]
        failed=False
        original_write=namespace['write']
        def guarded_write(path,text):
            if app=='mysql' and pathlib.Path(path)==namespace['auto'] and namespace['action']=='configure' and not stopped:
                assert any(method=='stop' or method=='POST' and '/stop?' in value for method,value in calls), 'MySQL persisted file was written before stopping mysqld'
            return original_write(path,text)
        namespace['write']=guarded_write
        def api(method,path,body=None,expected=None):
            calls.append((method,path))
            if method=='POST' and path.startswith('/containers/create?'):
                new=copy.deepcopy(original)
                new.update(Id='b'*64,State={'Running':False},Config={key:value for key,value in body.items() if key not in ('HostConfig','NetworkingConfig')},HostConfig=copy.deepcopy(body['HostConfig']))
                containers[new['Id']]=new
                return {'Id':new['Id']}
            parts=path.split('/')
            identity=parts[2].split('?')[0]
            found=next((item for item in containers.values() if item['Id']==identity or item['Name'].lstrip('/')==identity),None)
            if not found:
                raise RuntimeError('容器不存在')
            if method=='GET':
                return copy.deepcopy(found)
            if '/rename?' in path:
                found['Name']='/'+path.split('name=',1)[1]
            elif '/stop?' in path:
                found['State']['Running']=False
            elif path.endswith('/start'):
                found['State']['Running']=True
            elif method=='DELETE':
                del containers[found['Id']]
            return None
        namespace['_docker_api']=api
        namespace['container_name']='sfops-service-'+sid
        def command(args,timeout=60,input=None):
            nonlocal failed
            calls.append(('command',args[0]))
            if args[:2]==['systemctl','stop']:
                calls.append(('stop',args[2]))
            if failure and not failed and (args[:2]==['systemctl','restart'] or '-t' in args or '--validate-config' in args):
                failed=True
                raise RuntimeError('模拟配置保存后校验失败')
            if args[:2]==['systemctl','show']:
                if any('ActiveState' in value for value in args):
                    return 'inactive' if stopped else 'active'
                memory=root/'config/memory.conf'
                if memory.exists():
                    value=memory.read_text().split('MemoryMax=',1)[1].strip()
                    return str(int(value[:-1])*1048576) if value.endswith('M') else value
                return 'infinity'
            if any(value.startswith('--execute=') for value in args):
                if app=='mysql' and mode=='NATIVE' and not official:
                    assert '--socket='+str(root/'data/mysql.sock') in args, 'native MySQL lost its instance socket'
                    assert '--defaults-extra-file=/dev/stdin' in args
                    assert input and 'do-not-return-native-password' in input, 'password options did not use stdin'
                    assert all('do-not-return-native-password' not in value for value in args), 'password leaked in argv'
                query=next(value for value in args if value.startswith('--execute='))
                if 'SELECT 1' in query:
                    return '1'
                values=namespace['mysql_values'](namespace['read'](namespace['config']))
                return '\t'.join([values.get('port','3306'),values.get('bind-address','0.0.0.0'),values.get('max-connections','151'),str(int(values.get('innodb-buffer-pool-size','128M')[:-1])*1048576)])
            return ''
        namespace['run']=command
        # HTTP/protocol availability is exercised by dedicated adapters, with
        # no real network/service process created in this filesystem scenario.
        namespace['check']=lambda value:None
        before=namespace['actual']()
        before_files=digest(root)
        namespace['action']='rollback'
        with contextlib.redirect_stdout(io.StringIO()):
            namespace['execute']()
        assert digest(root)==before_files, 'pre-mutation rollback changed files'
        namespace['action']='read-config'
        captured=io.StringIO()
        with contextlib.redirect_stdout(captured):
            namespace['execute']()
        values={row['key']:row['value'] for row in json.loads(captured.getvalue())['rows']}
        assert 'do-not-return' not in captured.getvalue() and values['MEMORY_MIB']=='0'
        assert digest(root)==before_files, 'read-config wrote files'
        if app=='mysql':
            assert values['ADMIN_USER']=='root'
        desired=dict(BIND_ADDRESS='0.0.0.0',CONFIG_PORT=namespace['internal_port']+10,MEMORY_MIB=0)
        if app=='nginx':
            assert 'listen [::]:80;' in namespace['read'](namespace['site']), 'IPv6 listener was changed'
            desired.update(MAX_UPLOAD_MIB=64,MAX_CONNECTIONS=2048)
        elif app=='redis':
            desired.update(MAX_MEMORY_MIB=384,MAX_CLIENTS=20000)
        else:
            desired.update(MAX_CONNECTIONS=300,BUFFER_POOL_MIB=256)
        os.environ.update({'OPS_PARAM_'+key:str(value) for key,value in desired.items()})
        namespace['action']='configure'
        if failure:
            try:
                with contextlib.redirect_stdout(io.StringIO()):
                    namespace['execute']()
                raise AssertionError('expected application failure')
            except RuntimeError:
                pass
            namespace['action']='rollback'
            with contextlib.redirect_stdout(io.StringIO()):
                namespace['execute']()
            assert namespace['actual']()==before
            for path,hash_value in before_files.items():
                assert digest(root)[path]==hash_value,'failed rollback changed '+path
            print(app,mode,'failed apply recovered OK')
            return
        else:
            with contextlib.redirect_stdout(io.StringIO()):
                namespace['execute']()
        namespace['action']='verify'
        with contextlib.redirect_stdout(io.StringIO()):
            namespace['execute']()
        after=namespace['actual']()
        assert all(str(after[key])==str(value) for key,value in desired.items())
        if app=='nginx':
            assert 'listen [::]:80;' in namespace['read'](namespace['site']), 'IPv6 listener was changed'
            assert ('server_name other' in namespace['read'](namespace['main'])) != official
            if not official and mode!='DOCKER':
                assert ':90' in namespace['read'](namespace['saved_base'])
        elif app=='redis':
            text=namespace['read'](namespace['config'])
            assert 'requirepass do-not-return-this-secret' in text and 'appendonly yes' in text and 'save 900 1' in text
        else:
            assert 'log-bin=mysql-bin' in namespace['read'](namespace['config'])
            assert 'long_query_time' in namespace['read'](namespace['auto'])
            assert (root/'data/database.frm').read_text()=='preserved database and account data'
            assert 'password=do-not-return' in namespace['read'](root/'config/health.cnf')
        if mode=='DOCKER':
            candidate=namespace['docker_inspect'](namespace['container_name'])
            assert candidate['Config']['Env']==original['Config']['Env']
            assert candidate['HostConfig']['PortBindings']['9000/tcp']==original['HostConfig']['PortBindings']['9000/tcp']
            assert candidate['Config']['Cmd']==original['Config']['Cmd']
            assert candidate['HostConfig']['SecurityOpt']==original['HostConfig']['SecurityOpt']
            assert candidate['State']['Running']==(not stopped)
        namespace['action']='rollback'
        with contextlib.redirect_stdout(io.StringIO()):
            namespace['execute']()
        assert namespace['actual']()==before
        for path,hash_value in before_files.items():
            assert digest(root)[path]==hash_value, 'rollback changed '+path
        if mode=='DOCKER':
            assert len(containers)==1 and containers[original['Id']]['Name']==original['Name']
            assert all('?v=false' in path for method,path in calls if method=='DELETE')
        print(app,mode,'official='+str(official),'stopped='+str(stopped),'read/configure/verify/rollback/preserve OK')


def docker_api_versions():
    for name,maximum,minimum,prefix in [('20.10','1.41','1.12','/v1.41'),('29.0','1.52','1.44','/v1.44'),('29.8','1.54','1.40','/v1.44'),('too-old','1.40','1.12',None),('too-new-minimum','1.55','1.45',None),('malformed','1.44/secret','1.40',None),('leading-zero','1.044','1.40',None),('missing-version',None,'1.40',None)]:
        namespace={}
        exec(compile(source('docker'),'docker','exec'),namespace)
        requests=[]
        class WireSocket:
            def __init__(self):
                self.data=bytearray()
            def sendall(self,data):
                self.data.extend(data)
            def close(self):
                pass
            def makefile(self,*args):
                method,path,_=bytes(self.data).split(b'\r\n',1)[0].decode().split()
                requests.append((method,path))
                body=json.dumps({'ApiVersion':maximum,'MinAPIVersion':minimum} if path=='/version' else {'ok':True}).encode()
                return io.BytesIO(b'HTTP/1.1 200 OK\r\nConnection: close\r\nContent-Length: '+str(len(body)).encode()+b'\r\n\r\n'+body)
        # Exercise the actual HTTPConnection request/response parser; only the
        # Unix socket is replaced, so no daemon or real network is contacted.
        namespace['_DockerConnection'].connect=lambda connection:setattr(connection,'sock',WireSocket())
        if prefix:
            assert namespace['_docker_api']('GET','/containers/example/json')=={'ok':True}
            assert namespace['_docker_api']('POST','/containers/example/stop?t=60')=={'ok':True}
            assert requests==[('GET','/version'),('GET',prefix+'/containers/example/json'),('POST',prefix+'/containers/example/stop?t=60')]
        else:
            try:
                namespace['_docker_api']('POST','/containers/example/stop?t=60')
                raise AssertionError('incompatible Docker API was accepted')
            except RuntimeError as error:
                assert 'secret' not in str(error)
            assert requests==[('GET','/version')], 'mutation occurred before API negotiation'
        print('Docker API',name,'negotiation/cache/HTTP paths OK')


def docker_guards():
    namespace={}
    exec(compile(source('docker'),'docker','exec'),namespace)
    original={'Id':'a'*64,'Name':'/sfops-service-'+sid,'Image':'sha256:pinned','State':{'Running':True},'Config':{'Env':['TOKEN=secret'],'Labels':{'com.streamfusion.ops.software':sid},'Cmd':['keep-cmd']},'HostConfig':{'Memory':512*1048576,'MemorySwap':1024*1048576,'Mounts':None,'NetworkMode':'default','SecurityOpt':['keep-security']},'Mounts':[{'Type':'volume','Name':'existing-volume','Destination':'/database','RW':True}],'NetworkSettings':{'Networks':{'net':{'Aliases':['named-alias','a'*12]}}}}
    previous=copy.deepcopy(original)
    plan=namespace['docker_plan'](original,memory_mib=0)
    assert original==previous and plan['HostConfig']['Memory']==0 and plan['HostConfig']['MemorySwap']==0
    assert plan['HostConfig']['Mounts'][0]['Source']=='existing-volume' and plan['HostConfig']['Mounts'][0]['VolumeOptions']['NoCopy']
    assert plan['Config'] if 'Config' in plan else plan['Env']==['TOKEN=secret']
    assert plan['NetworkingConfig']['EndpointsConfig']['net']['Aliases']==['named-alias']
    variants=[]
    value=copy.deepcopy(original); value['HostConfig']['AutoRemove']=True; variants.append(value)
    value=copy.deepcopy(original); value['HostConfig']['NetworkMode']='container:other'; variants.append(value)
    value=copy.deepcopy(original); value['NetworkSettings']['Networks']['net']['IPAMConfig']={'IPv4Address':'10.0.0.8'}; variants.append(value)
    value=copy.deepcopy(original); value['HostConfig']['MemoryReservation']=512*1048576; variants.append(value)
    for value in variants:
        try:
            namespace['docker_plan'](value,memory_mib=128)
            raise AssertionError('unsafe Docker plan accepted')
        except RuntimeError as error:
            assert 'secret' not in str(error)
    print('Docker complete settings/volume reuse/unlimited/unsafe-network guards OK')


docker_api_versions()
docker_guards()
for application in ('nginx','mysql','redis'):
    for runtime in ('NATIVE','DOCKER'):
        for stopped in (False,True):
            scenario(application,runtime,stopped=stopped)
    scenario(application,official=True)
    scenario(application,failure=True)
print('all core configuration scenarios passed')
