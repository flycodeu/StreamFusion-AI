"""OPS-SOFTWARE-06 UI checks using an isolated real controller and named fixtures.

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
from urllib.parse import parse_qs, urlsplit
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
              "scope": "Real isolated controller catalog/auth/assets; host, official plan/update and owned-instance browser fixtures",
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
                definitions, official = api("/software-recipes"), api("/software-official-recipes")
                def recipe_choice(recipe):
                    return json.dumps([recipe["key"], recipe["version"], recipe["revision"]], separators=(",", ":"))
                builtin_definitions = [item for item in definitions if not item["key"].startswith("custom-")]
                additional_definitions = [item for item in definitions if item.get("catalogKey") in {"jenkins", "postgresql", "gitlab", "prometheus", "nacos"}]
                assert len(builtin_definitions) == 18 and len(official) == 10 and len(additional_definitions) == 20
                assert not api("/installation-materials") and not api("/software-instances")
                checks.append("real-catalog-18-builtins-20-editable-additions-and-10-official-definitions-empty-library")
                now = datetime.now(timezone.utc)
                stamp = now.isoformat().replace("+00:00", "Z")
                expiry = (now + timedelta(hours=1)).isoformat().replace("+00:00", "Z")
                cap = {"status": "AVAILABLE", "code": "UI_FIXTURE", "message": "仅界面夹具", "observedAt": stamp, "source": "UI_FIXTURE"}
                fixture_host = {**host, "name": "界面夹具 Ubuntu 主机", "kind": "SSH", "address": "192.0.2.26", "version": "1",
                                "installRoot": "/opt/streamfusion-ops", "observedAt": stamp, "maintenance": False,
                                "capabilities": {"reachability": cap, "readOnly": cap, "mutation": cap, "docker": {**cap, "status": "UNAVAILABLE", "code": "DOCKER_NOT_INSTALLED"},
                                                 "expiresAt": expiry, "platform": "linux/amd64"}}
                nginx = next(item for item in official if item["key"] == "nginx")
                paths = {"root": "/", "binary": "/usr/sbin/nginx", "config": "/etc/nginx", "data": "/var/www/html", "logs": "/var/log/nginx",
                         "unit": "nginx.service", "ownedPaths": ["/etc/nginx", "/var/www/html", "/var/log/nginx"]}
                official_plan = {"manager": "APT", "os": "ubuntu24.04", "architecture": "amd64", "paths": paths,
                                 "packages": [{"name": "nginx", "version": "1.24.0-2ubuntu7.5", "origin": "Ubuntu noble-updates/main", "direct": True},
                                              {"name": "libfixture-one", "version": "1.0.0", "origin": "Ubuntu noble/main", "direct": False},
                                              {"name": "libfixture-two", "version": "2.0.0", "origin": "Ubuntu noble/main", "direct": False}],
                                 "commands": [{"executable": "/usr/bin/apt-get", "args": ["update"]},
                                              {"executable": "/usr/bin/apt-get", "args": ["install", "--yes", "nginx=1.24.0-2ubuntu7.5"]}]}
                git_plan = {**official_plan, "paths": {"root": "/", "binary": "/usr/bin/git", "config": "", "data": "", "logs": "", "unit": "", "ownedPaths": []},
                            "packages": [{"name": "git", "version": "2.43.0-1ubuntu7.4", "origin": "Ubuntu noble-updates/main", "direct": True}],
                            "commands": [{"executable": "/usr/bin/apt-get", "args": ["update"]},
                                         {"executable": "/usr/bin/apt-get", "args": ["install", "--yes", "git=2.43.0-1ubuntu7.4"]}]}
                docker_plan = {**official_plan, "paths": {"root": "/", "binary": "/usr/bin/docker", "config": "/etc/docker", "data": "/var/lib/docker", "logs": "", "unit": "docker.service", "ownedPaths": ["/etc/docker", "/var/lib/docker"]},
                               "packages": [{"name": "docker-ce", "version": "5:29.0.0-1~ubuntu.24.04~noble", "origin": "Docker noble/stable", "direct": True}],
                               "commands": [{"executable": "/usr/bin/apt-get", "args": ["update"]},
                                            {"executable": "/usr/bin/apt-get", "args": ["install", "--yes", "docker-ce"]}]}
                rows, preflights, updates = [], [], []
                pending_checks = []
                hold_host_check = False
                update_job = {"id": "old-connection", "status": "SUCCEEDED", "phase": "VERIFYING_EXECUTOR", "errorCode": "", "updatedAt": stamp}
                upgraded = False
                update_scenario = "auto"
                preflight_code = "EXECUTOR_UPGRADE_REQUIRED"
                update_attempts = {}
                installation_submits, installation_logs = [], []
                hide_install_job = False
                show_other_log = False
                hold_log_list = False
                held_log_lists = []
                hold_detail_job = False
                held_detail_jobs = []
                final_log_failures = 0
                installation_rejection = ""
                installation_job = {"id": "ui-install-job", "status": "RUNNING", "phase": "OFFICIAL_PACKAGES_APPLY", "errorCode": "",
                                    "kind": "SOFTWARE_INSTALL", "targetId": host["id"], "instanceId": "", "createdAt": stamp,
                                    "traceId": "UI_FIXTURE_INSTALL_OUTPUT", "updatedAt": stamp, "targetAckPending": False, "result": {}}
                other_log_job = {**installation_job, "id": "ui-other-log-job", "kind": "SOFTWARE_CONFIGURE"}
                git_active = False
                git_profiles, git_bindings, git_checks, git_submits, inline_credentials = [], [], [], [], []
                git_job = {"id": "ui-git-job", "status": "SUCCEEDED", "phase": "GIT_REPOSITORY_BOUND", "errorCode": "", "traceId": "UI_GIT_TRACE", "updatedAt": stamp, "targetAckPending": False}

                def route_api(route):
                    nonlocal upgraded, update_job, final_log_failures
                    request = route.request
                    path = urlsplit(request.url).path.removeprefix("/ops/api/v1")
                    calls.append({"path": path, "query": urlsplit(request.url).query, "method": request.method})
                    if path.startswith("/auth/"):
                        route.continue_()
                    elif git_active and path == "/credentials" and request.method == "GET":
                        route.fulfill(json={"data": [{"id": "git-key", "revision": "1", "kind": "SSH_KEY", "name": "工作 Git 密钥", "revokedAt": ""}]})
                    elif git_active and path == "/credentials" and request.method == "POST":
                        envelope = request.post_data_json
                        assert all(envelope.get(key) for key in ("challengeId", "clientPublicKey", "iv", "ciphertext"))
                        assert "UI_FIXTURE_NOT_A_REAL_PRIVATE_KEY" not in request.post_data and "secret" not in envelope
                        inline_credentials.append(True)
                        route.continue_()  # Real encrypted credential save in this disposable controller only.
                    elif path == "/git-accounts" and request.method == "GET":
                        route.fulfill(json={"data": git_profiles})
                    elif path == "/git-accounts" and request.method == "POST":
                        data = request.post_data_json
                        assert (data['authMethod']=='HTTPS' and data['credentialId']) or (data["keySource"] == "TARGET" and not data["credentialId"])
                        assert not data["hostPublicKey"] and not data["authorEmail"]
                        assert "privateKey" not in data
                        git_profiles.append({**data, "id": str(uuid.uuid4()), "version": "1", "configuredVersion":"0", "keyFingerprint": "SHA256:UI_GIT_KEY"})
                        route.fulfill(json={"data": git_profiles[-1]})
                    elif path.startswith('/git-accounts/') and path.endswith('/configure'):
                        selected=next(item for item in git_profiles if item['id']==path.split('/')[2])
                        selected['configuredVersion']=selected['version']
                        route.fulfill(json={'data':{'id':selected['id'],'status':'CONFIGURED','version':selected['version'],'account':'flycode','accountUid':'1000','observedAt':stamp}})
                    elif path == "/git-host-key":
                        route.fulfill(json={"data": {"hostPublicKey": "ssh-ed25519 AAAA_UI_PUBLIC_KEY", "fingerprint": "SHA256:UI_GIT_HOST"}})
                    elif path == f"/hosts/{host['id']}/git-keys":
                        data = request.post_data_json
                        assert data['account'] == 'flycode'
                        route.fulfill(json={"data": {"id": data['id'], "account": 'flycode', "accountUid": '1000', "keyPath": f"/home/flycode/.local/share/streamfusion-ops/git-keys/{data['id']}/key", "publicKey": 'ssh-ed25519 AAAA_UI_ACCOUNT_KEY', "fingerprint": 'SHA256:UI_ACCOUNT'}})
                    elif path == f"/hosts/{host['id']}/git-repositories":
                        route.fulfill(json={"data": git_bindings})
                    elif path == f"/hosts/{host['id']}/git-repository-plans":
                        data = request.post_data_json
                        git_checks.append(data)
                        route.fulfill(json={"data": {"id": "ui-git-plan", "hostId": host["id"], "action": data["action"], "change": {**git_profiles[0], "account": data["account"], "repoPath": data["repoPath"]}, "observation": {"account": data["account"], "repoPath": data["repoPath"], "branch": "main", "remoteUrl": "git@git.example.test:team/app.git", "hostKeyFingerprint": "SHA256:UI_GIT_HOST", "keyPath": "/home/flycode/.local/share/streamfusion-ops/git/ui/key"}, "expiresAt": expiry}})
                    elif path == "/git-repository-plans/ui-git-plan/submit":
                        assert request.post_data_json["confirmed"]
                        git_submits.append(request.post_data_json)
                        data = git_checks[-1]
                        git_job["phase"] = "GIT_REPOSITORY_UNBOUND" if data["action"] == "UNBIND" else "GIT_REPOSITORY_BOUND"
                        git_bindings[:] = [{"id": "ui-git-binding", "hostId": host["id"], "account": data["account"], "accountUid": "1000", "repoPath": data["repoPath"], "profileId": "ui-git-profile", "profileName": git_profiles[0]["name"], "version": str(len(git_submits)), "status": "RELEASED" if data["action"] == "UNBIND" else "BOUND", "lastJobId": "ui-git-job", "lastJobStatus": "SUCCEEDED", "observation": {"branch": "main", "remoteUrl": "git@git.example.test:team/app.git", "observedAt": stamp}}]
                        route.fulfill(json={"data": {"jobId": "ui-git-job", "bindingId": "ui-git-binding"}})
                    elif path == "/jobs/ui-git-job":
                        route.fulfill(json={"data": git_job})
                    elif path == "/git-repositories/ui-git-binding/test":
                        route.fulfill(json={"data": {"ready": True, "code": "GIT_REMOTE_READABLE", "message": "仓库可读取"}})
                    elif path == "/git-repositories/ui-git-binding/jobs":
                        route.fulfill(json={"data": [git_job]})
                    elif path == "/hosts" and request.method == "GET":
                        route.fulfill(json={"data": [fixture_host]})
                    elif path == "/software-instances" and request.method == "GET":
                        route.fulfill(json={"data": rows})
                    elif path.startswith("/software-instances/") and path.endswith("/logs") and request.method == "GET":
                        route.fulfill(json={"data": {"text": "界面夹具运行日志", "limited": False}})
                    elif path.startswith("/software-instances/") and request.method == "GET" and any(item["id"] == path.split("/")[-1] for item in rows):
                        route.fulfill(json={"data": next(item for item in rows if item["id"] == path.split("/")[-1])})
                    elif path == f"/hosts/{host['id']}/checks":
                        if hold_host_check:
                            pending_checks.append(route)
                        else:
                            route.fulfill(json={"data": fixture_host})
                    elif path == f"/hosts/{host['id']}/preparation":
                        data = {"hostId": host["id"], "expectedHostVersion": fixture_host["version"], "account": "ubuntu",
                                "installRoot": fixture_host["installRoot"], "remoteJob": update_job,
                                "package": {"available": True, "fileName": "ui-fixture.tar.gz", "sha256": "b" * 64}}
                        if "checkUpdate=1" in urlsplit(request.url).query:
                            data["update"] = {"status": "CURRENT" if upgraded or update_scenario == "manual" else "AVAILABLE",
                                              "currentSHA256": ("b" if upgraded else "a") * 64, "packageSHA256": "b" * 64}
                        route.fulfill(json={"data": data})
                    elif path == f"/hosts/{host['id']}/preparation/install":
                        updates.append({"path": path, "scenario": update_scenario, "encrypted": all(request.post_data_json.get(key) for key in ("challengeId", "clientPublicKey", "iv", "ciphertext"))})
                        update_attempts[update_scenario] = update_attempts.get(update_scenario, 0) + 1
                        assert updates[-1]["encrypted"] and "ui-only-not-a-server-password" not in request.post_data
                        if update_scenario == "sudo" and update_attempts[update_scenario] == 1:
                            update_job = {**update_job, "id": "sudo-challenge", "status": "FAILED", "phase": "INSTALLING_EXECUTOR", "errorCode": "PREPARATION_SUDO_REQUIRED"}
                        elif update_scenario == "upload-progress":
                            update_job = {**update_job, "id": "upload-confirmation", "status": "RUNNING", "phase": "UPLOADING_EXECUTOR", "errorCode": "",
                                          "result": {"transfer": {"sentBytes": 7549747, "totalBytes": 7549747, "startedAt": stamp, "updatedAt": stamp}}}
                        else:
                            upgraded = True
                            fixture_host["version"] = str(int(fixture_host["version"]) + 1)
                            update_job = {**update_job, "id": update_scenario + "-updated", "status": "SUCCEEDED", "phase": "VERIFYING_EXECUTOR", "errorCode": ""}
                        route.fulfill(json={"data": update_job})
                    elif path == f"/hosts/{host['id']}/software-install-plans":
                        data = request.post_data_json
                        preflights.append(data)
                        assert data["source"] == {"type": "OFFICIAL"}
                        assert set(data["config"]) == {"name", "runtimeMode", "bindAddress", "hostPort", "memoryMiB"}
                        definition = next(item for item in official if item["key"] == data["recipeKey"])
                        assert data["config"]["hostPort"] == definition["port"]
                        if not upgraded:
                            route.fulfill(status=409, json={"code": preflight_code, "msg": "接入组件未接受所选配方或安装参数，请升级执行器后重新预检", "timestamp": stamp, "traceId": "UI_FIXTURE_UPGRADE"})
                        else:
                            route.fulfill(json={"data": {"id": "ui-official-plan", "definition": definition, "config": data["config"], "source": data["source"],
                                                         "checks": [{"key": "UI_FIXTURE", "status": "PASSED", "message": "界面夹具预检，未执行目标命令"}],
                                                         "expiresAt": expiry, "paths": {"root": "/"}, "portMappings": [], "official": {"git": git_plan, "docker": docker_plan}.get(data["recipeKey"], official_plan)}})
                    elif path == "/software-install-plans/ui-official-plan/submit" and request.method == "POST":
                        assert request.post_data_json.get("idempotencyKey")
                        if installation_rejection:
                            route.fulfill(status=409, json={"code": installation_rejection, "msg": "安装计划已失效，请重新检查安装条件", "timestamp": stamp, "traceId": "UI_REJECTED_PLAN"})
                        else:
                            installation_submits.append(request.post_data_json)
                            route.fulfill(json={"data": {"jobId": installation_job["id"], "softwareInstanceId": "ui-installed-software"}})
                    elif path == "/jobs" and request.method == "GET":
                        if hold_log_list:
                            held_log_lists.append(route)
                        else:
                            history = ([installation_job] if installation_submits and not hide_install_job else []) + ([other_log_job] if show_other_log else [])
                            filters = parse_qs(urlsplit(request.url).query)
                            if filters.get("query"):
                                history = [job for job in history if filters["query"][0].lower() in (job.get("subjectName", "") + " " + host["name"]).lower()]
                            route.fulfill(json={"data": {"items": history, "nextCursor": ""}})
                    elif path == "/jobs/ui-other-log-job" and request.method == "GET":
                        route.fulfill(json={"data": other_log_job})
                    elif path == "/jobs/ui-other-log-job/logs" and request.method == "GET":
                        route.fulfill(json={"data": {"text": "另一个任务的输出", "limited": False}})
                    elif path == f"/jobs/{installation_job['id']}" and request.method == "GET":
                        if hold_detail_job:
                            held_detail_jobs.append(route)
                        else:
                            route.fulfill(json={"data": installation_job})
                    elif path == f"/jobs/{installation_job['id']}/logs" and request.method == "GET":
                        if final_log_failures:
                            final_log_failures -= 1
                            route.fulfill(status=503, json={"code": "TEMPORARY_LOG_FAILURE", "msg": "日志暂不可用"})
                            return
                        text = "界面夹具安装输出：" + ("安装完成" if installation_job["status"] == "SUCCEEDED" else f"软件包步骤 {len(installation_logs) + 1}")
                        installation_logs.append(text)
                        route.fulfill(json={"data": {"text": "\n".join(installation_logs), "limited": False}})
                    elif path == f"/hosts/{host['id']}/software-discovery":
                        route.fulfill(json={"data": {"hostId": host["id"], "hostVersion": fixture_host["version"], "candidates": [], "checks": [], "observedAt": stamp}})
                    elif request.method not in ("GET", "HEAD", "OPTIONS"):
                        blocked.append({"path": path, "method": request.method})
                        route.fulfill(status=409, json={"code": "TEST_TARGET_MUTATION_BLOCKED", "msg": "No target execution"})
                    else:
                        route.continue_()

                page.route("**/ops/api/v1/**", route_api)

                def close_log_dialog():
                    popup = page.get_by_role("dialog", name="执行日志", exact=True)
                    if popup.is_visible():
                        popup.get_by_role("button", name="Close this dialog", exact=True).click()
                        popup.wait_for(state="hidden")

                def software_page(catalog=True):
                    page.goto(base)
                    page.get_by_role("button", name="软件管理", exact=True).click()
                    if catalog:
                        page.get_by_role("button", name="软件安装", exact=True).click()
                        page.locator("article.plugin-card").first.wait_for()

                def open_install(key):
                    definition = next(item for item in definitions if item["key"] == key)
                    card = page.locator("article.plugin-card").filter(has=page.get_by_role("heading", name=definition["name"], exact=True))
                    card.get_by_role("button", name="安装", exact=True).click()
                    drawer = page.locator(".service-install-dialog.el-dialog:visible").last
                    drawer.wait_for()
                    return drawer

                def close(subject):
                    subject.get_by_role("button", name="取消", exact=True).click()
                    discard = page.get_by_role("dialog", name="关闭窗口", exact=True)
                    if discard.is_visible(): discard.get_by_role("button", name="放弃修改", exact=True).click()
                    subject.wait_for(state="hidden")

                def layout(subject, name):
                    for width in (1440, 390):
                        page.set_viewport_size({"width": width, "height": 1000})
                        page.wait_for_timeout(180)
                        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), (name, width, "document")
                        assert subject.evaluate("el => el.scrollWidth <= el.clientWidth + 1"), (name, width, "subject")
                        page.screenshot(path=str(output / f"{name}-{width}.png"), animations="disabled")
                        checks.append(f"layout-{name}-{width}")
                    page.set_viewport_size({"width": 1440, "height": 1000})

                software_page()
                expect(page.locator(".nav-group-label").get_by_text("软件中心", exact=True)).to_be_visible()
                expect(page.get_by_role("button", name="软件中心", exact=True)).to_have_count(0)
                for title in ("软件管理", "软件安装", "软件配置"):
                    expect(page.get_by_role("button", name=title, exact=True)).to_be_visible()
                expect(page.get_by_role("tablist", name="软件管理视图", exact=True)).to_have_count(0)
                page.get_by_role("button", name="软件配置", exact=True).click()
                page.locator(".recipe-workspace").wait_for()
                page.get_by_role("button", name="软件管理", exact=True).click()
                expect(page.locator("section.software-management")).to_have_attribute("aria-label", "软件管理")
                page.get_by_role("button", name="软件安装", exact=True).click()
                expect(page.locator("section.software-management")).to_have_attribute("aria-label", "软件安装")
                checks.append("software-center-three-independent-menu-entries-without-page-tabs")
                fixture_host["capabilities"]["docker"] = dict(cap)
                software_page()
                drawer = open_install("nginx")
                expect(drawer.get_by_role("button", name="Docker 安装", exact=True)).to_have_attribute("aria-pressed", "true")
                expect(drawer.get_by_role("combobox", name="安装来源", exact=True)).to_have_value("ONLINE")
                docker_revision = drawer.get_by_label("安装方案", exact=True).input_value()
                assert docker_revision == recipe_choice(next(item for item in definitions if item["key"] == "nginx" and "DOCKER" in item["runtimeModes"]))
                drawer.get_by_role("button", name="原生安装", exact=True).click()
                native_choice = drawer.get_by_label("安装方案", exact=True)
                if not native_choice.input_value():
                    expect(drawer.get_by_role("button", name="检查安装条件", exact=True)).to_be_disabled()
                else:
                    assert native_choice.input_value() in [recipe_choice(item) for item in [*definitions, *official] if item["key"] == "nginx" and "NATIVE" in item["runtimeModes"]]
                native_choice.select_option(recipe_choice(nginx))
                expect(native_choice).to_have_value(recipe_choice(nginx))
                assert recipe_choice(nginx) != docker_revision
                drawer.get_by_role("button", name="Docker 安装", exact=True).click()
                expect(drawer.get_by_label("安装方案", exact=True)).to_have_value(docker_revision)
                checks.append("fresh-docker-default-online-and-runtime-switch-keeps-matching-recipe-revisions")
                close(drawer)

                hold_host_check = True
                fixture_host["capabilities"]["expiresAt"] = (now - timedelta(minutes=1)).isoformat().replace("+00:00", "Z")
                software_page()
                before_pending = len(calls)
                drawer = open_install("nginx")
                expect(drawer.get_by_text("正在检测安装环境…", exact=True)).to_be_visible()
                drawer.get_by_role("button", name="原生安装", exact=True).click()
                assert len(pending_checks) == 1
                fixture_host["capabilities"]["expiresAt"] = expiry
                pending_checks.pop().fulfill(json={"data": fixture_host})
                expect(drawer.get_by_text("正在检测安装环境…", exact=True)).not_to_be_visible()
                expect(drawer.get_by_role("button", name="原生安装", exact=True)).to_have_attribute("aria-pressed", "true")
                assert not any(call["path"].endswith("/preparation/install") or call["path"].startswith("/installation-materials/") for call in calls[before_pending:])
                checks.append("expired-host-check-keeps-manual-native-choice-without-upload-or-component-update")
                close(drawer)
                hold_host_check = False
                fixture_host["capabilities"]["docker"] = {**cap, "status": "UNAVAILABLE", "code": "DOCKER_NOT_INSTALLED"}
                software_page()
                drawer = open_install("nginx")
                drawer.get_by_role("button", name="Docker 安装", exact=True).click()
                drawer.get_by_label("实例名称", exact=True).fill("安装引擎后保留的软件草稿")
                drawer.get_by_role("button", name="安装 Docker", exact=True).click()
                engine = drawer.locator(".el-card").filter(has=page.locator(".service-install"))
                expect(engine.get_by_role("combobox", name="安装来源", exact=True)).to_have_value("ONLINE")
                expect(engine.get_by_label("安装方案", exact=True)).to_have_value(recipe_choice(next(item for item in official if item["key"] == "docker")))
                expect(page.locator(".service-install-dialog.el-dialog:visible")).to_have_count(1)
                engine.get_by_role("button", name="返回软件安装", exact=True).click()
                expect(engine).not_to_be_visible()
                expect(drawer.get_by_label("实例名称", exact=True)).to_have_value("安装引擎后保留的软件草稿")
                expect(drawer.get_by_role("button", name="Docker 安装", exact=True)).to_have_attribute("aria-pressed", "true")
                assert not preflights and not updates and not installation_submits
                checks.append("missing-engine-opens-inline-and-returns-to-software-draft-without-installing")
                close(drawer)
                baseline = len(calls)
                drawer = open_install("nginx")
                expect(drawer.get_by_role("button", name="原生安装", exact=True)).to_have_attribute("aria-pressed", "true")
                expect(drawer.get_by_role("combobox", name="安装来源", exact=True)).to_have_value("ONLINE")
                expect(drawer.get_by_label("安装方案", exact=True)).to_have_value(recipe_choice(nginx))
                assert not drawer.get_by_label("可用安装包", exact=True).count()
                assert not any(call["query"].startswith("recipeKey=") or call["path"] == "/credentials" for call in calls[baseline:])
                drawer.get_by_label("实例名称", exact=True).fill("更新后保留的官方实例")
                layout(drawer, "official-default")
                preflight_before, update_before = len(preflights), len(updates)
                drawer.get_by_role("button", name="检查安装条件", exact=True).click()
                expect(drawer.get_by_text("预检完成", exact=True)).to_be_visible()
                assert len(updates) - update_before == 1 and len(preflights) - preflight_before == 2
                assert preflights[preflight_before]["expectedHostVersion"] == "1"
                assert preflights[-1]["expectedHostVersion"] == "2" and preflights[-1]["config"]["name"] == "更新后保留的官方实例"
                assert update_attempts["auto"] == 1
                details = drawer.get_by_label("官方安装详情", exact=True)
                expect(details.get_by_label("安装软件包", exact=True).get_by_text("nginx", exact=True)).to_be_visible()
                expect(details.locator(".package-dependencies").get_by_text("libfixture-one", exact=True)).not_to_be_visible()
                preview = drawer.get_by_label("安装命令", exact=True)
                expect(preview).to_have_count(1)
                planned_steps = [step for step in nginx["commands"]["steps"] if step["phase"] in ("INSTALL", "VERIFY")]
                expected_commands = [step["script"] for step in planned_steps if step.get("kind") != "SYSTEM"]
                expect(preview.locator(".command-step")).to_have_count(len(planned_steps))
                expect(preview.locator(".command-step pre")).to_have_text(expected_commands)
                expect(preview.locator(".system-step-details")).to_have_count(sum(step.get("kind") == "SYSTEM" for step in planned_steps))
                expect(preview.locator(".command-step[open]")).to_have_count(0)
                package_step = preview.locator(".command-step").filter(has=page.get_by_text("nginx", exact=True))
                package_step.locator("summary").click()
                expect(package_step.get_by_text("nginx", exact=True)).to_be_visible()
                package_step.locator("summary").click()
                expect(details.locator(".terminal")).to_have_count(0)
                details.locator(".package-dependencies summary").click()
                expect(details.locator(".package-dependencies").get_by_text("libfixture-one", exact=True)).to_be_visible()
                details.locator(".package-dependencies summary").click()
                layout(drawer, "official-preflight")
                assert not any(call["path"].endswith("/submit") for call in calls)
                checks.extend(["official-default-without-material-or-credential-request", "automatic-passwordless-update-keeps-name-preflights-new-host-version-no-auto-submit",
                               "official-plan-displays-main-package-version-origin-commands-system-paths-dependencies-collapsed"])
                close(drawer)

                # Five preparation steps must remain legible while the server
                # confirms a completed upload; byte transfer is not installation.
                update_scenario, upgraded = "upload-progress", False
                software_page()
                drawer = open_install("nginx")
                drawer.get_by_role("button", name="检查安装条件", exact=True).click()
                preparation_panel = drawer.locator(".host-preparation")
                expect(preparation_panel.get_by_role("heading", name="上传完成，正在确认", exact=True)).to_be_visible()
                expect(preparation_panel.locator(".state-tag")).to_have_text("确认中")
                expect(preparation_panel.locator(".phase-item")).to_have_count(5)
                expect(preparation_panel.locator(".phase-item").nth(2)).to_have_class("phase-item phase-pending")
                expect(preparation_panel.locator(".transfer-progress")).to_contain_text("100%")
                assert "KiB/s" not in preparation_panel.locator(".transfer-progress").inner_text()
                for width, height in ((1440, 1000), (900, 900), (390, 844), (1440, 480), (390, 480)):
                    page.set_viewport_size({"width": width, "height": height})
                    page.wait_for_timeout(180)
                    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
                    assert preparation_panel.evaluate("el => el.scrollWidth <= el.clientWidth + 1")
                    boxes = preparation_panel.locator(".phase-item").evaluate_all("els => els.map(el => {const r=el.getBoundingClientRect(); return {x:r.x,y:r.y,width:r.width,height:r.height}})")
                    axis = "y" if width >= 900 else "x"
                    assert max(box[axis] for box in boxes) - min(box[axis] for box in boxes) < 2, boxes
                    if width >= 900:
                        assert max(box["width"] for box in boxes) - min(box["width"] for box in boxes) < 2, boxes
                    else:
                        assert all(boxes[index+1]["y"] >= boxes[index]["y"] + boxes[index]["height"] - 1 for index in range(4)), boxes
                    footer = drawer.locator(".el-dialog__footer").bounding_box()
                    assert footer["y"] + footer["height"] <= height + 1, footer
                    page.screenshot(path=str(output / f"preparation-five-steps-{width}x{height}.png"), animations="disabled")
                    checks.append(f"five-preparation-steps-upload-confirmation-layout-{width}x{height}")
                update_job = {**update_job, "phase": "INSTALLING_EXECUTOR"}
                expect(preparation_panel.get_by_role("heading", name="安装接入组件", exact=True)).to_be_visible()
                expect(preparation_panel.locator(".phase-active")).to_contain_text("安装接入组件")
                expect(preparation_panel.locator(".transfer-progress")).to_have_count(0)
                upgraded = True
                fixture_host["version"] = str(int(fixture_host["version"]) + 1)
                update_job = {**update_job, "status": "SUCCEEDED", "phase": "VERIFYING_EXECUTOR"}
                expect(drawer.get_by_text("预检完成", exact=True)).to_be_visible()
                assert not installation_submits
                checks.append("upload-completion-waits-for-server-stage-and-never-submits-software")
                page.set_viewport_size({"width": 1440, "height": 1000})
                close(drawer)

                # Old executors reject the new Git recipe before official checks.
                # Continue only the component update, never the software submit.
                preflight_code, update_scenario, upgraded = "SERVICE_INSTALL_INPUT_INVALID", "git", False
                software_page()
                drawer = open_install("git")
                drawer.get_by_label("实例名称", exact=True).fill("Git 更新后保留的草稿")
                preflight_before, update_before = len(preflights), len(updates)
                drawer.get_by_role("button", name="检查安装条件", exact=True).click()
                expect(drawer.get_by_text("预检完成", exact=True)).to_be_visible()
                assert len(updates) - update_before == 1 and len(preflights) - preflight_before == 2
                assert preflights[-1]["recipeKey"] == "git" and preflights[-1]["config"]["name"] == "Git 更新后保留的草稿"
                assert not installation_submits
                layout(drawer, "git-compatible-preflight")
                checks.append("git-invalid-input-updates-known-component-once-and-rechecks-without-software-submit")
                close(drawer)
                preflight_code = "EXECUTOR_UPGRADE_REQUIRED"

                # An explicit sudo challenge pauses the same installation draft;
                # the password below is a browser-fixture value, never a server credential.
                update_scenario, upgraded = "sudo", False
                fixture_host["version"] = "3"
                update_job = {**update_job, "id": "sudo-old-connection", "status": "SUCCEEDED", "errorCode": ""}
                software_page()
                drawer = open_install("nginx")
                drawer.get_by_label("实例名称", exact=True).fill("sudo后保留的官方实例")
                preflight_before, update_before = len(preflights), len(updates)
                drawer.get_by_role("button", name="检查安装条件", exact=True).click()
                update = drawer.locator(".inline-preparation")
                password_input = update.get_by_label("ubuntu 的登录密码", exact=True)
                expect(password_input).to_be_enabled()
                assert len(updates) - update_before == 1 and len(preflights) - preflight_before == 1
                assert preflights[preflight_before]["expectedHostVersion"] == "3"
                layout(update, "inline-update-sudo-challenge")
                password_input.fill("ui-only-not-a-server-password")
                update.get_by_role("button", name="验证并继续", exact=True).click()
                update.wait_for(state="hidden")
                expect(drawer.get_by_text("预检完成", exact=True)).to_be_visible()
                assert len(updates) - update_before == 2 and len(preflights) - preflight_before == 2
                assert preflights[-1]["expectedHostVersion"] == "4" and preflights[-1]["config"]["name"] == "sudo后保留的官方实例"
                assert not any(call["path"].endswith("/submit") for call in calls)
                checks.append("automatic-sudo-challenge-pauses-and-encrypted-password-retry-keeps-draft-without-software-submit")
                close(drawer)

                # Invalid recipe input with a current component must not force
                # another upload. Recheck after correcting the selected recipe.
                update_scenario, upgraded = "manual", False
                preflight_code = "SERVICE_INSTALL_INPUT_INVALID"
                fixture_host["version"] = "5"
                update_job = {**update_job, "id": "manual-old-connection", "status": "SUCCEEDED", "errorCode": ""}
                software_page()
                drawer = open_install("nginx")
                drawer.get_by_label("实例名称", exact=True).fill("手工兜底保留的官方实例")
                preflight_before, update_before = len(preflights), len(updates)
                drawer.get_by_role("button", name="检查安装条件", exact=True).click()
                expect(drawer.locator(".install-diagnostics")).to_have_count(0)
                expect(drawer.get_by_role("alert")).to_contain_text("接入组件未接受所选配方")
                expect(drawer.get_by_text(preflight_code, exact=False)).not_to_be_visible()
                expect(drawer.get_by_text("UI_FIXTURE_UPGRADE", exact=False)).not_to_be_visible()
                layout(drawer, "compatibility-error-chinese")
                drawer.get_by_role("button", name="重新检查", exact=True).click()
                expect(drawer.get_by_role("button", name="检查安装条件", exact=True)).to_be_enabled()
                expect(drawer.locator(".inline-preparation")).to_have_count(0)
                assert len(updates) == update_before and len(preflights) - preflight_before == 2
                assert preflights[preflight_before]["expectedHostVersion"] == "5"
                upgraded = True
                drawer.get_by_role("button", name="检查安装条件", exact=True).click()
                expect(drawer.get_by_text("预检完成", exact=True)).to_be_visible()
                assert len(updates) == update_before and len(preflights) - preflight_before == 3
                assert preflights[-1]["expectedHostVersion"] == "5" and preflights[-1]["config"]["name"] == "手工兜底保留的官方实例"
                assert not any(call["path"].endswith("/submit") for call in calls)
                checks.append("current-component-invalid-input-rechecks-without-upload-and-preserves-draft")
                close(drawer)

                preflight_before = len(preflights)
                drawer = open_install("nginx")
                drawer.get_by_role("combobox", name="安装来源", exact=True).select_option("LIBRARY")
                offline_version = drawer.get_by_label("安装方案", exact=True)
                if not offline_version.input_value():
                    expect(drawer.get_by_role("button", name="检查安装条件", exact=True)).to_be_disabled()
                    offline_version.select_option(offline_version.locator('option[value]:not([value=""])').first.get_attribute("value"))
                expect(drawer.get_by_role("combobox", name="安装来源", exact=True)).to_have_value("LIBRARY")
                drawer.get_by_role("button", name="检查安装条件", exact=True).click()
                expect(drawer.get_by_role("alert")).to_contain_text("离线归档")
                expect(drawer.get_by_role("button", name="下载到平台", exact=True)).to_be_enabled()
                assert not any(call["path"] == "/installation-materials/official" for call in calls)
                layout(drawer, "offline-explicit")
                checks.append("offline-empty-library-blocks-and-online-download-remains-explicit-library-supplement")
                close(drawer)

                drawer = open_install("java")
                expect(drawer.get_by_role("combobox", name="安装来源", exact=True)).to_have_value("ONLINE")
                assert drawer.get_by_label("安装方案", exact=True).input_value() in [recipe_choice(item) for item in official if item["key"] == "java"]
                drawer.get_by_role("button", name="管理已有软件", exact=True).click()
                layout(drawer, "self-install-supported")
                drawer.get_by_role("button", name="检测并登记", exact=True).click()
                discovery = page.get_by_role("dialog", name="自动识别软件", exact=True)
                expect(discovery.get_by_label("识别主机", exact=True)).to_have_value(host["id"])
                discovery.get_by_role("button", name="开始识别", exact=True).click()
                expect(discovery.get_by_text("未发现受支持的软件", exact=True)).to_be_visible()
                discovery.get_by_role("button", name="Close this dialog", exact=True).click()
                assert len(preflights) == preflight_before
                checks.append("self-java-opens-existing-readonly-discovery-without-software-install-post")
                software_page()
                for key in ("maven", "codex", "claude"):
                    drawer = open_install(key)
                    expect(drawer.get_by_role("combobox", name="安装来源", exact=True)).to_have_value("ONLINE")
                    drawer.get_by_role("button", name="管理已有软件", exact=True).click()
                    if key == "maven":
                        assert drawer.get_by_role("button", name="检测并登记", exact=True).count() == 0
                        expect(drawer.get_by_text("此软件暂不支持自动工具登记", exact=False)).to_be_visible()
                    else:
                        expect(drawer.get_by_role("button", name="检测并登记", exact=True)).to_be_enabled()
                    assert drawer.locator(".self-install a").get_attribute("href").startswith("https://")
                    close(drawer)
                checks.append("maven-readonly-limit-and-codex-claude-detection-with-official-links")

                def owned(key, name, official_data):
                    definition = next(item for item in official if item["key"] == key)
                    tool = definition["kind"] == "TOOL"
                    return {"id": str(uuid.uuid4()), "name": name, "hostId": host["id"], "hostName": fixture_host["name"], "softwareType": key.upper(),
                            "runtimeKind": "TOOL" if tool else "SYSTEMD", "locator": official_data["paths"]["binary"] if tool else "nginx.service",
                            "managementMode": "MANAGED", "desiredState": "INSTALLED" if tool else "RUNNING", "actualState": "INSTALLED" if tool else "RUNNING",
                            "installationStatus": "READY", "version": "1", "freshness": "FRESH", "dataOwnership": "MANAGED", "lastJobId": "", "errorCode": "",
                            "latestAttempt": {"code": "READY", "at": stamp, "traceId": "obs-diagnostics-fixture"},
                            "installation": {"definition": definition, "official": official_data, "config": {"name": name, "runtimeMode": "NATIVE", "memoryMiB": 512,
                                                                                                       "hostPort": definition["port"], "bindAddress": "0.0.0.0" if not tool else ""}},
                            "installationPaths": {"root": "/", "data": official_data["paths"]["data"], "config": official_data["paths"]["config"], "logs": official_data["paths"]["logs"]},
                            "observation": {"state": "INSTALLED" if tool else "RUNNING", "version": "UI_FIXTURE", "identityHash": "a" * 64, "observedAt": stamp, "source": "UI_FIXTURE"},
                            "createdAt": stamp, "updatedAt": stamp}
                tool_data = deepcopy(official_plan)
                tool_data["paths"] = {"root": "/", "binary": "/usr/lib/jvm/java-17-openjdk-amd64/bin/java", "data": "", "config": "", "logs": "", "unit": "", "ownedPaths": []}
                rows[:] = [owned("nginx", "界面夹具 官方 Nginx", official_plan), owned("java", "界面夹具 官方 Java", tool_data)]
                (output / "ui-fixtures.json").write_text(json.dumps({"host": fixture_host, "officialPlan": official_plan, "instances": rows}, ensure_ascii=False, indent=2), encoding="utf8")
                software_page(False)
                page.get_by_role("searchbox", name="搜索软件或服务标识", exact=True).fill("官方")
                expect(page.locator(".software-count")).to_have_count(0)
                layout(page.locator(".software-management"), "software-search-spacing")
                for item in rows:
                    card = page.locator(".software-card").filter(has=page.get_by_role("heading", name=item["name"], exact=True))
                    card.get_by_role("button",name="卸载",exact=True).click()
                    direct_uninstall = page.get_by_role('dialog',name='卸载软件',exact=True)
                    expect(direct_uninstall.get_by_role('checkbox',name='同时删除此实例的数据',exact=True)).not_to_be_checked()
                    close(direct_uninstall)
                    card.get_by_role("button", name="详情", exact=True).click()
                    detail = page.get_by_role("dialog", name=item["name"], exact=True)
                    expect(detail.get_by_text(item["id"], exact=True)).not_to_be_visible()
                    expect(detail.get_by_text("obs-diagnostics-fixture", exact=True)).not_to_be_visible()
                    detail.get_by_role("tab", name="来源与归属", exact=True).click()
                    expect(detail.get_by_text(item["id"], exact=True)).not_to_be_visible()
                    detail.get_by_text("排障信息", exact=True).click()
                    expect(detail.get_by_text(item["id"], exact=True)).to_be_visible()
                    expect(detail.get_by_text("obs-diagnostics-fixture", exact=True)).to_be_visible()
                    expect(detail.get_by_text("错误码", exact=True)).to_have_count(0)
                    detail.get_by_role("tab", name="安装详情", exact=True).click()
                    assert not detail.get_by_role("button", name="调整配置", exact=True).count()
                    expect(detail.get_by_label("官方安装详情", exact=True).get_by_text(item["installation"]["official"]["paths"]["binary"], exact=True)).to_be_visible()
                    detail.get_by_role("button", name="卸载软件", exact=True).click()
                    uninstall = page.get_by_role("dialog", name="卸载软件", exact=True)
                    choice = uninstall.get_by_role("checkbox", name="同时删除此实例的数据", exact=True)
                    expect(choice).not_to_be_checked()
                    if item["softwareType"] == "NGINX":
                        scope = uninstall.get_by_label("卸载目录范围", exact=True)
                        assert scope.locator("code").all_text_contents() == paths["ownedPaths"]
                        choice.check()
                        expect(uninstall.get_by_role("button", name="卸载并删除数据", exact=True)).to_be_disabled()
                        uninstall.get_by_label("确认删除的实例名称", exact=True).fill(item["name"])
                        expect(uninstall.get_by_role("button", name="卸载并删除数据", exact=True)).to_be_enabled()
                    else:
                        expect(choice).to_be_disabled()
                        expect(uninstall.get_by_text("此官方安装没有可删除的专属数据目录", exact=False)).to_be_visible()
                    assert not uninstall.get_by_text("/", exact=True).count()
                    layout(uninstall, "official-uninstall-" + item["softwareType"].lower())
                    close(uninstall)
                    detail.get_by_role("button", name="Close this dialog", exact=True).click()
                    detail.wait_for(state="hidden")
                checks.append("official-details-show-real-system-paths-readonly-and-uninstall-only-owned-paths-never-root")

                # The submit and all subsequent target reads below are explicit
                # browser fixtures. The isolated controller never receives them.
                software_page()
                drawer = open_install("nginx")
                drawer.get_by_role("button", name="检查安装条件", exact=True).click()
                expect(drawer.get_by_text("预检完成", exact=True)).to_be_visible()
                installation_rejection = "INSTALL_PLAN_STALE"
                drawer.get_by_role("button", name="确认安装", exact=True).click()
                expect(drawer.get_by_role("button", name="检查安装条件", exact=True)).to_be_visible()
                expect(drawer.get_by_role("button", name="Close this dialog", exact=True)).to_be_enabled()
                assert not installation_submits
                installation_rejection = ""
                drawer.get_by_role("button", name="检查安装条件", exact=True).click()
                expect(drawer.get_by_text("预检完成", exact=True)).to_be_visible()
                checks.append("rejected-installation-plan-can-recheck-without-accepting-or-freezing-dialog")
                drawer.get_by_role("button", name="确认安装", exact=True).click()
                expect(drawer.get_by_label("执行输出", exact=True)).to_have_count(0)
                expect(drawer.get_by_text("任务详情", exact=True)).to_have_count(0)
                expect(drawer.get_by_text(installation_job["traceId"], exact=True)).not_to_be_visible()
                assert len(installation_submits) == 1
                rows[0]["installationJobId"] = installation_job["id"]
                rows[0]["installationStatus"] = "INSTALLING"
                count_before_resume = len(installation_submits)
                preflights_before_resume = len(preflights)
                assert not installation_logs, "installation status dialog must not fetch log output"
                drawer.get_by_role("button", name="查看日志", exact=True).click()
                drawer.wait_for(state="hidden")
                popup = page.get_by_role("dialog", name="执行日志", exact=True)
                expect(popup.get_by_label("执行过程输出", exact=True)).to_contain_text("软件包步骤")
                expect(page.get_by_role("heading", name="软件安装", exact=True)).to_be_visible()
                layout(popup, "inline-install-log-modal")
                close_log_dialog()
                page.get_by_role("button", name="日志记录", exact=True).click()
                log_page = page.locator('section[aria-label="执行记录"]')
                terminal = log_page.get_by_label("执行输出", exact=True)
                expect(terminal.get_by_label("执行过程输出", exact=True)).to_contain_text("软件包步骤")
                first_count = len(installation_logs)
                page.wait_for_timeout(3400)
                assert len(installation_logs) > first_count
                terminal.get_by_role("button", name="暂停刷新", exact=True).click()
                paused_count = len(installation_logs)
                page.wait_for_timeout(3400)
                assert len(installation_logs) == paused_count
                expect(terminal.get_by_text("已暂停", exact=True)).to_be_visible()
                terminal.get_by_role("button", name="继续刷新", exact=True).click()
                expect(terminal.get_by_label("执行过程输出", exact=True)).to_contain_text(f"软件包步骤 {paused_count + 1}")
                layout(log_page, "central-live-install-output")

                terminal.get_by_role("button", name="暂停刷新", exact=True).click()
                paused_output = terminal.get_by_label("执行过程输出", exact=True).inner_text()
                show_other_log = True
                log_page.get_by_role("button", name="刷新记录", exact=True).click()
                other_record = log_page.get_by_label("日志列表", exact=True).get_by_role("button", name="软件配置", exact=True)
                expect(other_record).to_be_visible()
                expect(terminal.get_by_text("已暂停", exact=True)).to_be_visible()
                assert terminal.get_by_label("执行过程输出", exact=True).inner_text() == paused_output
                checks.append("refreshing-log-list-preserves-paused-output")

                hold_log_list = True
                log_page.get_by_role("button", name="刷新记录", exact=True).click()
                expect(log_page.get_by_role("button", name="加载中…", exact=True)).to_be_visible()
                other_record.click()
                expect(terminal.get_by_label("执行过程输出", exact=True)).to_have_text("另一个任务的输出")
                hold_log_list = False
                for held in held_log_lists:
                    held.fulfill(json={"data": {"items": [installation_job], "nextCursor": ""}})
                held_log_lists.clear()
                expect(terminal.get_by_label("执行过程输出", exact=True)).to_have_text("另一个任务的输出")
                page.get_by_role("button", name="软件安装", exact=True).click()
                page.get_by_role("button", name="日志记录", exact=True).click()
                expect(terminal.get_by_label("执行过程输出", exact=True)).to_have_text("另一个任务的输出")
                checks.append("late-log-list-cannot-replace-user-selection-and-navigation-retains-selection")

                log_page.get_by_label("搜索日志", exact=True).fill("__NO_MATCHING_LOG__")
                expect(log_page.get_by_text("没有匹配的日志", exact=True)).to_be_visible()
                expect(terminal.get_by_label("执行过程输出", exact=True)).not_to_contain_text("另一个任务的输出")
                no_selection_reads = sum(call["path"] == "/jobs/ui-other-log-job/logs" for call in calls)
                page.wait_for_timeout(3400)
                assert sum(call["path"] == "/jobs/ui-other-log-job/logs" for call in calls) == no_selection_reads
                log_page.get_by_label("搜索日志", exact=True).fill("")
                expect(terminal.get_by_label("执行过程输出", exact=True)).to_contain_text("软件包步骤")
                show_other_log = False
                log_page.get_by_role("button", name="刷新记录", exact=True).click()
                checks.append("filtering-away-log-selection-clears-output-and-stops-old-polling")

                page.get_by_role("button", name="软件安装", exact=True).click()
                nginx_name = next(item["name"] for item in definitions if item["key"] == "nginx")
                progress_card = page.locator("article.plugin-card").filter(has=page.get_by_role("heading", name=nginx_name, exact=True))
                progress_card.get_by_role("button", name="查看进度", exact=True).click()
                page.get_by_role("menuitem").filter(has_text=nginx["version"]).click()
                drawer = page.locator(".service-install-dialog.el-dialog:visible").last
                expect(drawer.get_by_label("执行输出", exact=True)).to_have_count(0)
                expect(drawer.get_by_text("任务详情", exact=True)).to_have_count(0)
                assert not drawer.get_by_role("button", name="检查安装条件", exact=True).count()
                assert not drawer.get_by_role("button", name="原生安装", exact=True).count()
                assert len(installation_submits) == count_before_resume and len(preflights) == preflights_before_resume
                layout(drawer, "restored-install-status")
                installation_job.update(status="UNKNOWN", phase="TARGET_RESULT_UNKNOWN", targetAckPending=True)
                expect(drawer.get_by_text("结果待核对", exact=True)).to_be_visible(timeout=7000)
                drawer.get_by_role("button", name="Close this dialog", exact=True).click()
                drawer.wait_for(state="hidden")
                progress_card.get_by_role("button", name="查看进度", exact=True).click()
                page.get_by_role("menuitem").filter(has_text=nginx["version"]).click()
                drawer = page.locator(".service-install-dialog.el-dialog:visible").last
                expect(drawer.get_by_role("button", name="核对原作业", exact=True)).to_be_visible()
                assert len(installation_submits) == count_before_resume
                installation_job["targetAckPending"] = False
                checks.append("running-and-unknown-installation-resumes-status-without-resubmitting-and-keeps-logs-central")
                installation_job.update(status="SUCCEEDED", phase="SERVICE_INSTALLED")
                rows[0]["installationStatus"] = "READY"
                expect(drawer.get_by_text("软件已安装", exact=True)).to_be_visible(timeout=7000)
                expect(drawer.get_by_text("SERVICE_INSTALLED", exact=True)).not_to_be_visible()
                expect(drawer.get_by_text(installation_job["traceId"], exact=True)).not_to_be_visible()
                final_log_failures = 1
                drawer.get_by_role("button", name="查看日志", exact=True).click()
                drawer.wait_for(state="hidden")
                log_page = page.get_by_role("dialog", name="执行日志", exact=True)
                terminal = log_page.get_by_label("执行输出", exact=True)
                expect(log_page.get_by_role("status")).to_contain_text("日志读取未完成")
                expect(terminal.get_by_label("执行过程输出", exact=True)).to_contain_text("安装完成", timeout=10000)
                checks.append("terminal-job-retries-failed-final-output-before-stopping")
                expect(terminal.get_by_text("执行结束", exact=True)).to_be_visible()
                finished_count = len(installation_logs)
                finished_jobs = sum(call["path"] == "/jobs/ui-install-job" for call in calls)
                page.wait_for_timeout(3400)
                assert len(installation_logs) == finished_count
                assert sum(call["path"] == "/jobs/ui-install-job" for call in calls) == finished_jobs
                layout(log_page, "central-finished-install-output")
                final_log_failures = 3
                terminal.get_by_role("button", name="刷新输出", exact=True).click()
                expect(terminal.get_by_text("已暂停", exact=True)).to_be_visible(timeout=13000)
                failed_read_count = sum(call["path"] == "/jobs/ui-install-job/logs" for call in calls)
                page.wait_for_timeout(3400)
                assert sum(call["path"] == "/jobs/ui-install-job/logs" for call in calls) == failed_read_count
                final_log_failures = 0
                terminal.get_by_role("button", name="继续刷新", exact=True).click()
                expect(terminal.get_by_text("执行结束", exact=True)).to_be_visible()
                expect(terminal.get_by_label("执行过程输出", exact=True)).to_contain_text("安装完成")
                checks.append("three-output-failures-pause-polling-and-manual-retry-recovers")
                log_page.locator("details > summary").filter(has_text="诊断信息").click()
                expect(log_page.get_by_text("SERVICE_INSTALLED", exact=True)).to_be_visible()
                expect(log_page.get_by_text(installation_job["traceId"], exact=True)).to_be_visible()
                close_log_dialog()
                page.get_by_role("button", name="软件安装", exact=True).click()
                page.get_by_role("button", name="日志记录", exact=True).click()
                log_page = page.locator('section[aria-label="执行记录"]')
                terminal = log_page.get_by_label("执行输出", exact=True)
                expect(terminal.get_by_label("执行过程输出", exact=True)).to_contain_text("安装完成")
                checks.append("central-install-output-refresh-pause-resume-final-tail-stops-and-selection-survives-navigation")

                rows[0]["lastJobId"] = installation_job["id"]
                rows[0]["observation"]["message"] = "SERVICE_INSTALLED"
                installation_job.update(status="RUNNING", phase="OFFICIAL_PACKAGES_APPLY")
                software_page(False)
                page.locator(".software-card").filter(has=page.get_by_role("heading", name=rows[0]["name"], exact=True)).get_by_role("button", name="详情", exact=True).click()
                running_detail = page.get_by_role("dialog", name=rows[0]["name"], exact=True)
                running_detail.get_by_role("button", name="Close this dialog", exact=True).click()
                running_detail.wait_for(state="hidden")
                assert installation_job["status"] == "RUNNING"
                checks.append("read-only-detail-can-close-while-original-job-keeps-running")
                installation_job.update(status="SUCCEEDED", phase="SERVICE_INSTALLED")

                hold_detail_job = True
                page.locator(".software-card").filter(has=page.get_by_role("heading", name=rows[0]["name"], exact=True)).get_by_role("button", name="详情", exact=True).click()
                delayed_detail = page.get_by_role("dialog", name=rows[0]["name"], exact=True)
                expect(delayed_detail).to_be_visible()
                expect(delayed_detail.get_by_role("button", name="卸载软件", exact=True)).to_be_disabled()
                expect(delayed_detail.get_by_role("button", name="Close this dialog", exact=True)).to_be_visible()
                delayed_detail.get_by_role("button", name="Close this dialog", exact=True).click()
                delayed_detail.wait_for(state="hidden")
                page.get_by_role("button", name="运行总览", exact=True).click()
                hold_detail_job = False
                for held in held_detail_jobs:
                    held.fulfill(status=503, json={"code": "LATE_DETAIL_FAILURE", "msg": "旧页面请求失败测试"})
                held_detail_jobs.clear()
                page.wait_for_timeout(300)
                expect(page.get_by_text("旧页面请求失败测试", exact=False)).to_have_count(0)
                checks.append("late-detail-failure-does-not-report-on-another-page")
                software_page(False)
                page.locator(".software-card").filter(has=page.get_by_role("heading", name=rows[0]["name"], exact=True)).get_by_role("button", name="详情", exact=True).click()
                detail = page.get_by_role("dialog", name=rows[0]["name"], exact=True)
                expect(detail.get_by_text("SERVICE_INSTALLED", exact=True)).not_to_be_visible()
                detail.get_by_role("tab", name="执行记录", exact=True).click()
                expect(detail.get_by_text("软件已安装", exact=True)).to_be_visible()
                expect(detail.locator(".software-job code").filter(has_text="SERVICE_INSTALLED")).not_to_be_visible()
                layout(detail, "software-job-clean-status")
                expect(detail.get_by_text(installation_job["traceId"], exact=True)).not_to_be_visible()
                expect(detail.locator(".software-job details")).to_have_count(0)
                # A direct software-task link must remain readable even when
                # the task is older than /jobs' latest window or is a lifecycle receipt.
                hide_install_job = True
                installation_job["kind"] = "SOFTWARE_START"
                detail.get_by_role("button", name="查看日志", exact=True).click()
                detail.wait_for(state="hidden")
                popup = page.get_by_role("dialog", name="执行日志", exact=True)
                terminal = popup.get_by_label("执行输出", exact=True)
                expect(terminal.get_by_label("执行过程输出", exact=True)).to_contain_text("安装完成")
                expect(page.get_by_role("heading", name="软件管理", exact=True)).to_be_visible()
                hide_install_job = False
                installation_job["kind"] = "SOFTWARE_INSTALL"
                checks.append("older-lifecycle-receipt-opens-directly-without-latest-list-membership")
                software_page(False)
                page.locator(".software-card").filter(has=page.get_by_role("heading", name=rows[0]["name"], exact=True)).get_by_role("button", name="详情", exact=True).click()
                detail = page.get_by_role("dialog", name=rows[0]["name"], exact=True)
                detail.get_by_role("button", name="运行日志", exact=True).click()
                detail.wait_for(state="hidden")
                runtime_logs = page.get_by_role("dialog", name="执行日志", exact=True)
                expect(runtime_logs.get_by_label("运行日志输出", exact=True)).to_contain_text("界面夹具运行日志")
                expect(page.get_by_role("heading", name="软件管理", exact=True)).to_be_visible()
                close_log_dialog()
                page.get_by_role("button", name="日志记录", exact=True).click()
                for title in ("执行记录", "运行日志", "操作记录"):
                    expect(page.get_by_role("tab", name=title, exact=True)).to_be_visible()
                checks.append("software-detail-job-and-runtime-log-links-use-central-log-records-without-inline-diagnostics")
                rows.append(owned("git", "界面夹具 已安装 Git", git_plan))
                rows[-1]["installationJobId"] = installation_job["id"]
                software_page()
                git_name = next(item["name"] for item in definitions if item["key"] == "git")
                git_card = page.locator("article.plugin-card").filter(has=page.get_by_role("heading", name=git_name, exact=True))
                git_card.get_by_role("button", name="安装日志", exact=True).click()
                terminal = page.get_by_role("dialog", name="执行日志", exact=True).get_by_label("执行输出", exact=True)
                expect(terminal.get_by_label("执行过程输出", exact=True)).to_contain_text("安装完成")
                assert len(installation_submits) == count_before_resume and len(preflights) == preflights_before_resume
                software_page(False)
                page.locator(".software-card").filter(has=page.get_by_role("heading", name=rows[-1]["name"], exact=True)).get_by_role("button", name="安装日志", exact=True).click()
                terminal = page.get_by_role("dialog", name="执行日志", exact=True).get_by_label("执行输出", exact=True)
                expect(terminal.get_by_label("执行过程输出", exact=True)).to_contain_text("安装完成")
                checks.append("installed-git-opens-central-logs-from-catalog-and-software-list-without-reinstall")

                # External existing packages remain unowned; only their presentation changes.
                rows.pop()
                upgraded = False
                preflight_code = "OFFICIAL_PACKAGE_ALREADY_INSTALLED"
                software_page()
                drawer = open_install("git")
                updates_before_existing = len(updates)
                drawer.get_by_role("button", name="检查安装条件", exact=True).click()
                notice = drawer.get_by_role("status").filter(has_text="服务器已安装此软件，无需重复安装。")
                expect(notice).to_be_visible()
                expect(notice).not_to_have_class("install-error")
                expect(notice.get_by_role("button", name="检测并登记", exact=True)).to_be_visible()
                expect(notice.get_by_text("OFFICIAL_PACKAGE_ALREADY_INSTALLED", exact=False)).not_to_be_visible()
                assert len(updates) == updates_before_existing and len(installation_submits) == count_before_resume
                layout(drawer, "existing-package-notice")
                close(drawer)
                upgraded = True
                checks.append("existing-external-package-neutral-notice-retains-diagnostics-without-adoption-or-install")
                # Git UI uses explicit API fixtures; no key or repository write is forwarded.
                git_active = True
                software_page()
                git_name = next(item["name"] for item in definitions if item["key"] == "git")
                git_card = page.locator("article.plugin-card").filter(has=page.get_by_role("heading", name=git_name, exact=True))
                git_card.get_by_role("button", name="账号配置", exact=True).click()
                git_drawer = page.get_by_role("dialog", name="Git 账号配置", exact=True)
                git_drawer.get_by_role("button", name="新增账号", exact=True).click()
                git_drawer.get_by_label('Git 认证方式',exact=True).select_option('SSH')
                git_drawer.get_by_label("Git 账号名称", exact=True).fill("公司 Git 账号")
                git_drawer.get_by_label("Git 服务器地址", exact=True).fill("git.example.test")
                expect(git_drawer.get_by_label("仓库目录", exact=True)).to_have_count(0)
                git_drawer.get_by_label("目标系统账号", exact=True).fill('flycode')
                git_drawer.get_by_role("button", name="生成密钥", exact=True).click()
                expect(git_drawer.get_by_label("复制到 Git 服务的账号公钥", exact=True)).to_have_value('ssh-ed25519 AAAA_UI_ACCOUNT_KEY')
                expect(git_drawer.get_by_text('自建 GitLab：', exact=False)).to_be_visible()
                assert not git_profiles
                layout(git_drawer, "git-account-without-repository")
                git_drawer.get_by_label('Git 服务器地址',exact=True).fill('github.com')
                expect(git_drawer.get_by_text('Authentication Key',exact=True)).to_be_visible()
                expect(git_drawer.get_by_text('复制下方完整公钥',exact=True)).to_be_visible()
                layout(git_drawer,'github-ssh-key-fields')
                git_drawer.get_by_label('Git 服务器地址',exact=True).fill('git.example.test')
                git_drawer.locator('summary').filter(has_text='连接校验').click()
                git_drawer.get_by_role('button',name='从目标服务器读取',exact=True).click()
                expect(git_drawer.get_by_role('alert')).to_contain_text('没有读取到 Git 服务主机公钥')
                git_drawer.get_by_role("button", name="保存账号", exact=True).click()
                expect(git_drawer.get_by_text("公司 Git 账号", exact=True)).to_be_visible()
                assert not git_submits and not git_checks
                checks.append("git-target-account-save-without-repository-author-or-host-key-and-no-repository-mutation")
                rows.append(owned("git", "界面夹具 Git 连续输入", git_plan))
                software_page(False)
                page.locator(".software-card").filter(has=page.get_by_role("heading", name=rows[-1]["name"], exact=True)).get_by_role("button", name="详情", exact=True).click()
                detail = page.get_by_role("dialog", name=rows[-1]["name"], exact=True)
                detail.get_by_role("tab", name="Git 账号", exact=True).click()
                detail.get_by_role("button", name="新增账号", exact=True).click()
                field = detail.get_by_label("Git 账号名称", exact=True)
                field.press_sequentially("company-", delay=180)
                host_reads = sum(call["path"] == "/hosts" for call in calls)
                page.wait_for_timeout(5600)
                assert sum(call["path"] == "/hosts" for call in calls) > host_reads
                expect(field).to_be_focused()
                page.keyboard.type("account-input", delay=120)
                expect(field).to_have_value("company-account-input")
                checks.append("Git-form-retains-focus-and-draft-through-parent-host-refresh-without-refocusing")
                expect(detail.get_by_label('Git 认证方式',exact=True)).to_have_value('HTTPS')
                expect(detail.get_by_text('兼容旧配置',exact=False)).to_have_count(0)
                detail.get_by_label('Git 服务器地址',exact=True).fill('github.com')
                detail.get_by_label('目标系统账号',exact=True).fill('flycode')
                detail.get_by_label('Git 登录用户名',exact=True).fill('flycodeu')
                detail.get_by_label('Git 访问 Token',exact=True).fill('UI_FIXTURE_TOKEN_ONLY')
                layout(detail, 'git-https-default')
                detail.get_by_role('button',name='保存账号',exact=True).click()
                account_row=detail.locator('.git-row').filter(has_text='company-account-input')
                account_row.get_by_role('button',name='配置到服务器',exact=True).click()
                expect(account_row.get_by_text('flycode · 已配置到服务器',exact=True)).to_be_visible()
                expect(account_row.get_by_role('button',name='配置到服务器',exact=True)).to_have_count(0)
                account_row.get_by_role('button',name='使用账号',exact=True).click()
                account_row.get_by_label('仓库地址',exact=True).fill('https://github.com/team/project.git')
                expect(account_row.locator('.git-usage code')).to_have_text('git clone -- https://flycodeu@github.com/team/project.git')
                assert not git_submits and not git_checks
                saved_keys = api("/credentials")
                assert len(saved_keys) == 1 and saved_keys[0]["kind"] == "TOKEN"
                assert "secret" not in saved_keys[0] and len(inline_credentials) == 1 and len(git_profiles) == 2
                assert 'UI_FIXTURE_TOKEN_ONLY' not in detail.inner_text()
                checks.append("HTTPS-token-saved-encrypted-and-explicit-target-config-without-secret-echo-or-repeat-prompt")

                # A failed saved receipt becomes an editable draft only after
                # target acknowledgement, with current published commands.
                git_active, upgraded = False, True
                failed_record = owned("nginx", "失败后保留的 Nginx", official_plan)
                failed_record["installation"]["definition"] = {**nginx, "revision": "ui-old-failed-revision"}
                failed_record.update(installationStatus="UNKNOWN", installationJobId="ui-failed-install")
                rows[:] = [failed_record]
                installation_job.update(id="ui-failed-install", status="FAILED", phase="SOFTWARE_REJECTED", targetAckPending=True)
                before_retry = len(preflights), len(installation_submits)
                software_page()
                retry_card = page.locator("article.plugin-card").filter(has=page.get_by_role("heading", name=nginx_name, exact=True))
                retry_card.get_by_role("button", name="查看进度", exact=True).click()
                page.get_by_role("menuitem").filter(has_text=nginx["version"]).click()
                drawer = page.locator(".service-install-dialog.el-dialog:visible").last
                expect(drawer.get_by_role("button", name="核对原作业", exact=True)).to_be_visible()
                expect(drawer.get_by_role("button", name="重新安装", exact=True)).to_have_count(0)
                installation_job["targetAckPending"] = False
                retry_button = drawer.get_by_role("button", name="重新安装", exact=True)
                expect(retry_button).to_be_visible(timeout=7000)
                retry_button.click()
                expect(drawer.get_by_label("实例名称", exact=True)).to_have_value(failed_record["name"])
                expect(drawer.get_by_label("安装方案", exact=True)).to_have_value(recipe_choice(nginx))
                assert before_retry == (len(preflights), len(installation_submits))
                drawer.get_by_role("button", name="检查安装条件", exact=True).click()
                expect(drawer.get_by_text("预检完成", exact=True)).to_be_visible()
                assert preflights[-1]["recipeRevision"] == nginx["revision"]
                assert preflights[-1]["config"]["hostPort"] == nginx["port"] and preflights[-1]["config"]["name"] == failed_record["name"]
                installation_job.update(id="ui-retried-install", status="RUNNING", phase="OFFICIAL_PACKAGES_APPLY")
                drawer.get_by_role("button", name="确认安装", exact=True).click()
                expect(drawer.get_by_label("执行结果", exact=True)).to_be_visible()
                assert len(installation_submits) == before_retry[1] + 1
                assert failed_record["installationJobId"] == "ui-failed-install"
                checks.append("acknowledged-failed-installation-keeps-name-port-and-uses-published-revision-new-plan-new-job")
                drawer.get_by_role("button", name="Close this dialog", exact=True).click()
                drawer.wait_for(state="hidden")

                # Reuse the isolated installation fixtures for an inline Engine task.
                # A distinct job ID proves navigation selects its output, not the outer draft.
                rows.clear()
                git_active = False
                installation_job.update(id="ui-engine-job", status="RUNNING", phase="OFFICIAL_PACKAGES_APPLY", traceId="UI_ENGINE_INSTALL_OUTPUT")
                installation_logs.clear()
                software_page()
                drawer = open_install("nginx")
                drawer.get_by_role("button", name="Docker 安装", exact=True).click()
                drawer.get_by_label("实例名称", exact=True).fill("查看引擎日志前保留的草稿")
                drawer.get_by_label("宿主机访问端口", exact=True).fill("18088")
                drawer.get_by_role("button", name="安装 Docker", exact=True).click()
                engine = drawer.locator(".el-card").filter(has=page.locator(".service-install"))
                engine.get_by_role("button", name="检查安装条件", exact=True).click()
                expect(engine.get_by_text("预检完成", exact=True)).to_be_visible()
                assert preflights[-1]["recipeKey"] == "docker"
                submits_before_engine = len(installation_submits)
                engine.get_by_role("button", name="确认安装", exact=True).click()
                expect(engine.get_by_role("button", name="查看日志", exact=True)).to_be_enabled()
                assert len(installation_submits) == submits_before_engine + 1
                navigation_counts = (len(preflights), len(installation_submits), sum(call["path"].endswith("/checks") for call in calls))
                engine.get_by_role("button", name="查看日志", exact=True).click()
                discard = page.get_by_role("dialog", name="关闭窗口", exact=True)
                expect(discard).to_be_visible()
                discard.get_by_role("button", name="继续编辑", exact=True).click()
                expect(engine.get_by_role("button", name="查看日志", exact=True)).to_be_visible()
                expect(drawer.get_by_label("实例名称", exact=True).first).to_have_value("查看引擎日志前保留的草稿")
                expect(drawer.get_by_label("宿主机访问端口", exact=True).first).to_have_value("18088")
                assert not any(call["path"] == "/jobs/ui-engine-job/logs" for call in calls)
                checks.append("inline-engine-log-cancel-retains-outer-name-port-and-engine-progress")
                engine.get_by_role("button", name="查看日志", exact=True).click()
                expect(discard).to_be_visible()
                discard.get_by_role("button", name="放弃修改", exact=True).click()
                drawer.wait_for(state="hidden")
                log_page = page.get_by_role("dialog", name="执行日志", exact=True)
                expect(log_page.get_by_label("执行过程输出", exact=True)).to_contain_text("软件包步骤")
                expect(page.get_by_role("heading", name="软件安装", exact=True)).to_be_visible()
                assert any(call["path"] == "/jobs/ui-engine-job/logs" for call in calls)
                page.wait_for_timeout(500)
                assert navigation_counts == (len(preflights), len(installation_submits), sum(call["path"].endswith("/checks") for call in calls))
                assert installation_job["status"] == "RUNNING"
                checks.append("inline-engine-log-confirm-opens-engine-job-without-new-check-preflight-or-submit")
                layout(log_page, "inline-engine-central-log")
                assert not api("/software-instances") and not api("/installation-materials")
                result["remoteInstalls"] = sum(job["kind"] == "SOFTWARE_INSTALL" for job in api("/jobs?limit=100")["items"])
                assert result["remoteInstalls"] == 0
                assert not blocked and not errors, (blocked, errors)
                checks.append("real-controller-remains-without-software-materials-or-target-execution-only-disposable-inline-credential")
                (output / "requests.json").write_text(json.dumps({"calls": calls, "preflights": preflights, "updates": updates}, ensure_ascii=False, indent=2), encoding="utf8")
                result["status"] = "PASS"
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
