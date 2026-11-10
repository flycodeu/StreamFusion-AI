"""Isolated annotation UI acceptance; target hosts and app pages are fixtures."""
import argparse
import copy
import json
from pathlib import Path
import secrets
import socket
import subprocess
import time
import traceback
from urllib.parse import urlsplit
import urllib.request

from playwright.sync_api import expect, sync_playwright
from software_configuration_ui_fixture import check_configuration_ui, check_stock_service_configuration_ui


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--browser-executable", required=True)
    args = parser.parse_args()
    binary, output = str(Path(args.binary).resolve()), Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=False)
    state, library = output / "state", output / "empty-library"
    library.mkdir()
    (library / "library.json").write_text('{"schemaVersion":1,"materials":[]}', encoding="utf8")
    password, username = "Annotation-" + secrets.token_hex(16), "annotation_" + secrets.token_hex(5)
    secret = output / ".password.secret"
    flags = getattr(subprocess, "CREATE_NO_WINDOW", 0)
    process = browser = page = None
    checks, errors, blocked, preflights, registrations = [], [], [], [], []
    result = {"status": "RUNNING", "checks": checks, "pageErrors": errors, "blockedRequests": blocked, "remoteInstalls": 0, "backend": "real isolated controller, auth and annotation catalog; host, software and embedded app browser responses are fixtures"}
    try:
        secret.write_text(password, encoding="utf8")
        try:
            initialized = subprocess.run([binary, "init-admin", "--state", str(state), "--username", username, "--password-file", str(secret)], capture_output=True, timeout=30, creationflags=flags)
            assert initialized.returncode == 0, initialized.stderr.decode("utf8", errors="replace")
        finally:
            secret.unlink(missing_ok=True)
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        with (output / "runtime.log").open("w", encoding="utf8") as runtime_log:
            process = subprocess.Popen([binary, "serve", "--state", str(state), "--listen", f"127.0.0.1:{port}", "--material-library", str(library)], stdout=runtime_log, stderr=runtime_log, creationflags=flags)
            for _ in range(100):
                try:
                    urllib.request.urlopen(base + "/ops/api/v1/health", timeout=1).close()
                    break
                except OSError:
                    assert process.poll() is None, "isolated controller exited"
                    time.sleep(.1)
            else:
                raise AssertionError("isolated controller did not start")
            with sync_playwright() as playwright:
                browser = playwright.chromium.launch(headless=True, executable_path=args.browser_executable)
                context = browser.new_context(viewport={"width": 1440, "height": 1000})
                page = context.new_page()
                page.set_default_timeout(15000)
                page.on("pageerror", lambda error: errors.append(str(error)))

                def guard_network(route):
                    url = urlsplit(route.request.url)
                    if url.hostname == "annotation.invalid":
                        route.fulfill(content_type="text/html; charset=utf-8", body="<!doctype html><html><head><meta charset='utf-8'></head><body><h1>标注界面夹具</h1><input aria-label='未保存标注'></body></html>")
                    elif url.scheme in ["http", "https"] and f"{url.scheme}://{url.netloc}" != base:
                        blocked.append(route.request.url)
                        route.abort()
                    else:
                        route.continue_()
                context.route("**/*", guard_network)
                page.goto(base)
                page.get_by_label("账号", exact=True).fill(username)
                page.get_by_label("密码", exact=True).fill(password)
                page.get_by_role("button", name="登录", exact=True).click()
                page.get_by_role("heading", name="运行总览", exact=True).wait_for()
                data = page.request.get(base + "/ops/api/v1/software-recipes").json()["data"]
                recipes = [item for item in data if item.get("category") == "ANNOTATION"]
                assert len(recipes) == 8
                assert {item.get("catalogKey") for item in recipes} == {"label-studio", "ytlabel"}
                assert len([item for item in recipes if item["catalogKey"] == "label-studio"]) == 4
                assert all(item.get("web") for item in recipes)
                checks.append("eight-real-annotation-recipes-expose-their-supported-runtime-source-and-web-metadata")
                host_id, stamp = "11111111-1111-4111-8111-111111111115", "2026-11-09T12:00:00Z"
                cap = {"status": "AVAILABLE", "code": "READY", "message": "", "observedAt": stamp, "source": "fixture"}
                host = {"id": host_id, "name": "标注测试服务器", "kind": "SSH", "address": "192.0.2.20", "sshUser": "ops", "version": "1", "maintenance": False, "observedAt": stamp, "installRoot": "/opt/ops", "capabilities": {**{key: cap for key in ["reachability", "readOnly", "mutation", "docker", "dockerCompose"]}, "expiresAt": "2099-01-01T00:00:00Z", "platform": "linux/amd64", "distribution": "ubuntu", "distributionVersion": "24.04"}}
                instances = []
                for index, key in enumerate(["label-studio", "ytlabel"]):
                    definition = copy.deepcopy(next(item for item in recipes if item["catalogKey"] == key and "NATIVE" in item["runtimeModes"] and "ONLINE" in item["sourceModes"]))
                    number = 18080 if key == "label-studio" else 19924
                    config = {"name": definition["name"], "runtimeMode": "NATIVE", "memoryMiB": 1024, "ports": {}, "hostPort": number, "bindAddress": "192.0.2.20", "parameters": {"PUBLIC_URL": f"http://annotation.invalid:{number}/"}}
                    instances.append({"id": f"22222222-2222-4222-8222-{index+1:012d}", "hostId": host_id, "hostName": host["name"], "name": definition["name"], "softwareType": "OTHER", "softwareVersion": definition["version"], "runtimeKind": "SYSTEMD", "locator": f"annotation-{index}.service", "managementMode": "MANAGED", "desiredState": "RUNNING", "actualState": "RUNNING", "freshness": "STALE", "version": "1", "lastJobId": "", "installationStatus": "READY", "dataOwnership": "OPS", "createdAt": stamp, "updatedAt": stamp, "installation": {"definition": definition, "config": config}, "observation": {"state": "RUNNING", "identityHash": "a" * 64, "version": definition["version"], "observedAt": stamp, "errorCode": "", "message": "READY"}})
                python = {**instances[0], "id": "33333333-3333-4333-8333-333333333333", "name": "Python 3.12", "softwareType": "PYTHON", "softwareVersion": "3.12.13", "runtimeKind": "TOOL", "actualState": "INSTALLED", "desiredState": "INSTALLED", "installation": {},
                          "observation": {"state": "INSTALLED", "version": "3.12.13", "executablePath": "/opt/ops/python/bin/python3", "observedAt": stamp}}
                external = {"id": "44444444-4444-4444-8444-444444444444", "hostId": host_id, "hostName": host["name"], "name": "服务器已有 Label Studio", "softwareType": "LABEL_STUDIO", "softwareVersion": "1.22.0", "runtimeKind": "SYSTEMD", "locator": "label-studio.service", "managementMode": "OBSERVE", "desiredState": "UNCHANGED", "actualState": "UNKNOWN", "freshness": "UNOBSERVED", "version": "1", "lastJobId": "", "installationStatus": "", "dataOwnership": "EXTERNAL", "createdAt": stamp, "updatedAt": stamp}

                def api_fixture(route):
                    path, method = urlsplit(route.request.url).path.removeprefix("/ops/api/v1"), route.request.method
                    if method == "GET" and path == "/hosts": value = [host]
                    elif method == "POST" and path == f"/hosts/{host_id}/checks": value = host
                    elif method == "GET" and path == "/software-instances": value = [*instances, python]
                    elif method == "POST" and path == f"/hosts/{host_id}/software-discovery":
                        value = {"hostId": host_id, "hostVersion": host["version"], "observedAt": stamp, "limited": False, "checks": [], "configurations": [],
                                 "candidates": [{"name": external["name"], "softwareType": "LABEL_STUDIO", "runtimeKind": "SYSTEMD", "locator": "label-studio.service", "state": "RUNNING", "version": "1.22.0", "packageName": "label-studio", "ports": [], "configPaths": []}]}
                    elif method == "POST" and path == "/software-instances":
                        registrations.append(route.request.post_data_json)
                        assert registrations[-1]["softwareType"] == "LABEL_STUDIO"
                        assert "installation" not in registrations[-1] and "managementMode" not in registrations[-1]
                        instances.append(external)
                        value = external
                    elif method == "POST" and path == f"/software-instances/{external['id']}/observe":
                        external.update(actualState="RUNNING", freshness="FRESH", observation={"state": "RUNNING", "version": "1.22.0", "identityHash": "e" * 64, "observedAt": stamp})
                        value = external
                    elif method == "POST" and path == f"/hosts/{host_id}/software-install-plans":
                        preflights.append(route.request.post_data_json)
                        route.fulfill(status=409, json={"code": "COMMAND_PYTHON_NOT_FOUND", "msg": "所选 Python 不存在，请先安装兼容版本或选择已有解释器"})
                        return
                    elif method == "GET" and path.startswith("/software-instances/") and path.split("/")[-1] in {item["id"] for item in instances}: value = next(item for item in instances if item["id"] == path.split("/")[-1])
                    elif path.startswith("/command-templates/") and (method == "PUT" or method == "POST" and path.endswith("/publish")):
                        route.fallback()
                        return
                    elif method != "GET" and not path.startswith("/auth/"):
                        blocked.append(path)
                        route.fulfill(status=409, json={"code": "TEST_MUTATION_BLOCKED", "msg": "隔离界面测试未执行目标操作"})
                        return
                    else:
                        route.fallback()
                        return
                    route.fulfill(json={"code": "OK", "data": value})
                page.route("**/ops/api/v1/**", api_fixture)
                page.get_by_role("button", name="软件管理", exact=True).click()
                cards = page.locator("article.software-card")
                for item in instances:
                    card = cards.filter(has=page.get_by_role("heading", name=item["name"], exact=True))
                    expect(card.get_by_role("link", name="进入系统 ↗", exact=True)).to_have_attribute("href", item["installation"]["config"]["parameters"]["PUBLIC_URL"])
                label_card = cards.filter(has=page.get_by_role("heading", name="Label Studio", exact=True))
                with page.expect_popup() as popup:
                    label_card.get_by_role("link", name="进入系统 ↗", exact=True).click()
                expect(popup.value.get_by_role("heading", name="标注界面夹具", exact=True)).to_be_visible()
                popup.value.close()
                checks.append("installed-annotation-cards-open-the-configured-system-in-a-separate-tab")
                yt_card = cards.filter(has=page.get_by_role("heading", name="ytlabel", exact=True))
                yt_card.get_by_role("button", name="详情", exact=True).click()
                dialog = page.get_by_role("dialog", name="ytlabel", exact=True)
                dialog.get_by_role("tab", name="访问界面", exact=True).click()
                expect(dialog.locator("iframe")).to_have_count(0)
                dialog.get_by_role("button", name="内嵌预览", exact=True).click()
                expect(dialog.frame_locator("iframe").get_by_role("heading", name="标注界面夹具", exact=True)).to_be_visible()
                draft = dialog.frame_locator("iframe").get_by_label("未保存标注", exact=True)
                draft.fill("尚未保存的框选结果")
                with page.expect_response(lambda response: response.request.method == "GET" and urlsplit(response.url).path.endswith("/software-instances")):
                    instances[1]["actualState"] = "UNKNOWN"
                expect(dialog.get_by_text("请先检查软件运行状态", exact=True)).to_be_visible()
                expect(draft).to_have_value("尚未保存的框选结果")
                expect(dialog.get_by_role("button", name="关闭预览", exact=True)).to_be_visible()
                with page.expect_response(lambda response: response.request.method == "GET" and urlsplit(response.url).path.endswith("/software-instances")):
                    instances[1]["actualState"] = "RUNNING"
                expect(dialog.get_by_text("请先检查软件运行状态", exact=True)).not_to_be_visible()
                expect(draft).to_have_value("尚未保存的框选结果")
                checks.append("polling-and-temporary-unknown-observation-preserve-unsaved-annotation-input")
                for width, height in [(1440, 1000), (390, 700)]:
                    page.set_viewport_size({"width": width, "height": height})
                    assert dialog.evaluate("el => el.scrollWidth <= el.clientWidth+1")
                    page.screenshot(path=str(output / f"annotation-preview-{width}.png"), animations="disabled")
                dialog.get_by_role("button", name="关闭预览", exact=True).click()
                expect(dialog.locator("iframe")).to_have_count(0)
                dialog.get_by_role("button", name="内嵌预览", exact=True).click()
                dialog.get_by_role("tab", name="软件概览", exact=True).click()
                expect(dialog.locator("iframe")).to_have_count(0)
                page.set_viewport_size({"width": 1440, "height": 1000})
                page.mouse.click(3, 3)
                expect(dialog).not_to_be_visible()
                checks.append("iframe-is-opt-in-responsive-and-unloads-on-close-or-tab-change-without-a-nested-modal")
                instances[1]["installation"]["config"].update({"bindAddress": "127.0.0.1", "parameters": {}})
                page.get_by_role("button", name="刷新", exact=True).click()
                expect(yt_card.get_by_role("button", name="进入系统", exact=True)).to_be_disabled()
                yt_card.get_by_role("button", name="详情", exact=True).click()
                dialog.get_by_role("tab", name="访问界面", exact=True).click()
                expect(dialog.get_by_text("服务仅在服务器本机监听，请填写可访问的系统地址", exact=True)).to_be_visible()
                expect(dialog.get_by_role("link", name="进入系统 ↗", exact=True)).to_have_count(0)
                page.mouse.click(3, 3)
                checks.append("remote-loopback-is-never-linked-to-the-operator-computer")
                page.get_by_role("button", name="软件安装", exact=True).click()
                page.get_by_role("button", name="标注平台", exact=True).click()
                expect(page.locator(".plugin-card")).to_have_count(2)
                for key in ["label-studio", "ytlabel"]:
                    definition = next(item for item in recipes if item["catalogKey"] == key and "NATIVE" in item["runtimeModes"] and "ONLINE" in item["sourceModes"] and (key != "ytlabel" or item["version"] == "4.0-python312"))
                    card = page.locator(".plugin-card").filter(has=page.get_by_role("heading", name="Label Studio" if key == "label-studio" else "ytlabel", exact=True))
                    if key == "ytlabel":
                        (output / "ytlabel-card-version.json").write_text(json.dumps({"version": card.get_by_label("ytlabel版本", exact=True).input_value(), "expectedRecipe": definition["key"]}, ensure_ascii=False, indent=2), encoding="utf8")
                    card.get_by_role("button", name="安装", exact=True).click()
                    install = page.locator(".service-install-dialog.el-dialog")
                    expect(install).to_be_visible()
                    if key == "label-studio": install.get_by_role("button", name="原生安装", exact=True).click()
                    upload = next(field for field in definition["parameters"] if field["key"] == "MAX_UPLOAD_MIB")
                    if key == "ytlabel":
                        (output / "ytlabel-initial-form.json").write_text(json.dumps({"selectedScheme": install.get_by_label("安装方案", exact=True).input_value(), "fields": install.locator("input[aria-label]").evaluate_all("els => els.map(el => ({label:el.getAttribute('aria-label'),value:el.value}))")}, ensure_ascii=False, indent=2), encoding="utf8")
                        page.screenshot(path=str(output / "ytlabel-initial-form.png"), animations="disabled")
                    expect(install.get_by_label(upload["label"], exact=True)).to_be_visible()
                    expect(install.get_by_label("宿主机访问端口", exact=True)).to_be_visible()
                    expect(install.get_by_label("宿主机监听地址", exact=True)).to_have_value(definition.get("defaultBindAddress") or ("0.0.0.0" if definition.get("web") else host["address"]))
                    if key == "ytlabel": expect(install.get_by_text("ytlabel 无登录认证，请在受限网络使用。", exact=True)).to_be_visible()
                    for field in definition["parameters"]:
                        if field["type"] == "PATH": expect(install.get_by_label(field["label"], exact=True)).to_be_visible()
                    assert install.locator(".custom-install-parameters").bounding_box()["width"] >= install.get_by_label("实例配置", exact=True).bounding_box()["width"] * .9
                    page.screenshot(path=str(output / f"annotation-install-{key}.png"), animations="disabled")
                    if key == "ytlabel":
                        expect(install.get_by_role("button", name="原生安装", exact=True)).to_have_attribute("aria-pressed", "true")
                        expect(install.get_by_label("安装方案", exact=True)).to_have_value(json.dumps([definition["key"], definition["version"], definition["revision"]], separators=(",", ":")))
                        python_field = next(field for field in definition["parameters"] if field["key"] == "PYTHON_BINARY")
                        interpreter = install.get_by_label(python_field["label"], exact=True)
                        expect(interpreter).to_have_value("/usr/bin/python3")
                        suggestions = install.locator(f'datalist[id="{interpreter.get_attribute("list")}"] option')
                        assert suggestions.evaluate_all("els => els.map(el => el.value)") == ["/opt/ops/python/bin/python3"]
                        interpreter.fill("/opt/ops/python/bin/python3")
                        install.get_by_role("combobox", name="安装来源", exact=True).select_option("LIBRARY")
                        offline = next(item for item in recipes if item["key"] == "custom-ytlabel-py312-native-offline")
                        expect(install.get_by_label("安装方案", exact=True)).to_have_value(json.dumps([offline["key"], offline["version"], offline["revision"]], separators=(",", ":")))
                        expect(interpreter).to_have_value("/opt/ops/python/bin/python3")
                        install.get_by_role("combobox", name="安装来源", exact=True).select_option("ONLINE")
                        interpreter.fill("/usr/bin/python3")
                        preview = install.get_by_label("安装命令", exact=True)
                        preview.locator("summary").first.click()
                        dependency_step = preview.locator(".command-step").filter(has=page.locator("pre").filter(has_text="requirements.effective.txt")).first
                        dependency_step.locator("summary").first.click()
                        expect(dependency_step.locator("pre")).to_contain_text("opencv-python==4.10.0.84")
                        expect(dependency_step.locator("pre")).to_contain_text("psutil==5.9.8")
                        dependency_step.locator("summary").first.click()
                        preview.locator("summary").first.click()
                        checks.append("ytlabel-python312-default-native-editable-known-python-path-and-source-switch-preserve-version")
                        page.set_viewport_size({"width": 390, "height": 700})
                        assert install.evaluate("el => el.scrollWidth <= el.clientWidth+1")
                        expect(install.get_by_role("button", name="检查安装条件", exact=True)).to_be_in_viewport()
                        page.screenshot(path=str(output / "annotation-install-ytlabel-390.png"), animations="disabled")
                        page.set_viewport_size({"width": 1440, "height": 1000})
                        install.get_by_role("button", name="检查安装条件", exact=True).click()
                        expect(install.get_by_role("alert")).to_contain_text("所选 Python 不存在，请先安装兼容版本或选择已有解释器")
                        expect(install.get_by_text("COMMAND_PYTHON_NOT_FOUND", exact=True)).not_to_be_visible()
                        assert preflights[-1]["recipeKey"] == definition["key"] and preflights[-1]["config"]["parameters"]["PYTHON_BINARY"] == "/usr/bin/python3"
                        assert not install.get_by_role("button", name="确认安装", exact=True).count()
                        page.screenshot(path=str(output / "annotation-python-missing-preflight.png"), animations="disabled")
                        checks.append("missing-python-preflight-shows-chinese-reason-without-submitting-or-starting-installation")
                    page.mouse.click(3, 3)
                    if page.get_by_role("button", name="放弃修改", exact=True).count(): page.get_by_role("button", name="放弃修改", exact=True).click()
                    expect(install).not_to_be_visible()
                checks.append("annotation-category-forms-expose-upload-size-port-directory-and-safe-ytlabel-default")
                page.get_by_role("button", name="全部软件", exact=True).click()
                gitlab_definition = next(item for item in data if item.get("catalogKey") == "gitlab" and item.get("web", {}).get("urlParameter") == "EXTERNAL_URL")
                gitlab_card = page.locator(".plugin-card").filter(has=page.get_by_role("heading", name=gitlab_definition["name"], exact=True))
                gitlab_card.get_by_role("button", name="安装", exact=True).click()
                gitlab_install = page.locator(".service-install-dialog.el-dialog")
                external_field = next(field for field in gitlab_definition["parameters"] if field["key"] == "EXTERNAL_URL")
                external_url = gitlab_install.get_by_label(external_field["label"], exact=True)
                port = gitlab_definition["port"]
                expect(external_url).to_have_value(f"http://{host['address']}" + ("" if port == 80 else f":{port}") + "/")
                expect(gitlab_install.get_by_label("宿主机监听地址", exact=True)).to_have_value("0.0.0.0")
                gitlab_install.get_by_label("宿主机访问端口", exact=True).fill("18929")
                expect(external_url).to_have_value(f"http://{host['address']}:18929/")
                external_url.fill("https://git.company.invalid/")
                gitlab_install.get_by_label("宿主机访问端口", exact=True).fill("28929")
                expect(external_url).to_have_value("https://git.company.invalid/")
                page.screenshot(path=str(output / "gitlab-web-url-draft.png"), animations="disabled")
                page.mouse.click(3, 3)
                if page.get_by_role("button", name="放弃修改", exact=True).count(): page.get_by_role("button", name="放弃修改", exact=True).click()
                expect(gitlab_install).not_to_be_visible()
                checks.append("gitlab-default-target-ip-port-follows-unchanged-url-and-preserves-user-domain-without-installing")
                page.get_by_role("button", name="软件配置", exact=True).click()
                workspace = page.get_by_label("软件配置", exact=True)
                workspace.get_by_label("搜索软件配置", exact=True).fill("Label Studio")
                workspace.locator(".software-option").filter(has=page.get_by_text("Label Studio", exact=True)).click()
                editor = page.locator(".command-editor.el-dialog")
                editor.locator(".recipe-settings > summary").click()
                editor.get_by_label("默认监听地址", exact=True).fill("0.0.0.0")
                editor.get_by_role("tab", name="访问入口", exact=True).click()
                expect(editor.get_by_label("启用 Web 入口", exact=True)).to_be_checked()
                editor.get_by_label("入口路径", exact=True).fill("/label/")
                editor.get_by_label("允许内嵌预览", exact=True).check()
                with page.expect_response(lambda response: response.request.method == "PUT" and "/command-templates/" in response.url) as saved:
                    editor.get_by_role("button", name="保存草稿", exact=True).click()
                draft = saved.value.json()["data"]
                assert draft["draft"]["defaultBindAddress"] == "0.0.0.0"
                assert draft["draft"]["web"]["path"] == "/label/" and draft["draft"]["web"]["embed"] is True
                editor.get_by_role("button", name="预览发布", exact=True).click()
                with page.expect_response(lambda response: response.request.method == "POST" and response.url.endswith("/publish")) as published:
                    editor.get_by_role("button", name="确认发布", exact=True).click()
                record = published.value.json()["data"]
                assert record["published"]["web"] == draft["draft"]["web"]
                assert record["published"]["parameters"] == draft["draft"]["parameters"]
                checks.append("web-entry-is-editable-and-persists-through-real-template-save-and-publication")

                # Discovery and registration preserve an existing service's
                # external ownership; only an explicit read verifies its state.
                instances[:] = [item for item in instances if item["installation"]["definition"]["catalogKey"] != "label-studio"]
                page.goto(base)
                page.get_by_role("button", name="软件管理", exact=True).click()
                page.get_by_role("button", name="自动识别", exact=True).click()
                discovery = page.get_by_role("dialog", name="自动识别软件", exact=True)
                discovery.get_by_role("button", name="开始识别", exact=True).click()
                candidate = discovery.locator("tbody tr").filter(has=page.get_by_text(external["name"], exact=True))
                expect(candidate).to_contain_text("Label Studio")
                assert not registrations
                candidate.get_by_role("button", name="登记", exact=True).click()
                registration = page.get_by_role("dialog", name="登记已有软件", exact=True)
                expect(registration.get_by_label("软件类型", exact=True)).to_have_value("LABEL_STUDIO")
                expect(registration.get_by_label("systemd unit", exact=True)).to_have_value("label-studio.service")
                assert not registrations
                registration.get_by_role("button", name="只读登记", exact=True).click()
                detail = page.get_by_role("dialog", name=external["name"], exact=True)
                expect(detail.get_by_text("只读登记", exact=True)).to_be_visible()
                detail.get_by_role("button", name="检查状态", exact=True).click()
                expect(detail.locator(".detail-status .state-tag")).to_have_text("运行中")
                assert len(registrations) == 1 and external["managementMode"] == "OBSERVE" and "installation" not in external
                page.mouse.click(3, 3)
                page.get_by_role("button", name="软件安装", exact=True).click()
                page.get_by_role("button", name="标注平台", exact=True).click()
                label_catalog = page.locator(".plugin-card").filter(has=page.get_by_role("heading", name="Label Studio", exact=True))
                expect(label_catalog).to_contain_text("已安装其他版本")
                expect(label_catalog.get_by_role("button", name="安装", exact=True)).to_be_enabled()
                external["observation"]["version"] = next(item["version"] for item in recipes if item["catalogKey"] == "label-studio")
                page.get_by_role("button", name="刷新", exact=True).click()
                expect(label_catalog.get_by_text("已安装", exact=True)).to_be_visible()
                external["observation"]["version"], external["softwareVersion"] = "", ""
                page.get_by_role("button", name="刷新", exact=True).click()
                expect(label_catalog).to_contain_text("已安装 · 版本待确认")
                expect(label_catalog.get_by_role("button", name="安装", exact=True)).to_be_enabled()
                page.screenshot(path=str(output / "external-label-studio-catalog.png"), animations="disabled")
                checks.append("external-label-studio-explicit-readonly-registration-observation-and-catalog-known-other-or-unknown-version")
                check_configuration_ui(page, output, next(item for item in recipes if item["key"] == "custom-ytlabel-py312-native-online"), checks)
                for key in ["nginx", "mysql", "redis", "custom-jenkins-native-online", "custom-postgresql-native-online", "custom-gitlab-native-online", "custom-prometheus-native-online", "custom-nacos-native-online"]:
                    check_stock_service_configuration_ui(page, output, next(item for item in data if item["key"] == key), checks)
                assert not errors and not blocked, (errors, blocked)
                result["status"] = "PASS"
                browser.close()
    except Exception as error:
        result.update({"status": "FAIL", "error": str(error), "traceback": traceback.format_exc()})
        if page:
            try:
                page.screenshot(path=str(output / "failure.png"), animations="disabled")
                (output / "failure-ui.txt").write_text(page.locator("body").inner_text(), encoding="utf8")
            except Exception:
                pass
        raise
    finally:
        if process and process.poll() is None:
            subprocess.run([binary, "stop", "--state", str(state)], capture_output=True, timeout=20, creationflags=flags)
            process.wait(timeout=15)
        (output / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf8")
        print(json.dumps({"status": result["status"], "checks": len(checks), "remoteInstalls": 0}))


if __name__ == "__main__":
    main()
