"""OPS-INSTALL-UX-04 official detail checks using an isolated real controller and named fixtures.

Catalog/auth/assets are real. Host capabilities, plans, component updates and
owned instances are browser fixtures. No target command is ever forwarded.
"""
import argparse
from copy import deepcopy
from datetime import datetime, timedelta, timezone
import json
from pathlib import Path
import secrets
import shlex
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
              "scope": "Real isolated controller/auth/catalog; official preflight browser fixtures; no target mutations",
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

                definitions, official = api('/software-recipes'), api('/software-official-recipes')
                definition=next(item for item in official if item['key']=='docker')
                parts=definition['native']['parts']
                assert len(parts)==5
                now=datetime.now(timezone.utc)
                stamp=now.isoformat().replace('+00:00','Z')
                expiry=(now+timedelta(hours=1)).isoformat().replace('+00:00','Z')
                cap={'status':'AVAILABLE','code':'UI_FIXTURE','message':'Isolated fixture','observedAt':stamp}
                fixture_host={**host,'name':'安装详情夹具','kind':'SSH','address':'192.0.2.26','version':'1','installRoot':'/opt/streamfusion-ops','maintenance':False,'capabilities':{'reachability':cap,'readOnly':cap,'mutation':cap,'docker':{**cap,'status':'UNAVAILABLE'},'expiresAt':expiry,'platform':'linux/amd64'}}
                package_rows=[{'name':part['name'].split('_')[0],'version':part['name'].split('_')[1],'origin':part['url'],'direct':True} for part in parts]
                package_rows += [{'name':f'libdependency-{i:02d}','version':'1.24.0-2ubuntu7.19','origin':'Ubuntu noble-updates/main amd64 Packages','direct':False} for i in range(30)]
                package_rows.append({'name':'unsafe-origin-fixture','version':'1.0','origin':'<img src=x onerror="window.__sourceExecuted=true">','direct':False})
                commands=[{'executable':'/usr/bin/apt-get','args':['-o','DPkg::Lock::Timeout=15','-o','APT::Update::Error-Mode=any','update']},{'executable':'/usr/bin/apt-get','args':['--assume-yes','--no-remove','--no-upgrade','--no-install-recommends','-o','DPkg::Lock::Timeout=15','install','--',*['./'+p['name'] for p in parts]]},{'executable':'/usr/bin/systemctl','args':['enable','--now','--','docker.service']}]
                official_plan={'manager':'APT','os':'ubuntu24.04','architecture':'amd64','packages':package_rows,'commands':commands,'paths':{'root':'/','binary':'/usr/bin/dockerd','config':'/etc/docker','data':'/var/lib/docker','logs':'','unit':'docker.service','ownedPaths':['/etc/docker','/var/lib/docker']}}
                def route_api(route):
                    req=route.request
                    path=urlsplit(req.url).path.removeprefix('/ops/api/v1')
                    calls.append({'path':path,'method':req.method})
                    if path.startswith('/auth/'):route.continue_()
                    elif path=='/hosts':route.fulfill(json={'data':[fixture_host]})
                    elif path=='/software-instances':route.fulfill(json={'data':[]})
                    elif path==f"/hosts/{host['id']}/preparation":route.fulfill(json={'data':{'hostId':host['id'],'expectedHostVersion':fixture_host['version'],'update':{'status':'CURRENT'}}})
                    elif path==f"/hosts/{host['id']}/software-install-plans":
                        payload=req.post_data_json
                        assert payload['source']=={'type':'OFFICIAL'}
                        route.fulfill(json={'data':{'id':'ui-plan','definition':definition,'config':payload['config'],'source':payload['source'],'checks':[],'expiresAt':expiry,'paths':{'root':'/'},'portMappings':[],'official':official_plan}})
                    elif req.method not in ('GET','HEAD','OPTIONS'):
                        blocked.append({'path':path,'method':req.method});route.fulfill(status=409,json={'code':'TEST_MUTATION_BLOCKED','msg':'No target execution'})
                    else:route.continue_()
                page.route('**/ops/api/v1/**',route_api)
                page.add_init_script("""window.__copiedCommands=[];window.__clipboardFails=false;Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async value=>{if(window.__clipboardFails)throw new Error('denied');window.__copiedCommands.push(value)}}});""")
                page.goto(base)
                page.get_by_role('button',name='软件管理',exact=True).click()
                page.get_by_role('tab',name='软件目录',exact=True).click()
                card=page.locator('article.plugin-card').filter(has=page.get_by_role('heading',name=next(d['name'] for d in definitions if d['key']=='docker'),exact=True))
                card.get_by_role('button',name='安装',exact=True).click()
                drawer=page.locator('.el-drawer:visible').last
                drawer.get_by_role('button',name='检查官方安装条件',exact=True).click()
                details=drawer.get_by_label('官方安装详情',exact=True)
                details.wait_for()
                main=details.get_by_label('安装软件包',exact=True)
                expect(main.locator('li')).to_have_count(5)
                for i,part in enumerate(parts):
                    link=main.locator('a').nth(i)
                    expect(link).to_have_attribute('href',part['url'])
                    assert link.inner_text()=='download.docker.com ↗'
                assert details.locator('.package-dependencies').get_attribute('open') is None
                assert details.locator('.full-sources').get_attribute('open') is None
                checks.append('five-real-docker-package-names-versions-short-domain-links-no-url-wall')
                assert not details.get_by_text('目标服务器需要联网',exact=False).count()
                expect(details.get_by_label('安装命令',exact=True).locator(':scope > li')).to_have_count(3)
                for title in ['更新软件索引','安装软件包','启用并启动服务']:
                    expect(details.get_by_text(title,exact=True)).to_be_visible()
                assert details.locator('.token-executable').count()==3
                assert details.locator('.token-option').count()>=4
                for index,command in enumerate(commands):
                    details.get_by_role('button',name=f'复制第 {index+1} 步命令',exact=True).click()
                    copied=page.evaluate('window.__copiedCommands.at(-1)')
                    assert shlex.split(copied)==[command['executable'],*command['args']]
                    assert '\n' not in copied
                checks.append('all-step-copy-buttons-preserve-exact-original-argv-without-ui-line-breaks')
                for width in (1440,390):
                    page.set_viewport_size({'width':width,'height':1200});page.wait_for_timeout(180)
                    assert page.evaluate('document.documentElement.scrollWidth <= innerWidth')
                    assert details.evaluate('e=>e.scrollWidth<=e.clientWidth+1')
                    for pre in details.locator('pre').all():assert pre.evaluate('e=>e.scrollWidth<=e.clientWidth+1')
                    main.scroll_into_view_if_needed()
                    page.screenshot(path=str(output/f'official-compact-{width}.png'),animations='disabled')
                    details.locator('.terminal').screenshot(path=str(output/f'official-terminal-{width}.png'),animations='disabled')
                    checks.append(f'compact-packages-and-terminal-no-overflow-{width}')
                details.locator('.full-sources summary').click()
                for part in parts:expect(details.locator('.full-sources').get_by_text(part['url'],exact=True)).to_be_visible()
                assert not details.locator('.full-sources img').count()
                assert page.evaluate('window.__sourceExecuted') is None
                assert details.evaluate('e=>e.scrollWidth<=e.clientWidth+1')
                details.locator('.full-sources').screenshot(path=str(output/'official-expanded-sources-390.png'),animations='disabled')
                details.locator('.full-sources summary').click()
                details.locator('.package-dependencies summary').click()
                expect(details.locator('.package-dependencies li')).to_have_count(31)
                assert not details.locator('.package-dependencies img').count()
                checks.append('all-full-sources-and-dependencies-expand-with-text-escaping')
                details.locator('.package-dependencies summary').click()
                page.evaluate('window.__clipboardFails=true')
                details.get_by_role('button',name='复制第 1 步命令',exact=True).click()
                expect(details.get_by_role('status')).to_contain_text('复制未完成')
                checks.append('clipboard-denial-keeps-manual-copy-path')
                assert not blocked and not errors,(blocked,errors)
                assert not api('/software-instances') and not api('/installation-materials')
                (output/'requests.json').write_text(json.dumps(calls,ensure_ascii=False,indent=2),encoding='utf8')
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
