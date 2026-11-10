import copy, json, os, re, sys
from pathlib import Path

program, location, app, mode, state = sys.argv[1:]
location = str(Path(location).resolve())
sys.argv = ['fixture', location, 'configure']
os.environ.update(OPS_SOFTWARE_ID='11111111-1111-4111-8111-111111111111', OPS_JOB_ID='22222222-2222-4222-8222-222222222222', OPS_PREVIOUS_STATE=state, OPS_SOFTWARE_LOCATOR='a'*64)
namespace = {'__name__': 'configuration_fixture'}
exec(Path(program).read_text(encoding='utf8').split('\ntry:\n    main()')[0], namespace)
root = Path(location)
for name in ['bin', 'config', 'data/gitlab-config', 'units']:
    (root/name).mkdir(parents=True, exist_ok=True)
namespace['unit_path'] = root/'units/service.conf'
if app == 'gitlab': namespace['gitlab_config'] = root/'data/gitlab-config/gitlab.rb'
sid, unit = namespace['sid'], namespace['unit']
unit_path = namespace['unit_path']
unit_path.write_text('Description=Ops '+app+' '+sid+'\nMemoryMax=infinity\n', encoding='utf8')
(root/'config/ops-owner').write_text(app+':'+sid, encoding='utf8')
(root/'data/untouched-database').write_bytes(b'persisted-data')
(root/'config/untouched-secret').write_text('private-test-only-secret', encoding='utf8')
command = []
current_state = [state]
def run(args, timeout=120):
    command.append(args)
    if args[0] == 'systemctl':
        if args[1] == 'show':
            if '--property=ActiveState' in args: return 'active' if current_state[0] == 'RUNNING' else 'inactive'
            path = namespace['gitlab_memory_path']() if app == 'gitlab' and namespace['gitlab_memory_path']().exists() else unit_path
            value = re.search(r'MemoryMax=(.*)', path.read_text()).group(1)
            return value if value == 'infinity' else str(int(value[:-1])*1048576)
        if args[1] in ['stop','start']: current_state[0] = 'STOPPED' if args[1] == 'stop' else 'RUNNING'
    if args[0].endswith('gitlab-ctl') and args[1] == 'reconfigure':
        assert 'initial_root_password' not in namespace['read'](namespace['gitlab_config'])
    return ''
namespace['run'] = run
namespace['http_ready'] = lambda *_: None
if os.name == 'nt': namespace['sync_directory'] = lambda *_: None
if not hasattr(os, 'fchown'): os.fchown = lambda *_: None
if not hasattr(os, 'chown'): os.chown = lambda *_: None
if not hasattr(os, 'fchmod'): os.fchmod = lambda *_: None

if app == 'jenkins':
    (root/'bin/run').write_text("#!/bin/bash\nexport JENKINS_HOME='"+str(root/'data')+"'\nexec '/usr/bin/java' -Xmx512m -jar '/runtime/jenkins.war' --httpListenAddress='127.0.0.1' --httpPort='8080' --webroot='/data/war'\n", encoding='utf8')
elif app == 'prometheus':
    (root/'bin/run').write_text("#!/bin/bash\nexec '/runtime/prometheus' --config.file='/config/prometheus.yml' --storage.tsdb.path='/data' --storage.tsdb.retention.time='15d' --web.listen-address='127.0.0.1:9090' --web.enable-lifecycle\n", encoding='utf8')
elif app == 'postgresql':
    (root/'config/postgresql.conf').write_text("data_directory = '/data'\nlisten_addresses = '127.0.0.1'\nport = 5432\nshared_buffers = '128MB'\npassword_encryption = 'scram-sha-256'\n", encoding='utf8')
elif app == 'nacos':
    (root/'config/application.properties').write_text('nacos.server.main.port=8848\nnacos.console.port=8080\nserver.address=127.0.0.1\nnacos.inetutils.ip-address=127.0.0.1\nnacos.plugin.auth.nacos.token.secret.key=private-test-only-secret\n', encoding='utf8')
    (root/'bin/run').write_text("#!/bin/bash\nexec '/usr/bin/java' -Xmx512m -Dnacos.remote.grpc.listen.ip='127.0.0.1' -jar '/runtime/nacos.jar' --extra-preserved\n", encoding='utf8')
