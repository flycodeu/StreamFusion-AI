"""Real isolated controller/material library and explicitly labelled UI fixtures.

No target installation, lifecycle, uninstall, or preparation request reaches the
controller. Owned software and Engine capabilities are browser fixtures only.
"""

import argparse
from copy import deepcopy
from datetime import datetime, timedelta, timezone
import hashlib
import json
from pathlib import Path
import re
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
    parser.add_argument("--material-library", required=True)
    parser.add_argument("--uninstall-paths-only", action="store_true", help="Run the final uninstall-path UI risk checks without repeating catalog UI coverage")
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve())
    library = Path(args.material_library).resolve()
    manifest = json.loads((library / "library.json").read_text(encoding="utf8"))
    output = Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=False)
    state = output / "state"
    process = None
    page = None
    checks, browser_errors, blocked_mutations, fixture_actions = [], [], [], []
    report = {
        "status": "RUNNING",
        "scope": "Real isolated controller, real sidecar import/API/UI; owned software and Engine UI fixtures only",
        "binary": binary,
        "materialLibrary": str(library),
        "state": str(state),
        "checks": checks,
        "browserErrors": browser_errors,
        "blockedUnexpectedMutations": blocked_mutations,
        "fixtureActions": fixture_actions,
        "targetMutationExecuted": False,
        "uninstallPathsOnly": args.uninstall_paths_only,
        "limitations": ["No Linux/Docker installation or uninstall acceptance", "No existing controller state used"],
    }
    flags = getattr(subprocess, "CREATE_NO_WINDOW", 0)
    password = "Library-" + secrets.token_hex(16)
    username = "library_" + secrets.token_hex(6)
    password_file = output / ".password.secret"
    try:
        assert manifest["schemaVersion"] == 1
        assert {entry["recipeKey"] for entry in manifest["materials"]} == {"redis", "nginx", "java", "docker"}
        assert len(manifest["materials"]) == 4
        for entry in manifest["materials"]:
            archive = library / entry["file"]
            assert archive.is_file() and archive.stat().st_size == entry["byteSize"], entry["file"]
            with archive.open("rb") as stream:
                digest = hashlib.file_digest(stream, "sha256").hexdigest()
            assert digest == entry["sha256"], entry["file"]
        checks.append("real-sidecar-four-archive-sizes-and-sha256-match-manifest")
        password_file.write_text(password, encoding="utf8")
        password_file.chmod(0o600)
        try:
            subprocess.run([binary, "init-admin", "--state", str(state), "--username", username,
                            "--password-file", str(password_file)], check=True, capture_output=True,
                           timeout=30, creationflags=flags)
        finally:
            password_file.unlink(missing_ok=True)
        imported = subprocess.run([binary, "import-materials", "--state", str(state),
                                   "--material-library", str(library)], check=True, capture_output=True,
                                  timeout=120, text=True, encoding="utf8", creationflags=flags)
        (output / "import.log").write_text(imported.stdout + imported.stderr, encoding="utf8")
        checks.append("real-cli-import-material-library")
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        with (output / "runtime.log").open("w", encoding="utf8") as runtime_log:
            process = subprocess.Popen([binary, "serve", "--state", str(state), "--listen", f"127.0.0.1:{port}",
                                        "--material-library", str(library)], stdout=runtime_log, stderr=runtime_log,
                                       creationflags=flags)
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
            playwright = sync_playwright().start()
            try:
                browser = playwright.chromium.launch(headless=True, executable_path=args.browser_executable)
                page = browser.new_page(viewport={"width": 1440, "height": 1000})
                page.set_default_timeout(12000)
                page.on("pageerror", lambda error: browser_errors.append(str(error)))

                def block_target_mutations(route):
                    request = route.request
                    path = urlsplit(request.url).path.removeprefix("/ops/api/v1")
                    if request.method not in ("GET", "HEAD", "OPTIONS") and not path.startswith("/auth/"):
                        blocked_mutations.append({"path": path, "method": request.method})
                        route.fulfill(status=409, json={"code": "TEST_TARGET_MUTATION_BLOCKED", "message": "Test does not execute targets"})
                    else:
                        route.continue_()

                page.route("**/ops/api/v1/**", block_target_mutations)
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
                    result = response.json()
                    assert response.status == expected, (path, response.status, result.get("code"))
                    return result["data"]

                host = api("/hosts", "POST", {"name": "材料库隔离主机", "kind": "LOCAL"}, 201)
                definitions = api("/software-recipes")
                materials = api("/installation-materials")
                assert len(materials) == 4, materials
                assert not api("/software-instances")
                for entry in manifest["materials"]:
                    material = next(item for item in materials if item["recipeKey"] == entry["recipeKey"])
                    recipe = next(item for item in definitions if item["key"] == entry["recipeKey"] and item["version"] == entry["recipeVersion"])
                    assert material["status"] == "AVAILABLE" and material["available"]
                    assert material["recipeVersion"] == entry["recipeVersion"] and material["recipeRevision"] == entry["recipeRevision"]
                    assert material["runtimeMode"] == entry["runtimeMode"] and material["sha256"] == entry["sha256"]
                    assert material["byteSize"] == entry["byteSize"]
                    assert material["origin"]["kind"] == "BUNDLED" and material["origin"]["verification"] == "RECIPE_SHA256"
                    assert material["origin"]["fileName"] == entry["file"] and material["origin"]["importedAt"]
                    assert material["recipeName"] == recipe["name"]
                    assert len(material["sources"]) == len(recipe["native"]["parts"])
                    parts = {part["name"]: part for part in recipe["native"]["parts"]}
                    assert sum(source["role"] == "MAIN" for source in material["sources"]) == 1
                    for source in material["sources"]:
                        part = parts[source["name"]]
                        assert all(source[key] == part[key] for key in ("url", "sha256", "byteSize"))
                        assert source["displayName"] and source["version"] and source["publisher"]
                        assert source["url"].startswith("https://")
                    checks.append(f"real-api-{entry['recipeKey']}-available-bundled-version-origin-readable-sources")
                (output / "materials.json").write_text(json.dumps(materials, ensure_ascii=False, indent=2), encoding="utf8")
                docker = next(item for item in definitions if item["key"] == "docker")
                docker_material = next(item for item in materials if item["recipeKey"] == "docker")
                assert docker["kind"] == "SERVICE" and docker["runtimeModes"] == ["NATIVE"]
                assert docker["native"]["buildRecipe"] == "DEB_PACKAGE" and len(docker_material["sources"]) == 5
                assert any(source.get("requirements") for source in docker_material["sources"])
                checks.append("real-docker-native-five-deb-recipe-and-target-requirements")

                def software_page():
                    page.goto(base)
                    page.get_by_role("button", name="软件管理", exact=True).click()

                def catalog():
                    page.get_by_role("tab", name="软件目录", exact=True).click()
                    page.locator(".plugin-card").first.wait_for()

                def card_for(name):
                    return page.locator("article.plugin-card").filter(has=page.get_by_role("heading", name=name, exact=True))

                def open_install(recipe):
                    card_for(recipe["name"]).get_by_role("button", name="安装", exact=True).click()
                    drawer = page.get_by_role("dialog", name="安装 " + recipe["name"], exact=True)
                    drawer.wait_for()
                    return drawer

                def close_dialog(dialog):
                    dialog.get_by_role("button", name="取消", exact=True).click()
                    dialog.wait_for(state="hidden")

                def screenshot_layout(subject, name):
                    for width in (1440, 390):
                        page.set_viewport_size({"width": width, "height": 1000})
                        if subject.locator(".el-drawer__body").count():
                            subject.locator(".el-drawer__body").evaluate("el => el.scrollTop = 0")
                        if subject.locator(".uninstall-form").count():
                            subject.locator(".uninstall-form").evaluate("el => el.scrollTop = 0")
                        # Responsive styles must settle before measuring scroll bounds.
                        page.wait_for_timeout(150)
                        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), (name, width, "page")
                        assert subject.evaluate("el => el.scrollWidth <= el.clientWidth + 1"), (name, width, "subject")
                        page.screenshot(path=str(output / f"{name}-{width}.png"), animations="disabled")
                        checks.append(f"layout-{name}-{width}")
                    page.set_viewport_size({"width": 1440, "height": 1000})

                software_page()
                catalog()
                for key in (() if args.uninstall_paths_only else ("redis", "nginx", "java", "docker")):
                    material = next(item for item in materials if item["recipeKey"] == key)
                    recipe = next(item for item in definitions if item["key"] == key and item["version"] == material["recipeVersion"])
                    card = card_for(recipe["name"])
                    if card.locator("select.version-select").count():
                        expect(card.locator("select.version-select")).to_have_value(recipe["version"])
                    drawer = open_install(recipe)
                    if drawer.get_by_role("radio", name="原生 / systemd", exact=False).count():
                        drawer.get_by_role("radio", name="原生 / systemd", exact=False).check()
                    else:
                        drawer.get_by_role("radio", name="原生程序", exact=False).check()
                    expect(drawer.get_by_role("radio", name="本地离线材料库 · 默认", exact=False)).to_be_checked()
                    expect(drawer.get_by_label("可用安装包", exact=True)).to_have_value(material["id"])
                    contents = drawer.get_by_label("安装材料内容", exact=True)
                    expect(contents.get_by_text("本地材料已就绪", exact=True)).to_be_visible()
                    assert "目标服务器无需外网下载" in contents.inner_text()
                    for source in material["sources"]:
                        assert source["displayName"] in contents.inner_text() and source["publisher"] in contents.inner_text()
                        assert contents.locator("a").filter(has_text=source["displayName"] + " 下载来源").count() >= 1
                    assert not contents.locator("details").get_attribute("open")
                    contents.locator("summary").click()
                    for source in material["sources"]:
                        expect(contents.get_by_text(source["name"], exact=True)).to_be_visible()
                        expect(contents.get_by_text(source["sha256"], exact=True)).to_be_visible()
                    contents.locator("summary").click()
                    checks.append(f"real-ui-{key}-matching-offline-material-auto-selected-and-source-details")
                    if key == "docker":
                        assert contents.get_by_label("目标主机依赖").count() > 0
                        assert "不会自动将用户加入 docker 用户组" in contents.inner_text()
                        screenshot_layout(drawer, "docker-materials-real")
                        drawer.get_by_role("button", name="下一步", exact=True).click()
                        expect(drawer.get_by_label("实例名称", exact=True)).to_be_visible()
                        assert drawer.get_by_role("button", name="新建凭据", exact=True).count() == 0
                        assert drawer.locator('input[type="password"]').count() == 0
                        drawer.get_by_role("button", name="下一步", exact=True).click()
                        assert drawer.get_by_label("宿主机访问端口", exact=True).count() == 0
                        assert drawer.get_by_label("宿主机监听地址", exact=True).count() == 0
                        assert drawer.get_by_label("内存上限（MiB）", exact=True).count() == 0
                        assert "本地 Unix socket" in drawer.inner_text()
                        screenshot_layout(drawer, "docker-configuration-real")
                        checks.append("real-ui-docker-no-credentials-port-or-memory-fields")
                    elif key == "nginx":
                        screenshot_layout(drawer, "nginx-materials-real")
                    close_dialog(drawer)

                # A manual selection must survive periodic catalog/material refresh.
                if not args.uninstall_paths_only:
                    redis_card = card_for(next(item for item in definitions if item["key"] == "redis" and item["version"] == "7.0.15-1ubuntu0.24.04.5")["name"])
                    redis_choice = redis_card.locator("select.version-select")
                    other_redis = next(item for item in definitions if item["key"] == "redis" and item["version"] != redis_choice.input_value())
                    redis_choice.select_option(other_redis["version"])
                    refresh_button = page.locator(".catalog-search").get_by_role("button", name="刷新", exact=True)
                    with page.expect_response(lambda response: urlsplit(response.url).path == "/ops/api/v1/installation-materials"):
                        refresh_button.click()
                    expect(refresh_button).to_be_enabled()
                    expect(card_for(other_redis["name"]).locator("select.version-select")).to_have_value(other_redis["version"])
                    checks.append("real-ui-default-bundled-version-and-explicit-version-choice-preserved-on-refresh")

                # Everything below is a named browser fixture, never target state.
                now = datetime.now(timezone.utc)
                stamp = now.isoformat().replace("+00:00", "Z")
                expiry = (now + timedelta(hours=1)).isoformat().replace("+00:00", "Z")
                fixture_host = {**host, "name": "界面夹具 Ubuntu 主机", "kind": "SSH", "address": "192.0.2.25",
                                "installRoot": "/opt/streamfusion-ops", "observedAt": stamp, "maintenance": False}
                def capability(status):
                    return {"status": status, "code": "UI_FIXTURE", "message": "界面夹具，无目标执行", "observedAt": stamp, "source": "UI_FIXTURE"}
                fixture_host["capabilities"] = {"reachability": capability("AVAILABLE"), "readOnly": capability("AVAILABLE"),
                                                 "mutation": capability("AVAILABLE"), "docker": capability("AVAILABLE"),
                                                 "expiresAt": expiry, "platform": "linux/amd64"}
                fixture_rows = []
                page.route("**/ops/api/v1/hosts", lambda route: route.fulfill(json={"data": [fixture_host]}))
                page.route("**/ops/api/v1/software-instances", lambda route: route.fulfill(json={"data": fixture_rows}))
                software_page()
                catalog()
                engine_bar = page.get_by_label("主机 Docker Engine", exact=True)
                if not args.uninstall_paths_only:
                    expect(engine_bar.get_by_text("已检测到外部 Docker，可用于容器安装；启停由原安装方式管理", exact=True)).to_be_visible()
                    assert engine_bar.get_by_role("button").count() == 0
                    expect(card_for(docker["name"]).get_by_role("button", name="安装", exact=True)).to_be_disabled()
                    assert page.get_by_role("button", name=re.compile("接管.*Docker|发现.*Docker")).count() == 0
                    screenshot_layout(page.locator(".software-recipes"), "external-engine-fixture")
                    checks.append("fixture-external-engine-no-adopt-or-discovery-action-and-duplicate-install-disabled")

                mysql = next(item for item in definitions if item["key"] == "mysql")
                config = {"name": "界面夹具 MySQL", "runtimeMode": "DOCKER", "storageRoot": "/opt/fixture", "ports": {},
                          "bindAddress": "127.0.0.1", "hostPort": 13306, "memoryMiB": 1024}
                owned = {"id": str(uuid.uuid4()), "name": config["name"], "hostId": host["id"], "hostName": fixture_host["name"],
                         "softwareType": "MYSQL", "runtimeKind": "DOCKER", "locator": "a" * 64, "managementMode": "MANAGED",
                         "actualState": "RUNNING", "desiredState": "RUNNING", "installationStatus": "READY", "version": "7",
                         "freshness": "FRESH", "lastJobId": "", "errorCode": "", "dataOwnership": "MANAGED",
                         "installation": {"definition": mysql, "config": config},
                         "observation": {"state": "RUNNING", "identityHash": "b" * 64, "version": mysql["version"], "observedAt": stamp,
                                         "source": "UI_FIXTURE", "listeningPorts": ["3306/tcp"]}, "createdAt": stamp, "updatedAt": stamp}
                def fixture_paths(instance_id):
                    root = "/opt/fixture/" + instance_id
                    return {"root": root, "data": root + "/data", "config": root + "/config", "logs": root + "/logs"}
                owned["installationPaths"] = fixture_paths(owned["id"])
                adopted = {**deepcopy(owned), "id": str(uuid.uuid4()), "name": "界面夹具 外部接管", "dataOwnership": "EXTERNAL"}
                adopted.pop("installation")
                adopted.pop("installationPaths")
                engine = {**deepcopy(owned), "id": str(uuid.uuid4()), "name": "界面夹具 Docker Engine", "softwareType": "DOCKER",
                          "runtimeKind": "SYSTEMD", "locator": "docker.service", "installation": {"definition": docker,
                          "config": {**config, "name": "界面夹具 Docker Engine", "runtimeMode": "NATIVE", "hostPort": 0, "memoryMiB": 0}},
                          "observation": {"state": "RUNNING", "identityHash": "c" * 64, "version": docker["version"], "observedAt": stamp,
                                          "source": "UI_FIXTURE", "listeningPorts": []}}
                engine["installationPaths"] = {**fixture_paths(engine["id"]), "bin": "/usr/bin"}
                missing_paths = {**deepcopy(owned), "id": str(uuid.uuid4()), "name": "界面夹具 缺少安装目录"}
                missing_paths.pop("installationPaths")
                history = {**deepcopy(owned), "id": str(uuid.uuid4()), "name": "界面夹具 已卸载历史", "managementMode": "RELEASED",
                           "actualState": "REMOVED", "desiredState": "REMOVED", "installationStatus": "UNINSTALLED"}
                history["observation"]["state"] = "REMOVED"
                history["lastJobId"] = str(uuid.uuid4())
                fixture_rows[:] = [owned, adopted, engine, history, missing_paths]
                job = {"id": history["lastJobId"], "status": "SUCCEEDED", "phase": "SOFTWARE_UNINSTALLED", "errorCode": "",
                       "traceId": "UI_FIXTURE_UNINSTALL_HISTORY", "updatedAt": stamp, "targetAckPending": False}
                page.route(f"**/ops/api/v1/jobs/{job['id']}", lambda route: route.fulfill(json={"data": job}))
                (output / "ui-fixtures.json").write_text(json.dumps({"host": fixture_host, "softwareInstances": fixture_rows, "job": job},
                                                                   ensure_ascii=False, indent=2), encoding="utf8")

                def details(item):
                    page.locator(".software-card").filter(has=page.get_by_role("heading", name=item["name"], exact=True)).get_by_role("button", name="详情", exact=True).click()
                    drawer = page.get_by_role("dialog", name=item["name"], exact=True)
                    drawer.wait_for()
                    return drawer

                def close_details(drawer):
                    drawer.get_by_role("button", name="Close this dialog", exact=True).click()
                    drawer.wait_for(state="hidden")

                def check_paths(dialog, item, title):
                    scope = dialog.get_by_label("卸载目录范围", exact=True)
                    expect(scope.get_by_role("heading", name=title, exact=True)).to_be_visible()
                    for key in ("root", "data", "config", "logs"):
                        path = scope.get_by_text(item["installationPaths"][key], exact=True)
                        path.scroll_into_view_if_needed()
                        expect(path).to_be_visible()

                software_page()
                drawer = details(owned)
                expect(drawer.get_by_role("button", name="解除管理", exact=True)).to_be_visible()
                drawer.get_by_role("button", name="卸载软件", exact=True).click()
                uninstall = page.get_by_role("dialog", name="卸载软件", exact=True)
                expect(uninstall.get_by_role("checkbox", name="同时删除此实例的数据", exact=True)).not_to_be_checked()
                expect(uninstall.get_by_role("button", name="卸载，保留数据", exact=True)).to_be_enabled()
                assert "默认保留此实例的数据、配置和日志目录" in uninstall.inner_text()
                check_paths(uninstall, owned, "保留的实例目录")
                screenshot_layout(uninstall, "uninstall-preserve-fixture")
                uninstall.get_by_role("checkbox", name="同时删除此实例的数据", exact=True).check()
                delete = uninstall.get_by_role("button", name="卸载并删除数据", exact=True)
                expect(delete).to_be_disabled()
                uninstall.get_by_label("确认删除的实例名称", exact=True).fill(owned["name"] + "x")
                expect(delete).to_be_disabled()
                uninstall.get_by_label("确认删除的实例名称", exact=True).fill(owned["name"])
                expect(delete).to_be_enabled()
                check_paths(uninstall, owned, "将删除的实例目录")
                screenshot_layout(uninstall, "uninstall-delete-confirmation-fixture")
                checks.append("fixture-owned-uninstall-shows-exact-root-data-config-logs-for-preserve-and-delete")

                def intercepted_uninstall(route):
                    data = route.request.post_data_json
                    fixture_actions.append(data)
                    assert data["action"] == "UNINSTALL" and data["confirmed"] and data["deleteData"]
                    assert data["confirmationName"] == owned["name"] and data["expectedVersion"] == "7"
                    # Deliberate receipt loss, then a successful fixture response.
                    # Neither request is forwarded to the real controller.
                    if len(fixture_actions) == 1:
                        route.abort("connectionfailed")
                    else:
                        route.fulfill(json={"data": {"softwareInstanceId": owned["id"]}})

                page.route(f"**/ops/api/v1/software-instances/{owned['id']}/actions", intercepted_uninstall)
                delete.click()
                expect(uninstall.get_by_role("alert")).to_be_visible()
                expect(uninstall.get_by_role("button", name="重试并核对原请求", exact=True)).to_be_enabled()
                expect(uninstall.get_by_label("确认删除的实例名称", exact=True)).to_be_disabled()
                uninstall.get_by_role("button", name="重试并核对原请求", exact=True).click()
                uninstall.wait_for(state="hidden")
                assert len(fixture_actions) == 2 and fixture_actions[0] == fixture_actions[1]
                checks.append("fixture-lost-uninstall-receipt-reuses-same-idempotency-key-and-version")
                close_details(drawer)
                drawer = details(adopted)
                assert drawer.get_by_role("button", name="卸载软件", exact=True).count() == 0
                checks.append("fixture-external-adopted-software-has-no-uninstall-action")
                close_details(drawer)

                drawer = details(missing_paths)
                drawer.get_by_role("button", name="卸载软件", exact=True).click()
                uninstall = page.get_by_role("dialog", name="卸载软件", exact=True)
                expect(uninstall.get_by_role("alert")).to_contain_text("未取得完整安装目录")
                expect(uninstall.get_by_role("button", name="卸载，保留数据", exact=True)).to_be_enabled()
                uninstall.get_by_role("checkbox", name="同时删除此实例的数据", exact=True).check()
                uninstall.get_by_label("确认删除的实例名称", exact=True).fill(missing_paths["name"])
                expect(uninstall.get_by_role("button", name="卸载并删除数据", exact=True)).to_be_disabled()
                screenshot_layout(uninstall, "uninstall-missing-paths-blocked-fixture")
                close_dialog(uninstall)
                close_details(drawer)
                checks.append("fixture-missing-installation-paths-block-delete-even-with-exact-name-preserve-remains-available")

                catalog()
                engine_bar.get_by_role("button", name="查看与启停", exact=True).click()
                drawer = page.get_by_role("dialog", name=engine["name"], exact=True)
                expect(drawer.get_by_role("button", name="停止", exact=True)).to_be_enabled()
                drawer.get_by_role("button", name="停止", exact=True).click()
                stop_confirmation = page.get_by_role("dialog", name="停止服务", exact=True)
                expect(stop_confirmation).to_be_visible()
                assert owned["name"] in stop_confirmation.inner_text() and adopted["name"] in stop_confirmation.inner_text()
                assert "未登记容器也可能受影响" in stop_confirmation.inner_text()
                stop_confirmation.get_by_role("button", name="取消", exact=True).click()
                stop_confirmation.wait_for(state="hidden")
                drawer.get_by_role("tab", name="服务配置", exact=True).click()
                assert drawer.get_by_role("button", name="调整配置", exact=True).count() == 0
                drawer.get_by_role("button", name="卸载软件", exact=True).click()
                uninstall = page.get_by_role("dialog", name="卸载软件", exact=True)
                assert "容器服务" in uninstall.inner_text() and owned["name"] in uninstall.inner_text()
                assert "/var/lib/containerd" in uninstall.inner_text()
                check_paths(uninstall, engine, "保留的实例目录")
                systems = uninstall.get_by_label("Docker 系统目录处理", exact=True)
                assert "/etc/docker/daemon.json" in systems.inner_text() and "保留。" in systems.inner_text()
                screenshot_layout(uninstall, "engine-uninstall-warning-fixture")
                uninstall.get_by_role("checkbox", name="同时删除此实例的数据", exact=True).check()
                check_paths(uninstall, engine, "将删除的实例目录")
                assert "仅在内容仍与本实例安装回执一致时删除；不一致时报告错误" in systems.inner_text()
                assert "/var/lib/containerd" in systems.inner_text() and "始终保留" in systems.inner_text()
                systems.scroll_into_view_if_needed()
                for width in (1440, 390):
                    page.set_viewport_size({"width": width, "height": 1000})
                    systems.scroll_into_view_if_needed()
                    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
                    assert uninstall.evaluate("el => el.scrollWidth <= el.clientWidth + 1")
                    page.screenshot(path=str(output / f"engine-delete-system-paths-fixture-{width}.png"), animations="disabled")
                    checks.append(f"layout-engine-delete-system-paths-fixture-{width}")
                page.set_viewport_size({"width": 1440, "height": 1000})
                close_dialog(uninstall)
                close_details(drawer)
                checks.append("fixture-engine-exact-directories-daemon-delete-condition-and-containerd-preserved")

                page.get_by_label("筛选管理模式", exact=True).select_option("ALL")
                history_card = page.locator(".software-card").filter(has=page.get_by_role("heading", name=history["name"], exact=True))
                expect(history_card.locator(".state-tag")).to_have_text("已卸载")
                drawer = details(history)
                assert drawer.get_by_role("button", name="卸载软件", exact=True).count() == 0
                assert drawer.get_by_role("button", name="解除管理", exact=True).count() == 0
                drawer.get_by_role("tab", name="执行记录", exact=True).click()
                expect(drawer.locator(".software-job")).to_be_visible()
                assert job["id"] in drawer.inner_text() and "已完成" in drawer.inner_text() and "软件已卸载" in drawer.inner_text()
                screenshot_layout(drawer, "uninstalled-history-fixture")
                close_details(drawer)
                checks.append("fixture-uninstalled-record-and-execution-history-remain-visible")
                assert len(api("/installation-materials")) == 4 and not api("/software-instances")
                assert not blocked_mutations, blocked_mutations
                assert not browser_errors, browser_errors
                checks.append("real-controller-no-software-instances-created-no-target-mutations-browser-errors")
                report["status"] = "PASS"
                browser.close()
            except Exception:
                if page is not None:
                    try:
                        page.screenshot(path=str(output / "failure.png"), animations="disabled")
                    except Exception:
                        pass
                raise
            finally:
                playwright.stop()
    except Exception as error:
        report["status"] = "FAIL"
        report["error"] = str(error)
        report["traceback"] = traceback.format_exc()
        if page is not None:
            try:
                page.screenshot(path=str(output / "failure.png"), animations="disabled")
            except Exception:
                pass
        raise
    finally:
        password_file.unlink(missing_ok=True)
        if process is not None:
            if process.poll() is None:
                stopped = subprocess.run([binary, "stop", "--state", str(state)], capture_output=True, timeout=20,
                                         creationflags=flags, text=True, encoding="utf8")
                (output / "stop.log").write_text(stopped.stdout + stopped.stderr, encoding="utf8")
            try:
                process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                process.terminate()
                process.wait(timeout=10)
            report["runtimeExitCode"] = process.returncode
        (output / "result.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf8")
        print(json.dumps({"status": report["status"], "checks": len(checks), "output": str(output)}, ensure_ascii=False))


if __name__ == "__main__":
    main()
