import contextlib, copy, hashlib, io, json, os, shlex, subprocess, sys, tempfile, uuid
from pathlib import Path

report = {}
for application, mode, program in payload['programs']:
    with tempfile.TemporaryDirectory(prefix='ops-config20-') as temporary:
        base = Path(temporary).resolve()
        root, source = base/'root', base/'materials'
        for directory in [root/'config',root/'bin',root/'data',root/'logs',source]:
            directory.mkdir(parents=True, exist_ok=True)
        sid = str(uuid.uuid4())
        service = base/'fixture.service'
        (root/'config/ops-owner').write_text(application+':'+sid)
        (source/'image.png').write_bytes(b'image-original')
        (root/'data/sample.txt').write_bytes(b'original-dataset')
        if os.name != 'nt':
            os.link(root/'data/sample.txt',root/'data/hardlink.txt')
        if application == 'ytlabel':
            settings={'host':'127.0.0.1','port':9924,'maxUploadMiB':10}
            if os.name=='nt': settings['sourceDirectory']=str(source)
            (root/'config/ytlabel.json').write_text(json.dumps(settings))
            (root/'bin/ytlabel-launch.py').write_text("os.chdir(root / 'data')\n")
            service.write_text('Description=Ops ytlabel '+sid+'\nMemoryMax=2048M\nWorkingDirectory='+str(root/'data')+'\nReadWritePaths='+str(root/'data')+' '+str(root/'logs')+'\nBindReadOnlyPaths='+source.as_posix()+':'+root.as_posix()+'/data/uploads/samples\nExecStartPre=/usr/bin/test -r '+root.as_posix()+'/data/uploads/samples\n')
        else:
            env = {'LABEL_STUDIO_BASE_DATA_DIR':str(root/'data'),'DATA_UPLOAD_MAX_MEMORY_SIZE':str(10*1048576),'LABEL_STUDIO_HOST':'https://labels.example.test/','LABEL_STUDIO_LOCAL_FILES_SERVING_ENABLED':'true','LABEL_STUDIO_LOCAL_FILES_DOCUMENT_ROOT':str(source)}
            (root/'config/runtime.env').write_text(''.join('export '+k+'='+shlex.quote(v)+'\n' for k,v in env.items()))
            (root/'bin/run').write_text('exec '+shlex.join([str(root/'runtime/venv/bin/label-studio'),'start','--internal-host','127.0.0.1','--port','8080'])+'\n')
            service.write_text('Description=Ops Label Studio '+sid+'\nUser=fixture\nMemoryMax=8192M\nReadOnlyPaths='+str(source)+'\n[Install]\n')
        containers, sequence = {}, [0]
        name = 'ops-labelstudio-'+sid
        old = 'a'*64
        if mode == 'DOCKER':
            containers[old] = {'Id':old,'Name':'/'+name,'Image':'sha256:'+'b'*64,'State':{'Running':False},'Config':{'Labels':{'streamfusion.ops.software-id':sid,'retained':'yes'},'Env':[k+'='+v for k,v in env.items()]+['PRIVATE_TOKEN=must-not-be-returned']},'HostConfig':{'Memory':8192*1048576,'PortBindings':{'8080/tcp':[{'HostIp':'127.0.0.1','HostPort':'8080'}]}},'Mounts':[{'Source':str(root/'data'),'Destination':'/label-studio/data','Type':'bind','RW':True},{'Source':str(source),'Destination':'/datasets','Type':'bind','RW':False}]}
        bound, calls = [old], []
        def locate(value):
            return containers[value] if value in containers else next(v for v in containers.values() if v['Name'].lstrip('/')==value)
        def fake_run(args, display=None):
            calls.append(args[:])
            if args[0] in ('systemctl','runuser'):
                return ''
            if args[0] != 'docker':
                return original_run(args, display)
            command = args[1]
            if command == 'inspect':
                return json.dumps(locate(args[-1]))
            if command == 'stop':
                locate(args[-1])['State']['Running'] = False
            elif command == 'start':
                locate(args[-1])['State']['Running'] = True
            elif command == 'rename':
                locate(args[2])['Name'] = '/'+args[3]
            elif command == 'rm':
                del containers[locate(args[-1])['Id']]
            elif command == 'create':
                sequence[0] += 1
                value = {'Id':hashlib.sha256(str(sequence[0]).encode()).hexdigest(),'Name':'/'+name,'Image':containers[bound[0]]['Image'],'State':{'Running':False},'Config':{'Labels':{},'Env':[]},'HostConfig':{'Memory':0,'PortBindings':{}},'Mounts':[]}
                i = 2
                while i < len(args):
                    flag = args[i]
                    if flag in ('--label','-e','-p','-v','--name','--memory','--restart','--entrypoint'):
                        item = args[i+1]
                        if flag == '--label':
                            k,v = item.split('=',1); value['Config']['Labels'][k] = v
                        elif flag == '-e': value['Config']['Env'].append(item)
                        elif flag == '--memory': value['HostConfig']['Memory'] = int(item[:-1])*1048576
                        elif flag == '-p':
                            address,port,inner = item.split(':'); value['HostConfig']['PortBindings'][inner if '/' in inner else inner+'/tcp']=[{'HostIp':address,'HostPort':port}]
                        elif flag == '-v':
                            host,inner=item.rsplit(':/',1); value['Mounts'].append({'Source':host,'Destination':'/'+inner.split(':')[0],'Type':'bind','RW':not inner.endswith(':ro')})
                        i += 2
                    else:
                        i += 1
                containers[value['Id']] = value
            elif command != 'exec':
                raise AssertionError('unexpected Docker command')
            return ''
        original_run = None
        def execute(action, job, values=None, data_target=None, broken_copy=False):
            global original_run
            sys.argv = ['fixture',str(root),application,mode,action]
            os.environ.update(OPS_SOFTWARE_ID=sid,OPS_JOB_ID=job,OPS_PREVIOUS_STATE='STOPPED',OPS_PREVIOUS_LOCATOR=bound[0])
            for key in list(os.environ):
                if key.startswith('OPS_PARAM_'): del os.environ[key]
            os.environ.update({'OPS_PARAM_'+k:str(v) for k,v in (values or {}).items()})
            if data_target: os.environ['OPS_PARAM_DATA_DIRECTORY']=str(data_target)
            namespace = {}
            exec(compile(program,'published-prelude','exec'),namespace)
            namespace['unit_path'] = service
            original_run = namespace['run']
            namespace['run'] = fake_run
            if os.name == 'nt':
                os.chown = lambda *args: None
                os.fchown = lambda *args: None
                os.fchmod = lambda *args: None
                type(base).is_mount = lambda self: False
                def windows_safe(path, exists=True):
                    value=Path(path)
                    if value.resolve()!=value or value.is_symlink() or exists and not value.is_dir(): raise RuntimeError('unsafe fixture path')
                    return value
                namespace['safe']=windows_safe
            if data_target:
                # Linux file-copy/UID tests use a private unprivileged /tmp
                # tree. Production destination-prefix/root-parent validation
                # is not bypassed outside this isolated fixture.
                def private_migration(before):
                    origin=Path(before['DATA_DIRECTORY'])
                    if data_target.exists() or base not in data_target.parents: raise RuntimeError('fixture target occupied')
                    _,size,count=namespace['tree'](origin)
                    return origin,data_target,size,count,10**12
                namespace['migration']=private_migration
            if broken_copy:
                def interrupted(args,display=None):
                    if args[0]=='cp':
                        Path(args[-1]).mkdir(); (Path(args[-1])/'partial').write_text('partial')
                        raise RuntimeError('synthetic copy interruption')
                    return fake_run(args,display)
                namespace['run']=interrupted
            output=io.StringIO()
            with contextlib.redirect_stdout(output):
                exec(compile(payload['operations'][action],action,'exec'),namespace)
            return namespace,output.getvalue()
        job=str(uuid.uuid4())
        existing=set(str(p.relative_to(root)) for p in root.rglob('*'))
        namespace,output=execute('read-config',job)
        actual=json.loads(output)
        assert actual['columns']==['key','value'] and 'PRIVATE_TOKEN' not in output and 'must-not' not in output
        assert existing==set(str(p.relative_to(root)) for p in root.rglob('*'))
        first=namespace['actual']()
        values=dict(first,BIND_ADDRESS='0.0.0.0',WEB_PORT=19924,MAX_UPLOAD_MIB=50,MEMORY_MIB=1024)
        if application=='ytlabel':
            changed=base/'changed-source'; changed.mkdir(); (changed/'image.png').write_bytes(b'different-image')
            rejected=str(uuid.uuid4())
            try:
                execute('configure',rejected,dict(values,SOURCE_DIRECTORY=str(changed)))
                raise AssertionError('legacy/new source collision accepted')
            except RuntimeError as error:
                assert '新旧素材同名' in str(error)
            assert not (root/'config/management-backups'/rejected).exists()
            foreign=base/'foreign'; foreign.mkdir(); (foreign/'keep').write_bytes(b'foreign-data')
            samples=root/'data/uploads/samples'; samples.mkdir(parents=True,exist_ok=True)
            trapped=samples/'escape'
            (changed/'escape').mkdir(); (changed/'escape/a.png').write_bytes(b'must-stay-inside')
            try:
                trapped.symlink_to(foreign,target_is_directory=True)
            except OSError:
                pass
            else:
                before_foreign=foreign.stat()
                try:
                    namespace['import_source'](dict(values,SOURCE_DIRECTORY=str(changed)),True)
                    raise AssertionError('symlink ancestor accepted')
                except RuntimeError as error:
                    assert '链接' in str(error)
                assert not (foreign/'a.png').exists() and foreign.stat().st_mode==before_foreign.st_mode and (foreign/'keep').read_bytes()==b'foreign-data'
                trapped.unlink()
        namespace,_=execute('configure',job,values)
        execute('verify',job)
        if mode=='DOCKER':
            current=locate(name)
            assert current['Config']['Labels']['retained']=='yes' and 'PRIVATE_TOKEN=must-not-be-returned' in current['Config']['Env']
            bound[0]=current['Id']
        if application=='ytlabel':
            assert (root/'data/uploads/samples/image.png').read_bytes()==b'image-original'
            assert 'BindReadOnlyPaths=' not in service.read_text()
            (source/'image.png').write_bytes(b'changed-source')
            (source/'new.png').write_bytes(b'not-auto-imported')
        namespace,_=execute('read-config',str(uuid.uuid4()))
        before=namespace['actual']()
        second=str(uuid.uuid4())
        values['WEB_PORT']=19925
        execute('configure',second,values)
        execute('verify',second)
        execute('rollback',second)
        namespace,_=execute('read-config',str(uuid.uuid4()))
        assert namespace['actual']()==before
        assert not any(args[:2] in (['systemctl','start'],['docker','start']) for args in calls)
        if os.name!='nt':
            target=base/'migrated'
            failed=str(uuid.uuid4())
            try:
                execute('migrate-data',failed,data_target=target,broken_copy=True)
                raise AssertionError('copy interruption accepted')
            except RuntimeError as error:
                assert str(error)=='synthetic copy interruption'
            execute('rollback',failed)
            assert not target.exists()
            success=str(uuid.uuid4())
            execute('check-data-migration',success,data_target=target)
            execute('migrate-data',success,data_target=target)
            execute('verify',success)
            if mode=='DOCKER': bound[0]=locate(name)['Id']
            assert (target/'sample.txt').read_bytes()==b'original-dataset' and (root/'data/sample.txt').read_bytes()==b'original-dataset'
            assert (target/'sample.txt').stat().st_ino==(target/'hardlink.txt').stat().st_ino
            if application=='ytlabel': assert not (target/'uploads/samples/new.png').exists()
            namespace,_=execute('read-config',str(uuid.uuid4()))
            migrated=namespace['actual']()
            values=dict(migrated,WEB_PORT=19926)
            execute('configure',str(uuid.uuid4()),values)
            namespace,_=execute('read-config',str(uuid.uuid4()))
            assert namespace['actual']()['DATA_DIRECTORY']==str(target)
        report[application+'-'+mode]={'readOnlyNoWrites':True,'configureVerified':True,'rollbackVerified':True,'stoppedPreserved':True,'linuxCopyHardlinks':os.name!='nt'}
print(json.dumps(report))