elif app == 'gitlab':
    namespace['gitlab_config'].write_text("external_url 'http://git.example.test:80'\nnginx['listen_addresses'] = ['127.0.0.1']\nnginx['listen_port'] = 80\ngitlab_rails['gitlab_shell_ssh_port'] = 22\ngitlab_rails['initial_root_password'] = File.read('/etc/gitlab/ops-initial-password')\ncustom_setting['secret'] = 'private-test-only-secret'\n", encoding='utf8')

original_files = {str(path):path.read_bytes() for path in namespace['runtime_files']() if path.exists()}
original = None
if mode == 'DOCKER':
    keys = namespace['port_keys']
    ports = {str(port)+'/tcp':[{'HostIp':'127.0.0.1', 'HostPort':str(port if key != 'SSH_PORT' else 2222)}] for key,port in keys.items()}
    ports['4321/tcp'] = [{'HostIp':'192.0.2.10','HostPort':'14321'}]
    original = {'Id':'a'*64, 'Name':'/sfops-'+sid, 'Image':'sha256:'+'f'*64, 'Config':{'Image':'image:pinned','Labels':{'streamfusion.ops.software-id':sid,'custom':'keep'},'Env':['SECRET=private-test-only-secret','JVM_XMX=512m'], 'Cmd':['--storage.tsdb.retention.time=15d','--custom-argument=preserved'], 'Entrypoint':['/original-entry'], 'WorkingDir':'/original'}, 'HostConfig':{'Memory':0,'Binds':[str(root/'data')+':/data'],'Mounts':None,'PortBindings':ports,'NetworkMode':'bridge','RestartPolicy':{'Name':'unless-stopped'},'SecurityOpt':['no-new-privileges']}, 'NetworkSettings':{'Networks':{'bridge':{'IPAMConfig':None,'Aliases':['custom-alias']}}}, 'State':{'Running':state=='RUNNING','Paused':False,'Restarting':False}, 'Mounts':[{'Type':'bind','Source':str(root/'data/gitlab-config'),'Destination':'/etc/gitlab','RW':True}] if app=='gitlab' else []}
    candidate = [copy.deepcopy(original)]
    namespace['docker_inspect'] = lambda locator: copy.deepcopy(candidate[0] if locator.startswith('sfops-') else original)
    def recreate(old, job, ports, memory, config):
        plan = namespace['docker_plan'](old, ports, memory, config)
        assert plan['Env'] == old['Config']['Env'], 'port-only changes must preserve all secret environment entries'
        assert plan['HostConfig']['Binds'] == old['HostConfig']['Binds']
        assert plan['Labels'] == old['Config']['Labels']
        assert plan['HostConfig']['PortBindings']['4321/tcp'] == ports['4321/tcp']
        candidate[0] = copy.deepcopy(old)
        candidate[0].update(Id='b'*64, Config={key:value for key,value in plan.items() if key not in ['HostConfig','NetworkingConfig']}, HostConfig=plan['HostConfig'])
        candidate[0]['State']['Running'] = False
        return 'b'*64
    namespace['docker_recreate'] = recreate
    namespace['docker_restore'] = lambda old: candidate.__setitem__(0, copy.deepcopy(old))
    def api(method, path, body=None):
        assert path.endswith('/start') or path.endswith('/stop?t=60')
        candidate[0]['State']['Running'] = path.endswith('/start')
    namespace['_docker_api'] = api
    namespace['socket'].create_connection = lambda *args, **kwargs: open(os.devnull)

