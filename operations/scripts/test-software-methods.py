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
                assert len(definitions) == 17 and len(official) == 9
                assert not api("/installation-materials") and not api("/software-instances")
                checks.append("real-catalog-17-offline-and-9-official-definitions-empty-library")
                now = datetime.now(timezone.utc)
                stamp = now.isoformat().replace("+00:00", "Z")
                expiry = (now + timedelta(hours=1)).isoformat().replace("+00:00", "Z")
                cap = {"status": "AVAILABLE", "code": "UI_FIXTURE", "message": "仅界面夹具", "observedAt": stamp, "source": "UI_FIXTURE"}
                fixture_host = {**host, "name": "界面夹具 Ubuntu 主机", "kind": "SSH", "address": "192.0.2.26", "version": "1",
                                "installRoot": "/opt/streamfusion-ops", "observedAt": stamp, "maintenance": False,
                                "capabilities": {"reachability": cap, "readOnly": cap, "mutation": cap, "docker": {**cap, "status": "UNAVAILABLE"},
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
                rows, preflights, updates = [], [], []
                update_job = {"id": "old-connection", "status": "SUCCEEDED", "phase": "VERIFYING_EXECUTOR", "errorCode": "", "updatedAt": stamp}
                upgraded = False

                def route_api(route):
                    nonlocal upgraded, update_job
                    request = route.request
                    path = urlsplit(request.url).path.removeprefix("/ops/api/v1")
                    calls.append({"path": path, "query": urlsplit(request.url).query, "method": request.method})
                    if path.startswith("/auth/"):
                        route.continue_()
                    elif path == "/hosts" and request.method == "GET":
                        route.fulfill(json={"data": [fixture_host]})
                    elif path == "/software-instances" and request.method == "GET":
                        route.fulfill(json={"data": rows})
                    elif path == f"/hosts/{host['id']}/checks":
                        route.fulfill(json={"data": fixture_host})
                    elif path == f"/hosts/{host['id']}/preparation":
                        route.fulfill(json={"data": {"hostId": host["id"], "expectedHostVersion": fixture_host["version"], "account": "root",
                                                      "installRoot": fixture_host["installRoot"], "remoteJob": update_job,
                                                      "package": {"available": True, "fileName": "ui-fixture.tar.gz"}}})
                    elif path == f"/hosts/{host['id']}/preparation/install":
                        updates.append({"path": path, "encrypted": bool(request.post_data_json.get("envelope"))})
                        upgraded = True
                        fixture_host["version"] = "2"
                        update_job = {**update_job, "id": "explicit-update"}
                        route.fulfill(json={"data": update_job})
                    elif path == f"/hosts/{host['id']}/software-install-plans":
                        data = request.post_data_json
                        preflights.append(data)
                        assert data["source"] == {"type": "OFFICIAL"}
                        assert set(data["config"]) == {"name", "runtimeMode", "bindAddress", "hostPort", "memoryMiB"}
                        assert data["config"]["bindAddress"] == "0.0.0.0" and data["config"]["hostPort"] == 80
                        if not upgraded:
                            route.fulfill(status=409, json={"code": "EXECUTOR_UPGRADE_REQUIRED", "msg": "当前接入组件不支持官方命令安装", "timestamp": stamp, "traceId": "UI_FIXTURE_UPGRADE"})
                        else:
                            route.fulfill(json={"data": {"id": "ui-official-plan", "definition": nginx, "config": data["config"], "source": data["source"],
                                                         "checks": [{"key": "UI_FIXTURE", "status": "PASSED", "message": "界面夹具预检，未执行目标命令"}],
                                                         "expiresAt": expiry, "paths": {"root": "/"}, "portMappings": [], "official": official_plan}})
                    elif path == f"/hosts/{host['id']}/software-discovery":
                        route.fulfill(json={"data": {"hostId": host["id"], "hostVersion": fixture_host["version"], "candidates": [], "checks": [], "observedAt": stamp}})
                    elif request.method not in ("GET", "HEAD", "OPTIONS"):
                        blocked.append({"path": path, "method": request.method})
                        route.fulfill(status=409, json={"code": "TEST_TARGET_MUTATION_BLOCKED", "msg": "No target execution"})
                    else:
                        route.continue_()

                page.route("**/ops/api/v1/**", route_api)

                def software_page(catalog=True):
                    page.goto(base)
                    page.get_by_role("button", name="软件管理", exact=True).click()
                    if catalog:
                        page.get_by_role("tab", name="软件目录", exact=True).click()
                        page.locator("article.plugin-card").first.wait_for()

                def open_install(key):
                    definition = next(item for item in definitions if item["key"] == key)
                    card = page.locator("article.plugin-card").filter(has=page.get_by_role("heading", name=definition["name"], exact=True))
                    card.get_by_role("button", name="安装", exact=True).click()
                    drawer = page.locator(".el-drawer:visible").last
                    drawer.wait_for()
                    return drawer

                def close(subject):
                    subject.get_by_role("button", name="取消", exact=True).click()
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
                baseline = len(calls)
                drawer = open_install("nginx")
                expect(drawer.get_by_role("button", name="官方安装", exact=True)).to_have_attribute("aria-pressed", "true")
                assert not drawer.get_by_label("可用安装包", exact=True).count()
                assert not any(call["query"].startswith("recipeKey=") or call["path"] == "/credentials" for call in calls[baseline:])
                drawer.get_by_label("实例名称", exact=True).fill("更新后保留的官方实例")
                layout(drawer, "official-default")
                drawer.get_by_role("button", name="检查官方安装条件", exact=True).click()
                expect(drawer.get_by_role("button", name="更新接入组件", exact=True)).to_be_visible()
                assert not updates
                drawer.get_by_role("button", name="更新接入组件", exact=True).click()
                update = page.get_by_role("dialog", name="更新接入组件", exact=True)
                expect(update.get_by_role("button", name="确认更新接入组件", exact=True)).to_be_enabled()
                assert not updates
                layout(update, "inline-update-confirmation")
                update.get_by_role("button", name="确认更新接入组件", exact=True).click()
                update.wait_for(state="hidden")
                expect(drawer.get_by_text("预检完成", exact=True)).to_be_visible()
                assert len(updates) == 1 and len(preflights) == 2
                assert preflights[-1]["expectedHostVersion"] == "2" and preflights[-1]["config"]["name"] == "更新后保留的官方实例"
                details = drawer.get_by_label("官方安装详情", exact=True)
                expect(details.get_by_text("nginx", exact=True)).to_be_visible()
                expect(details.get_by_text("libfixture-one", exact=True)).not_to_be_visible()
                expect(details.locator("pre").filter(has_text="/usr/bin/apt-get install")).to_be_visible()
                details.get_by_text("查看 2 项依赖", exact=True).click()
                expect(details.get_by_text("libfixture-one", exact=True)).to_be_visible()
                details.get_by_text("查看 2 项依赖", exact=True).click()
                layout(drawer, "official-preflight")
                assert not any(call["path"].endswith("/submit") for call in calls)
                checks.extend(["official-default-without-material-or-credential-request", "explicit-inline-update-keeps-name-repreflights-new-host-version-no-auto-submit",
                               "official-plan-displays-main-package-version-origin-commands-system-paths-dependencies-collapsed"])
                close(drawer)

                drawer = open_install("nginx")
                drawer.get_by_role("button", name="Ops 离线安装", exact=True).click()
                drawer.get_by_role("radio", name="原生 / systemd", exact=False).check()
                expect(drawer.get_by_role("radio", name="本地离线材料库 · 默认", exact=False)).to_be_checked()
                drawer.get_by_role("button", name="下一步", exact=True).click()
                expect(drawer.get_by_role("alert")).to_contain_text("离线归档")
                drawer.get_by_role("radio", name="补充材料到离线库", exact=False).check()
                expect(drawer.get_by_role("button", name="下载材料到离线库", exact=True)).to_be_enabled()
                assert not any(call["path"] == "/installation-materials/official" for call in calls)
                layout(drawer, "offline-explicit")
                checks.append("offline-empty-library-blocks-and-online-download-remains-explicit-library-supplement")
                close(drawer)

                drawer = open_install("java")
                expect(drawer.get_by_label("官方配方", exact=True)).not_to_have_value("")
                drawer.get_by_role("button", name="自行安装", exact=True).click()
                layout(drawer, "self-install-supported")
                drawer.get_by_role("button", name="检测并登记", exact=True).click()
                discovery = page.get_by_role("dialog", name="自动识别软件", exact=True)
                expect(discovery.get_by_label("识别主机", exact=True)).to_have_value(host["id"])
                discovery.get_by_role("button", name="开始识别", exact=True).click()
                expect(discovery.get_by_text("未发现受支持的软件", exact=True)).to_be_visible()
                discovery.get_by_role("button", name="Close this dialog", exact=True).click()
                assert len(preflights) == 2
                checks.append("self-java-opens-existing-readonly-discovery-without-software-install-post")
                software_page()
                for key in ("maven", "codex", "claude"):
                    drawer = open_install(key)
                    if key != "maven":
                        expect(drawer.get_by_role("button", name="检查官方安装条件", exact=True)).to_be_disabled()
                    drawer.get_by_role("button", name="自行安装", exact=True).click()
                    assert drawer.get_by_role("button", name="检测并登记", exact=True).count() == 0
                    expect(drawer.get_by_text("此软件暂不支持自动工具登记", exact=False)).to_be_visible()
                    assert drawer.locator(".self-install a").get_attribute("href").startswith("https://")
                    close(drawer)
                checks.append("maven-codex-claude-self-detection-limit-and-official-links-are-explicit")

                def owned(key, name, official_data):
                    definition = next(item for item in official if item["key"] == key)
                    tool = definition["kind"] == "TOOL"
                    return {"id": str(uuid.uuid4()), "name": name, "hostId": host["id"], "hostName": fixture_host["name"], "softwareType": key.upper(),
                            "runtimeKind": "TOOL" if tool else "SYSTEMD", "locator": official_data["paths"]["binary"] if tool else "nginx.service",
                            "managementMode": "MANAGED", "desiredState": "INSTALLED" if tool else "RUNNING", "actualState": "INSTALLED" if tool else "RUNNING",
                            "installationStatus": "READY", "version": "1", "freshness": "FRESH", "dataOwnership": "MANAGED", "lastJobId": "", "errorCode": "",
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
                for item in rows:
                    card = page.locator(".software-card").filter(has=page.get_by_role("heading", name=item["name"], exact=True))
                    card.get_by_role("button", name="详情", exact=True).click()
                    detail = page.get_by_role("dialog", name=item["name"], exact=True)
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
                checks.append("official-details-show-real-system-paths-readonly-and-uninstall-only-owned-paths-never-root")
                assert not api("/software-instances") and not api("/installation-materials")
                assert not blocked and not errors, (blocked, errors)
                checks.append("real-controller-remains-without-software-materials-or-target-execution")
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
