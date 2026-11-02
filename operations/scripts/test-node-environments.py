"""OPS-NODE-01 UI checks using an isolated real controller and named fixtures.

Catalog/auth/assets are real. Host capabilities, plans, component updates and
owned instances are browser fixtures. No target command is ever forwarded.
"""
import argparse
from copy import deepcopy
from datetime import datetime, timedelta, timezone
import json
from pathlib import Path
import secrets
import socket
import subprocess
import time
import traceback
from urllib.parse import urlsplit
import urllib.request
import uuid

from playwright.sync_api import expect, sync_playwright
from secure_request import protect_request


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--browser-executable", required=True)
    args = parser.parse_args()
    output = Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=False)
    binary, state = str(Path(args.binary).resolve()), output / "state"
    library = output / "empty-library"
    library.mkdir()
    (library / "library.json").write_text('{"schemaVersion":1,"materials":[]}', encoding="utf8")
    username, password = "methods_" + secrets.token_hex(5), "Methods-" + secrets.token_hex(16)
    password_file = output / ".password.secret"
    flags = getattr(subprocess, "CREATE_NO_WINDOW", 0)
    process, page = None, None
    checks, errors, calls, blocked = [], [], [], []
    result = {"status": "RUNNING", "checks": checks, "browserErrors": errors,
              "scope": "Isolated controller auth/assets/catalog; Node host/actions use named browser fixtures",
              "targetMutationExecuted": False, "unexpectedMutations": blocked,
              "limitations": ["No remote Linux/APT/Docker install or uninstall acceptance", "No existing controller state opened"]}
    try:
        password_file.write_text(password, encoding="utf8")
        try:
            subprocess.run([binary, "init-admin", "--state", str(state), "--username", username,
                            "--password-file", str(password_file)], check=True, capture_output=True, timeout=30, creationflags=flags)
        finally:
            password_file.unlink(missing_ok=True)
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        with (output / "runtime.log").open("w", encoding="utf8") as log:
            process = subprocess.Popen([binary, "serve", "--state", str(state), "--listen", f"127.0.0.1:{port}",
                                        "--material-library", str(library)], stdout=log, stderr=log, creationflags=flags)
            for _ in range(150):
                try:
                    urllib.request.urlopen(base + "/ops/api/v1/health", timeout=1).close()
                    break
                except OSError:
                    if process.poll() is not None:
                        raise AssertionError("isolated controller exited")
                    time.sleep(.1)
            else:
                raise AssertionError("isolated controller did not start")
            with sync_playwright() as playwright:
                browser = playwright.chromium.launch(headless=True, executable_path=args.browser_executable)
                page = browser.new_page(viewport={"width": 1440, "height": 1000})
                page.set_default_timeout(12000)
                page.on("pageerror", lambda error: errors.append(str(error)))
                page.goto(base)
                page.get_by_label("账号", exact=True).fill(username)
                page.get_by_label("密码", exact=True).fill(password)
                page.get_by_role("button", name="登录", exact=True).click()
                page.get_by_role("heading", name="运行总览", exact=True).wait_for()

                def api(path, method="GET", data=None, expected=200):
                    assert method == "GET" or path.startswith("/auth/") or (path == "/hosts" and method == "POST"), path
                    data = protect_request(path, method, data, lambda target: api("/auth/secure-challenge", "POST", {"path": target}))
                    csrf = page.request.get(base + "/ops/api/v1/auth/csrf").json()["data"]["csrfToken"]
                    response = page.request.fetch(base + "/ops/api/v1" + path, method=method, data=data,
                                                  headers={"Origin": base, "X-CSRF-Token": csrf, "Content-Type": "application/json"})
                    body = response.json()
                    assert response.status == expected, (path, response.status, body.get("code"))
                    return body["data"]

                host = api("/hosts", "POST", {"name": "安装方式隔离主机", "kind": "LOCAL"}, 201)

                catalog = api('/node-environment-recipes')
                assert catalog['node'] and catalog['tools']
                now = datetime.now(timezone.utc)
                stamp = now.isoformat().replace('+00:00','Z')
                expiry = (now + timedelta(hours=1)).isoformat().replace('+00:00','Z')
                root = '/home/ubuntu/.local/share/streamfusion-ops/node/ui-environment'
                cap = {'status':'AVAILABLE','code':'UI_FIXTURE','message':'Isolated fixture','observedAt':stamp}
                fixture_host = {**host,'name':'Node 界面夹具','kind':'SSH','address':'192.0.2.21','sshUser':'ubuntu','version':'1','maintenance':False,'capabilities':{'reachability':cap,'readOnly':cap,'mutation':cap,'docker':cap,'expiresAt':(now-timedelta(minutes=1)).isoformat()}}
                env = {'id':'ui-environment','hostId':host['id'],'name':'开发环境','account':'ubuntu','accountUid':'1000','rootPath':root,'managerType':'NVM','managementMode':'MANAGED','nvmVersion':'0.40.8','version':'1','observedAt':stamp,'observedDefaultVersion':'','desiredDefaultSoftwareId':'','observation':{'versions':[],'nvmHash':catalog['tools'][0]['native']['parts'][0]['sha256']},'versions':[],'lastJobId':''}
                material = {'id':'node-material','name':'Node 离线材料','recipeKey':catalog['node'][0]['key'],'recipeVersion':catalog['node'][0]['version'],'recipeRevision':catalog['node'][0]['revision'],'runtimeMode':'NATIVE','status':'AVAILABLE','available':True,'byteSize':100}
                registries = [{'id':'npm-official','name':'npm 官方','url':'https://registry.npmjs.org/','version':'1','builtIn':True},{'id':'npmmirror','name':'npmmirror','url':'https://registry.npmmirror.com/','version':'1','builtIn':False}]
                env['observation']['registry']={'status':'DEFAULT','url':'https://registry.npmjs.org/','configPath':'/home/ubuntu/.npmrc','exists':False}
                envs, history, plans, submissions = [], [], {}, []
                context={'environmentId':env['id'],'account':env['account'],'accountUid':env['accountUid'],'rootPath':root,'nvmVersion':env['nvmVersion'],'nvmHash':env['observation']['nvmHash']}
                node_version = catalog['node'][0]['version']
                initialized = True
                delay_registry = False
                pending_registry_request = None
                def route_api(route):
                    nonlocal initialized, delay_registry, pending_registry_request
                    req = route.request
                    path = urlsplit(req.url).path.removeprefix('/ops/api/v1')
                    data = req.post_data_json if req.method == 'POST' else None
                    calls.append({'path':path,'method':req.method,'data':data})
                    def reply(value,status=200): route.fulfill(status=status,json={'data':value})
                    if path.startswith('/auth/'): route.continue_()
                    elif path == '/hosts': reply([fixture_host])
                    elif path == f"/hosts/{host['id']}/preparation" and urlsplit(req.url).query == 'checkUpdate=1': reply({'hostId':host['id'],'expectedHostVersion':fixture_host['version'],'update':{'status':'CURRENT'}})
                    elif path == '/node-registries' and req.method == 'GET': reply(registries)
                    elif path == '/node-registries' and req.method == 'POST':
                        entry={'id':'custom-registry','version':'1','builtIn':False,**data};registries.append(entry);reply(entry,201)
                    elif path.startswith('/node-registries/') and req.method == 'PATCH':
                        entry=next(x for x in registries if x['id']==path.split('/')[-1]);change=json.loads(req.post_data);assert not entry['builtIn'] and change['expectedVersion']==entry['version']
                        entry.update(name=change['name'],url=change['url'],version=str(int(entry['version'])+1));reply(entry)
                    elif path.startswith('/node-registries/') and req.method == 'DELETE':
                        entry=next(x for x in registries if x['id']==path.split('/')[-1]);assert not entry['builtIn']
                        assert json.loads(req.post_data)['expectedVersion']==entry['version'];registries.remove(entry);reply({})
                    elif path == '/software-instances': reply(env['versions'])
                    elif path == '/installation-materials': reply([material])
                    elif path == f"/hosts/{host['id']}/node-environments": reply(envs)
                    elif path == '/node-environments/ui-environment': reply(env)
                    elif path == '/node-environments/ui-environment/jobs': reply(history)
                    elif path.endswith('/node-environments/discover'): reply({'candidates':[{**env['observation'],'account':'ubuntu','accountUid':'1000','rootPath':'/home/ubuntu/.nvm','managerType':'NVM','exists':True}],'expiresAt':expiry})
                    elif path.endswith('/node-environments/register'):
                        external={**env,'id':'external','name':'已有只读环境','managementMode':'OBSERVE','rootPath':'/home/ubuntu/.nvm','lastJobId':''}
                        envs.append(external); reply(external)
                    elif path == '/node-environments/external': reply(envs[-1])
                    elif path == '/node-environments/external/jobs': reply([])
                    elif path.endswith('/observe'): reply(env)
                    elif path.endswith('-plans'):
                        if data.get('action')=='SET_NPM_REGISTRY' and delay_registry:
                            delay_registry=False;pending_registry_request=route;return
                        plan_id='plan-'+str(len(plans)+1)
                        plans[plan_id]=data
                        reply({'id':plan_id,'environmentId':env['id'],'expiresAt':expiry,'checks':[{'key':'FIXTURE','status':'PASSED','message':'界面夹具预检，无目标执行'}],'commands':([{'executable':'install','args':['-m','600','nvm.sh',root+'/nvm/nvm.sh']},{'executable':'install','args':['-m','700','nvm-exec',root+'/nvm/nvm-exec']}] if data.get('action') in ('INITIALIZE','UPDATE_NVM') else []),'removedPaths':[root] if data.get('deleteData') else [],'registry':next((x for x in registries if x['id']==data.get('registryId')),None),'change':{'context':context},'definition':next((r for r in catalog['node'] if r['version']==data.get('recipeVersion')),None),'source':data.get('source'),'config':data.get('config'),'node':{**context,'environmentVersion':env['version'],'makeDefault':data.get('node',{}).get('makeDefault',False),'connectTerminal':data.get('node',{}).get('connectTerminal',False)},'paths':{'root':root+'/nvm/versions/node/'+data['recipeVersion']} if data.get('recipeVersion') else None})
                    elif path.endswith('/submit'):
                        intent=plans[path.split('/')[-2]];submissions.append({'intent':intent,'submit':data})
                        if '/software-install-plans/' in path:
                            assert intent['node']['connectTerminal'] is True
                            env['observation']['terminal']={'status':'CONNECTED','profiles':[]}
                            v=intent['recipeVersion']
                            env['observation']['versions'].append({'version':v,'npmVersion':'11.6.2','pnpmVersion':'','binaryPath':root+'/nvm/versions/node/'+v+'/bin/node','globalPackages':[]})
                            env['versions'].append({'id':'node-'+v,'name':'Node.js '+v,'hostId':host['id'],'softwareType':'NODE','softwareVersion':v,'version':'1','runtimeKind':'TOOL','managementMode':'MANAGED','installationStatus':'READY','nodeDataPaths':[root+'/cache/'+v,root+'/backups/'+v,root+'/nvm/.cache/bin/node-'+v+'-linux-x64'],'installationPaths':{'root':root+'/nvm/versions/node/'+v,'data':root+'/cache/'+v,'logs':root+'/backups/'+v},'locator':root+'/nvm/versions/node/'+v+'/bin/node'})
                            if intent['node']['makeDefault']: env['observedDefaultVersion']=v;env['desiredDefaultSoftwareId']='node-'+v
                        elif intent['action']=='SET_NPM_REGISTRY':
                            entry=next(x for x in registries if x['id']==intent['registryId']);assert entry['version']==intent['expectedRegistryVersion']
                            assert not intent.get('source') and not intent.get('nodeVersion')
                            env['observation']['registry']={'status':'CONFIGURED','url':entry['url'],'configPath':'/home/ubuntu/.npmrc','exists':True}
                        elif intent['action']=='CONNECT_TERMINAL':
                            assert not intent.get('source') and not intent.get('nodeVersion') and not intent.get('packageVersion')
                            env['observation']['terminal']={'status':'CONNECTED','profiles':[]}
                        elif intent['action']=='INITIALIZE': envs.append(env)
                        elif intent['action']=='SET_DEFAULT': env['observedDefaultVersion']=intent['nodeVersion']
                        elif intent['action']=='INSTALL_PNPM': env['observation']['versions'][0]['pnpmVersion']=intent['packageVersion']
                        elif intent['action']=='REMOVE_PNPM': env['observation']['versions'][0]['pnpmVersion']=''
                        elif intent['action']=='REMOVE_ENVIRONMENT': envs.clear()
                        job={'id':'job-'+str(len(history)+1),'phase':'NODE_ENVIRONMENT_UPDATED','status':'SUCCEEDED','updatedAt':stamp,'traceId':'ui-trace','errorCode':''}
                        history.append(job);env['lastJobId']=job['id'];env['version']=str(int(env['version'])+1)
                        reply({'jobId':job['id'],'environmentId':env['id']})
                    elif path.startswith('/jobs/') and path.endswith('/logs'): reply({'text':'fixture: no target commands executed'})
                    elif path.startswith('/jobs/') and path.endswith('/reconcile'):
                        history[-1]['status']='SUCCEEDED';reply(history[-1])
                    elif path.startswith('/jobs/'): reply(next(j for j in history if j['id']==path.split('/')[2]))
                    elif path.endswith('/actions'):
                        submissions.append({'uninstall':data})
                        assert data['action']=='UNINSTALL' and data['clearNodeDefault']
                        assert data['confirmationName']==env['versions'][0]['name']
                        env['versions'].clear();env['observation']['versions'].clear();env['observedDefaultVersion']='';env['desiredDefaultSoftwareId']=''
                        reply({'jobId':env['lastJobId']})
                    elif req.method not in ('GET','HEAD','OPTIONS'):
                        blocked.append({'path':path,'method':req.method});route.fulfill(status=409,json={'code':'TEST_MUTATION_BLOCKED','msg':'No real target execution'})
                    else: route.continue_()
                page.route('**/ops/api/v1/**',route_api)
                def enter():
                    page.goto(base)
                    page.get_by_role('button',name='软件管理',exact=True).click()
                    expect(page.get_by_role('tablist',name='软件管理视图',exact=True).get_by_role('tab')).to_have_count(2)
                    assert not page.get_by_role('tab',name='Node.js 环境',exact=True).count()
                    page.get_by_role('tab',name='软件目录',exact=True).click()
                    page.locator('article.node-catalog-card').get_by_role('button',name='安装与管理',exact=True).click()
                    page.get_by_role('dialog',name='Node.js',exact=True).wait_for()
                def dialog(title): return page.get_by_role('dialog',name=title,exact=True)
                def layout(subject,name):
                    for width in (1440,390):
                        page.set_viewport_size({'width':width,'height':1000});page.wait_for_timeout(120)
                        assert page.evaluate('document.documentElement.scrollWidth <= innerWidth'),(name,width,'document')
                        assert subject.evaluate('e=>e.scrollWidth<=e.clientWidth+1'),(name,width,'subject')
                        if subject.locator('.node-form').count():
                            footer=subject.locator('.el-drawer__footer')
                            box=footer.bounding_box()
                            assert box and box['y']>=0 and box['y']+box['height']<=1001,(name,width,'fixed footer')
                            before=box['y']
                            subject.locator('.el-drawer__body').evaluate('e=>{e.scrollTop=e.scrollHeight}')
                            assert abs(footer.bounding_box()['y']-before)<1,(name,width,'footer moved')
                            subject.locator('.el-drawer__body').evaluate('e=>{e.scrollTop=0}')

                        page.screenshot(path=str(output/f'{name}-{width}.png'),animations='disabled')
                        checks.append(f'{name}-{width}')
                    page.set_viewport_size({'width':1440,'height':1000})
                page.get_by_role('button',name='主机管理',exact=True).click()
                page.get_by_role('button',name='新增主机',exact=True).click()
                connection=dialog('新增主机')
                page.mouse.click(120,400);connection.wait_for(state='hidden')
                page.get_by_role('button',name='新增主机',exact=True).click()
                connection.get_by_label('主机名称',exact=True).fill('未保存连接')
                page.mouse.click(120,400)
                page.get_by_role('dialog',name='关闭窗口',exact=True).get_by_role('button',name='继续编辑',exact=True).click()
                expect(connection.get_by_label('主机名称',exact=True)).to_have_value('未保存连接')
                connection.get_by_role('button',name='取消',exact=True).click()
                page.get_by_role('dialog',name='关闭窗口',exact=True).get_by_role('button',name='放弃修改',exact=True).click()
                connection.wait_for(state='hidden')
                checks.append('connection-mask-clean-close-dirty-cancel-and-explicit-discard')
                enter()
                page.get_by_role('button',name='新建 Node 环境',exact=True).click()
                first=dialog('准备 Node 环境')
                expect(first.get_by_label('首次 Node 版本',exact=True)).to_have_value(node_version)
                first.get_by_label('Node 环境名称',exact=True).fill('保留的草稿')
                page.mouse.click(400,400)
                discard=page.get_by_role('dialog',name='关闭安装窗口',exact=True)
                discard.get_by_role('button',name='继续编辑',exact=True).click()
                expect(dialog('Node.js')).to_be_visible()
                checks.append('dirty-child-mask-offers-discard-and-keeps-parent-open')
                page.wait_for_timeout(4500)
                expect(first.get_by_label('Node 环境名称',exact=True)).to_have_value('保留的草稿')
                checks.append('node-draft-survives-catalog-refresh-without-separate-tab')
                first.get_by_role('button',name='检查安装条件',exact=True).click()
                expect(first.get_by_text('预检完成',exact=True)).to_be_visible()
                layout(first,'node-first-confirmation')
                first.get_by_role('button',name='确认执行',exact=True).click()
                first.wait_for(state='hidden')
                expect(page.locator('.node-version')).to_have_count(1)
                assert len(submissions)==2 and submissions[0]['intent']['action']=='INITIALIZE'
                assert submissions[1]['intent']['recipeVersion']==node_version
                checks.append('single-confirmation-initializes-and-installs-exact-frozen-node-with-two-jobs')
                assert not page.get_by_text('安装前请检查主机能力。',exact=True).count()
                assert not any(c['path'].endswith('/checks') for c in calls)
                checks.append('expired-capability-cache-does-not-add-manual-or-redundant-check-gate')
                page.locator('.job-result details').get_by_text('诊断信息',exact=True).click()
                expect(page.get_by_text('已完成（SUCCEEDED） · Node 环境已更新（NODE_ENVIRONMENT_UPDATED）',exact=False)).to_be_visible()
                checks.append('node-diagnostic-chinese-status-and-original-code')
                env['versions'].clear();env['observation']['versions'].clear();env['observedDefaultVersion']='';env['desiredDefaultSoftwareId']='';env['lastJobId']='';history.clear();submissions.clear()
                enter()
                layout(page.locator('.node-environments'),'node-empty')
                page.get_by_role('button',name='安装 Node 版本',exact=True).click()
                install=dialog('安装 Node 版本')
                install.get_by_role('radio',name='Ops 离线安装',exact=True).check()
                expect(install.get_by_label('Node 离线材料',exact=True)).to_have_value('node-material')
                layout(install,'node-offline')
                install.get_by_role('radio',name='官方在线安装',exact=True).check()
                install.get_by_role('button',name='检查安装条件',exact=True).click()
                expect(install.get_by_text('预检完成',exact=True)).to_be_visible()
                assert not submissions
                install.get_by_role('button',name='确认执行',exact=True).click()
                install.wait_for(state='hidden')
                expect(page.locator('.node-version')).to_have_count(1)
                assert submissions[-1]['intent']['source']=={'type':'OFFICIAL'}
                assert set(submissions[-1]['submit'])=={'idempotencyKey'}
                checks.append('online-explicit-submit-and-reload-installed-version')
                expect(page.get_by_text('重新登录即可使用 node/npm',exact=True)).to_be_visible()
                env['observation']['terminal']={'status':'NOT_CONNECTED','profiles':[]}
                page.get_by_role('button',name='检测环境',exact=True).click()
                page.get_by_role('button',name='接入终端',exact=True).click()
                terminal=dialog('接入终端')
                terminal.get_by_role('button',name='检查安装条件',exact=True).click()
                assert submissions[-1]['intent'].get('action')!='CONNECT_TERMINAL'
                terminal.get_by_role('button',name='确认执行',exact=True).click()
                terminal.wait_for(state='hidden')
                expect(page.get_by_text('Bash 已接入',exact=True)).to_be_visible()
                checks.append('existing-environment-terminal-connection-requires-preview-and-confirmation')
                manager=dialog('Node.js')
                manager.get_by_role('tab',name='npm 镜像',exact=True).click()
                registry=manager.get_by_label('npm 镜像管理',exact=True)
                expect(registry.get_by_text('https://registry.npmjs.org/',exact=True).first).to_be_visible()
                official=registry.locator('article').filter(has_text='npm 官方')
                assert not official.get_by_role('button',name='删除',exact=True).count()
                registry.get_by_label('npm 镜像名称',exact=True).fill('公司内网')
                registry.get_by_label('npm 镜像地址',exact=True).fill('http://192.0.2.50:4873/npm/?token=secret')
                expect(registry.get_by_role('button',name='添加地址',exact=True)).to_be_disabled()
                registry.get_by_label('npm 镜像地址',exact=True).fill('http://192.0.2.50:4873/npm/')
                registry.get_by_role('button',name='添加地址',exact=True).click()
                custom=registry.locator('article').filter(has_text='公司内网')
                custom.get_by_role('button',name='编辑',exact=True).click()
                registry.get_by_label('npm 镜像地址',exact=True).fill('http://192.0.2.50:4873/npm-v2/')
                registry.get_by_role('button',name='保存修改',exact=True).click()
                expect(custom.get_by_text('http://192.0.2.50:4873/npm-v2/',exact=True)).to_be_visible()
                assert env['observation']['registry']['url']=='https://registry.npmjs.org/'
                checks.append('registry-edit-updates-version-without-changing-server')
                custom.get_by_role('button',name='切换',exact=True).click()
                switch=dialog('切换 npm 镜像')
                page.mouse.click(400,400);switch.wait_for(state='hidden');expect(manager).to_be_visible()
                custom.get_by_role('button',name='切换',exact=True).click()
                delay_registry=True
                switch.get_by_role('button',name='检查安装条件',exact=True).click()
                expect(switch.get_by_role('button',name='检查中…',exact=True)).to_be_disabled()
                page.mouse.click(400,400);expect(switch).to_be_visible();expect(manager).to_be_visible()
                assert pending_registry_request
                pending_registry_request.fulfill(status=503,json={'code':'UI_FIXTURE_RETRY','msg':'夹具预检稍后重试'})
                expect(switch.get_by_role('button',name='检查安装条件',exact=True)).to_be_enabled()
                checks.append('busy-child-mask-cannot-close-either-drawer')
                switch.get_by_role('button',name='检查安装条件',exact=True).click()
                expect(switch.get_by_text('http://192.0.2.50:4873/npm-v2/',exact=True)).to_be_visible()
                assert env['observation']['registry']['status']=='DEFAULT'
                switch.get_by_role('button',name='确认执行',exact=True).click();switch.wait_for(state='hidden')
                expect(custom.get_by_text('当前',exact=True)).to_be_visible()
                layout(registry,'npm-registry')
                custom.get_by_role('button',name='删除',exact=True).click()
                page.get_by_role('dialog',name='删除镜像地址',exact=True).get_by_role('button',name='删除',exact=True).click()
                expect(custom).to_have_count(0)
                assert env['observation']['registry']['url']=='http://192.0.2.50:4873/npm-v2/'
                checks.append('registry-add-switch-preview-and-delete-preserve-server-url')
                saved_registry=deepcopy(env['observation']['registry'])
                for status,label in [('REDACTED','地址含受保护配置'),('UNSAFE','配置文件不可安全读取'),('UNAVAILABLE','npm 配置暂不可读')]:
                    env['observation']['registry']={**saved_registry,'status':status,'url':'https://hidden-user:hidden-token@example.invalid/'}
                    manager.get_by_role('button',name='检测环境',exact=True).click()
                    expect(registry.get_by_text(label,exact=True)).to_be_visible()
                    assert 'hidden-token' not in registry.inner_text()
                env['observation']['registry']=saved_registry
                manager.get_by_role('button',name='检测环境',exact=True).click()
                expect(registry.get_by_text('当前账号镜像',exact=True)).to_be_visible()
                checks.append('unsafe-or-redacted-registry-statuses-never-render-protected-url')
                page.get_by_role('dialog',name='删除镜像地址',exact=True).wait_for(state='hidden')
                page.locator('.el-overlay.is-message-box:visible').wait_for(state='hidden')
                page.mouse.click(120,400);manager.wait_for(state='hidden')
                page.locator('article.node-catalog-card').get_by_role('button',name='安装与管理',exact=True).click();manager.wait_for()
                expect(manager.get_by_role('tab',name='npm 镜像',exact=True)).to_have_attribute('aria-selected','true')
                checks.append('parent-mask-close-keeps-selected-node-tab')
                manager.get_by_role('tab',name='版本列表',exact=True).click()
                layout(page.locator('.node-environments'),'node-installed')
                manager=dialog('Node.js')
                manager.get_by_role('tab',name='来源与目录',exact=True).click()
                manager.get_by_role('button',name='Close this dialog',exact=True).click()
                manager.wait_for(state='hidden')
                page.locator('article.node-catalog-card').get_by_role('button',name='安装与管理',exact=True).click()
                manager.wait_for()
                expect(manager.get_by_role('tab',name='来源与目录',exact=True)).to_have_attribute('aria-selected','true')
                manager.get_by_role('button',name='Close this dialog',exact=True).click()
                manager.wait_for(state='hidden')
                page.get_by_role('tab',name='软件中心',exact=True).click()
                page.locator('article.software-card').filter(has=page.get_by_role('heading',name='Node.js '+node_version,exact=True)).get_by_role('button',name='详情',exact=True).click()
                manager.wait_for()
                manager.get_by_role('tab',name='版本列表',exact=True).click()
                checks.append('catalog-and-instance-open-same-preserved-node-drawer')
                page.get_by_role('button',name='安装 pnpm',exact=True).click()
                tool=dialog('安装 pnpm');tool.get_by_role('button',name='检查安装条件',exact=True).click();tool.get_by_role('button',name='确认执行',exact=True).click();tool.wait_for(state='hidden')
                expect(page.get_by_role('button',name='更新 pnpm',exact=True)).to_be_visible()
                checks.append('pnpm-operation-refreshes-selected-version')
                history[-1]['status']='UNKNOWN'
                enter()
                expect(page.get_by_role('button',name='安装 Node 版本',exact=True)).to_be_disabled()
                page.mouse.click(120,400);expect(dialog('Node.js')).to_be_visible()
                checks.append('unknown-job-prevents-parent-mask-close')
                page.get_by_role('button',name='检查操作结果',exact=True).click()
                expect(page.get_by_role('button',name='安装 Node 版本',exact=True)).to_be_enabled()
                checks.append('unknown-blocks-new-operations-and-reconciles-original')
                page.get_by_role('button',name='卸载版本',exact=True).click()
                remove=dialog('卸载 Node 版本')
                clear=remove.get_by_role('checkbox',name='清除环境默认版本（这是最后一个版本）',exact=True)
                expect(clear).not_to_be_checked()
                remove.get_by_label('确认 Node 环境名称',exact=True).fill('Node.js '+node_version)
                expect(remove.get_by_role('button',name='确认执行',exact=True)).to_be_disabled()
                clear.check()
                deletion=remove.get_by_role('checkbox',name='同时清理本次操作对应的附属数据',exact=True)
                expect(deletion).not_to_be_checked()
                deletion.check()
                expect(remove.get_by_text(root+'/cache/'+node_version,exact=True)).to_be_visible()
                expect(remove.get_by_text(root+'/backups/'+node_version,exact=True)).to_be_visible()
                layout(remove,'node-uninstall')
                deletion.uncheck();remove.get_by_role('button',name='确认执行',exact=True).click();remove.wait_for(state='hidden')
                expect(page.locator('.node-version')).to_have_count(0)
                assert submissions[-1]['uninstall']['deleteData'] is False
                checks.append('default-clear-confirmation-and-exact-optional-paths-keep-data-submit')
                page.get_by_role('button',name='检测已有安装',exact=True).click()
                discovery=dialog('检测已有 Node 安装');discovery.get_by_role('button',name='开始检测',exact=True).click();discovery.get_by_role('button',name='登记此环境',exact=True).click();discovery.wait_for(state='hidden')
                expect(page.get_by_role('button',name='安装 Node 版本',exact=True)).to_be_disabled()
                checks.append('self-install-discovery-registers-readonly-environment')
                dialog('Node.js').get_by_role('tab',name='npm 镜像',exact=True).click()
                registry=dialog('Node.js').get_by_label('npm 镜像管理',exact=True)
                expect(registry.get_by_role('button',name='切换',exact=True).first).to_be_disabled()
                expect(registry.get_by_label('npm 镜像名称',exact=True)).to_be_disabled()
                checks.append('externally-installed-environment-keeps-registry-controls-readonly')
                assert not blocked and not errors,(blocked,errors)
                assert not api('/software-instances'), 'fixture changes must not touch controller storage'
                (output/'requests.json').write_text(json.dumps({'calls':calls,'submissions':submissions},ensure_ascii=False,indent=2),encoding='utf8')
                result['status']='PASS'
                browser.close()
    except Exception as error:
        result["status"], result["error"], result["traceback"] = "FAIL", str(error), traceback.format_exc()
        if page:
            try:
                page.screenshot(path=str(output / "failure.png"), animations="disabled")
            except Exception:
                pass
        raise
    finally:
        password_file.unlink(missing_ok=True)
        if process:
            if process.poll() is None:
                stopped = subprocess.run([binary, "stop", "--state", str(state)], capture_output=True, timeout=20, creationflags=flags)
                (output / "stop.log").write_bytes(stopped.stdout + stopped.stderr)
            try:
                process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                process.terminate()
                process.wait(timeout=10)
            result["runtimeExitCode"] = process.returncode
        (output / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf8")
        print(json.dumps({"status": result["status"], "checks": len(checks), "output": str(output)}, ensure_ascii=False))


if __name__ == "__main__":
    main()