before = namespace['runtime_actual']()
assert set(before) == {'BIND_ADDRESS','WEB_PORT','MEMORY_MIB'} | ({'RETENTION'} if app=='prometheus' else {'EXTERNAL_URL','SSH_PORT'} if app=='gitlab' else {'CONSOLE_PORT','GRPC_PORT','CLUSTER_GRPC_PORT'} if app=='nacos' else set())
assert before['MEMORY_MIB'] == 0
assert 'private-test-only-secret' not in json.dumps(before)
if app == 'jenkins' and mode == 'NATIVE':
    original_launch = (root/'bin/run').read_bytes()
    (root/'bin/run').write_bytes(original_launch.replace(b' -jar ', b' $JAVA_OPTS -jar '))
    try:
        namespace['launch_args']()
        raise AssertionError('shell variable would have lost its expansion')
    except RuntimeError:
        pass
    (root/'bin/run').write_bytes(original_launch)
if app == 'gitlab':
    original_config = namespace['gitlab_config'].read_bytes()
    namespace['gitlab_config'].write_bytes(original_config.replace(b"nginx['listen_port'] = 80", b"nginx['listen_port'] = 80; custom_setting['secret'] = 'preserve'"))
    try:
        namespace['app_apply'](dict(before, MEMORY_MIB=0))
        raise AssertionError('compound Ruby assignment accepted')
    except ValueError:
        pass
    assert b"custom_setting['secret'] = 'preserve'" in namespace['gitlab_config'].read_bytes()
    namespace['gitlab_config'].write_bytes(original_config)
os.environ['OPS_PARAM_WEB_PORT'] = '0'
try:
    namespace['requested'](before)
    raise AssertionError('invalid port accepted')
except RuntimeError:
    pass
assert all(Path(path).read_bytes() == original for path,original in original_files.items())
os.environ.update(OPS_PARAM_BIND_ADDRESS='0.0.0.0', OPS_PARAM_WEB_PORT=str(before['WEB_PORT']+10000), OPS_PARAM_MEMORY_MIB='0')
if app == 'nacos':
    os.environ.update(OPS_PARAM_CONSOLE_PORT='18080', OPS_PARAM_GRPC_PORT=str(before['WEB_PORT']+11000), OPS_PARAM_CLUSTER_GRPC_PORT=str(before['WEB_PORT']+11001))
if app == 'gitlab': os.environ.update(OPS_PARAM_EXTERNAL_URL='http://git.example.test:'+str(before['WEB_PORT']+10000), OPS_PARAM_SSH_PORT=str(before['SSH_PORT']+10000))
if app == 'prometheus': os.environ['OPS_PARAM_RETENTION'] = '30d'
desired = namespace['requested'](before)
namespace['main']()
namespace['action'] = 'verify'
after = namespace['runtime_actual']()
assert after == desired, (before, after, desired)
namespace['main']()
assert (root/'data/untouched-database').read_bytes() == b'persisted-data'
assert (root/'config/untouched-secret').read_text() == 'private-test-only-secret'
if app == 'nacos' and mode == 'NATIVE': assert 'private-test-only-secret' in (root/'config/application.properties').read_text()
assert all(not any(word in str(args) for word in ['initdb','createdb','ALTER USER','CREATE DATABASE']) for args in command)
namespace['action'] = 'rollback'
namespace['main']()
assert all(Path(path).read_bytes() == original for path,original in original_files.items())
assert current_state[0] == state
if mode == 'DOCKER': assert candidate[0] == original

# A failed complete-backup phase never starts a mutation or invents UNKNOWN.
namespace['backup'] = root/'config/management-backups/pre-mutation-failure'
def fail_snapshot(): raise RuntimeError('simulated inspect or snapshot failure')
namespace['runtime_snapshot'] = fail_snapshot
namespace['action'] = 'configure'
commands_before = len(command)
try:
    namespace['main']()
    raise AssertionError('snapshot failure accepted')
except RuntimeError as error:
    assert str(error) == 'simulated inspect or snapshot failure'
assert not (namespace['backup']/'mutation-ready.json').exists()
namespace['action'] = 'rollback'
namespace['main']()
assert not any(args[:2] == ['systemctl','stop'] for args in command[commands_before:])
assert all(Path(path).read_bytes() == original for path,original in original_files.items())
assert current_state[0] == state
print(app, mode, state, 'PASS')
