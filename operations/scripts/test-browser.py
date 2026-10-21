"""Browser acceptance for Ops, including F01 hosts and F02 software. Isolated state only."""
import argparse
import json
import pathlib
import platform
import secrets
import socket
import subprocess
import tarfile
import time
import urllib.request
from playwright.sync_api import sync_playwright

parser = argparse.ArgumentParser()
parser.add_argument("--binary", required=True)
parser.add_argument("--output", required=True)
parser.add_argument("--browser-executable", help="已有Chromium可执行文件；默认使用Playwright匹配版本")
args = parser.parse_args()
binary = str(pathlib.Path(args.binary).resolve())
output = pathlib.Path(args.output).resolve()
output.mkdir(parents=True, exist_ok=True)
state = output / "state"
password = "Browser-" + secrets.token_hex(12)
password_file = output / ".password.secret"
password_file.write_text(password)
password_file.chmod(0o600)
subprocess.run([binary, "init-admin", "--state", str(state), "--password-file", str(password_file)], check=True)
password_file.unlink()
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
url = f"http://127.0.0.1:{port}"
stderr = (output / "runtime.log").open("w")
process = subprocess.Popen([binary, "serve", "--state", str(state), "--listen", f"127.0.0.1:{port}"], stdout=stderr, stderr=stderr)
checks = []
errors = []
try:
    for attempt in range(50):
        try:
            with urllib.request.urlopen(url + "/ops/api/v1/health", timeout=1) as response:
                assert response.status == 200
            break
        except Exception:
            time.sleep(0.1)
    else:
        raise AssertionError("controller failed to start")
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True, executable_path=args.browser_executable)
        page = browser.new_page(viewport={"width": 1440, "height": 900})
        page.on("pageerror", lambda error: errors.append(str(error)))
        def screenshot_ready(dialog=None):
            page.wait_for_function("() => document.querySelectorAll('.el-message').length === 0")
            if dialog is None:
                page.locator(".el-overlay:visible").wait_for(state="hidden")
            else:
                dialog.wait_for(state="visible")
                page.wait_for_function("() => !document.getAnimations().some(a => a.playState === 'running' && a.effect?.target?.closest?.('.el-overlay'))")

        page.goto(url)
        page.get_by_label("账号", exact=True).fill("admin")
        page.get_by_label("密码", exact=True).fill(password)
        page.get_by_role("button", name="登录", exact=True).click()
        page.get_by_role("heading", name="运行总览", exact=True).wait_for()
        page.get_by_text("运行中", exact=True).wait_for()
        screenshot_ready()
        page.screenshot(path=str(output / "overview-wide.png"), full_page=True, animations="disabled")
        page.reload()
        page.get_by_role("heading", name="运行总览", exact=True).wait_for()
        checks.extend(["login", "session-after-reload", "real-status"])
        page.get_by_role("button", name="连接凭据", exact=True).click()
        page.get_by_role("button", name="新增凭据", exact=True).click()
        dialog = page.get_by_role("dialog", name="新增凭据", exact=True)
        dialog.get_by_label("名称", exact=True).fill("项目仓库")
        dialog.get_by_label("凭据内容", exact=True).fill("test-token-not-visible")
        dialog.get_by_role("button", name="保存", exact=True).click()
        dialog.wait_for(state="hidden")
        assert page.get_by_text("test-token-not-visible", exact=True).count() == 0
        page.get_by_role("button", name="新修订", exact=True).click()
        dialog = page.get_by_role("dialog", name="创建新修订", exact=True)
        dialog.get_by_label("凭据内容", exact=True).fill("revision-two-token")
        dialog.get_by_role("button", name="保存", exact=True).click()
        dialog.wait_for(state="hidden")
        row = page.locator("tbody tr").filter(has=page.get_by_text("r1", exact=True))
        row.get_by_role("button", name="撤销", exact=True).click()
        page.locator(".el-message-box").get_by_role("button", name="撤销", exact=True).click()
        row.get_by_text("已撤销", exact=True).wait_for()
        checks.extend(["credential-create", "credential-revision", "credential-revoke", "secret-not-rendered"])
        page.get_by_role("button", name="系统设置", exact=True).click()
        page.get_by_label("控制器名称", exact=True).fill("测试控制器")
        page.get_by_role("button", name="保存名称", exact=True).click()
        page.get_by_text("名称已保存", exact=True).wait_for()
        with page.expect_download() as download:
            page.get_by_role("button", name="下载备份", exact=True).click()
        backup = output / "ui-backup.tar.gz"
        download.value.save_as(str(backup))
        with tarfile.open(backup) as archive:
            assert "manifest.json" in archive.getnames() and "master.key" not in archive.getnames()
        checks.extend(["settings-save", "browser-backup-download"])
        page.get_by_role("button", name="操作记录", exact=True).click()
        page.get_by_text("创建备份", exact=True).wait_for()
        page.get_by_role("button", name="详情", exact=True).first.click()
        page.get_by_role("dialog", name="记录详情", exact=True).wait_for()
        page.get_by_role("dialog", name="记录详情", exact=True).get_by_role("button", name="Close this dialog").click()
        checks.append("audit-detail")
        page.get_by_role("button", name="主机管理", exact=True).click()
        page.get_by_role("button", name="新增主机", exact=True).first.click()
        dialog = page.get_by_role("dialog", name="新增主机", exact=True)
        assert dialog.get_by_label("IP / 域名", exact=True).is_visible()
        project_root = dialog.get_by_role("textbox", name="项目目录", exact=True)
        dialog.get_by_role("tab", name="项目目录", exact=True).click()
        assert project_root.is_visible()
        assert project_root.evaluate("element => element.closest('details') === null")
        dialog.get_by_role("tab", name="连接设置", exact=True).click()
        assert dialog.get_by_label("主机指纹", exact=True).count() == 0
        assert dialog.get_by_label("凭据修订", exact=True).count() == 0
        assert dialog.get_by_role("button", name="添加主机", exact=True).is_disabled()
        screenshot_ready(dialog)
        dialog.screenshot(path=str(output / "host-form-wide.png"), animations="disabled")
        page.set_viewport_size({"width": 390, "height": 844})
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
        screenshot_ready(dialog)
        dialog.screenshot(path=str(output / "host-form-narrow.png"), animations="disabled")
        page.set_viewport_size({"width": 1440, "height": 900})
        dialog.locator(".advanced-fields summary").click()
        dialog.get_by_label("连接方式", exact=True).select_option("LOCAL")
        dialog.get_by_label("主机名称", exact=True).fill("浏览器测试主机")
        dialog.get_by_role("tab", name="项目目录", exact=True).click()
        dialog.get_by_role("textbox", name="项目目录", exact=True).fill("/opt/browser-target")
        dialog.get_by_role("button", name="添加主机", exact=True).click()
        dialog.wait_for(state="hidden")
        page.get_by_text("浏览器测试主机", exact=True).wait_for()
        assert page.locator(".host-table th").first.inner_text() == "主机状态"
        assert page.locator(".host-status .state-tag").inner_text() == "未检查"
        page.get_by_role("button", name="检查能力", exact=True).click()
        detail = page.get_by_role("dialog", name="浏览器测试主机", exact=True)
        detail.wait_for()
        assert detail.locator(".capability-details article").count() == 4
        assert detail.locator(".detail-status .state-tag").inner_text() == "已连接"
        detail.get_by_role("tab", name="项目目录", exact=True).click()
        detail.locator("code:visible").filter(has_text="/opt/browser-target").first.wait_for()
        detail.get_by_role("tab", name="能力检查", exact=True).click()
        assert detail.locator(".capability-details article").count() == 4
        detail.get_by_text("本机连通", exact=True).wait_for()
        detail.get_by_role("button", name="进入维护", exact=True).click()
        page.locator(".el-message-box").get_by_role("button", name="进入维护", exact=True).click()
        detail.get_by_text("维护中", exact=True).wait_for()
        detail.get_by_role("button", name="退出维护", exact=True).click()
        detail.get_by_role("button", name="进入维护", exact=True).wait_for()
        detail.get_by_role("button", name="编辑连接", exact=True).click()
        dialog = page.get_by_role("dialog", name="维护主机连接", exact=True)
        dialog.get_by_role("tab", name="项目目录", exact=True).click()
        assert dialog.get_by_role("textbox", name="项目目录", exact=True).input_value() == "/opt/browser-target"
        dialog.get_by_role("tab", name="连接设置", exact=True).click()
        dialog.get_by_label("主机名称", exact=True).fill("浏览器测试主机-已更新")
        dialog.get_by_role("tab", name="项目目录", exact=True).click()
        dialog.get_by_role("textbox", name="项目目录", exact=True).fill("/opt/browser-target-edited")
        dialog.get_by_role("button", name="保存连接", exact=True).click()
        dialog.wait_for(state="hidden")
        detail = page.get_by_role("dialog", name="浏览器测试主机-已更新", exact=True)
        detail.get_by_role("tab", name="连接信息", exact=True).click()
        detail.get_by_text("/opt/browser-target-edited", exact=True).wait_for()
        detail.get_by_role("button", name="移除登记", exact=True).click()
        page.locator(".el-message-box").get_by_role("button", name="移除登记", exact=True).click()
        detail.wait_for(state="hidden")
        page.get_by_text("尚未添加主机", exact=True).wait_for()
        page.get_by_role("button", name="新增主机", exact=True).first.click()
        dialog = page.get_by_role("dialog", name="新增主机", exact=True)
        dialog.locator(".advanced-fields summary").click()
        dialog.get_by_label("连接方式", exact=True).select_option("LOCAL")
        dialog.get_by_label("主机名称", exact=True).fill("浏览器测试主机")
        dialog.get_by_role("tab", name="项目目录", exact=True).click()
        dialog.get_by_role("textbox", name="项目目录", exact=True).fill("/opt/browser-target")
        dialog.get_by_role("button", name="添加主机", exact=True).click()
        dialog.wait_for(state="hidden")
        page.get_by_text("浏览器测试主机", exact=True).wait_for()
        search_box = page.get_by_label("搜索主机名称或地址", exact=True).bounding_box()
        filter_box = page.get_by_label("筛选主机状态", exact=True).bounding_box()
        assert search_box and filter_box and abs(search_box["y"] - filter_box["y"]) <= 1 and abs(search_box["height"] - filter_box["height"]) <= 1
        page.get_by_label("搜索主机名称或地址", exact=True).fill("不存在的主机")
        page.get_by_text("没有匹配的主机", exact=True).wait_for()
        page.get_by_label("搜索主机名称或地址", exact=True).fill("")
        screenshot_ready()
        page.screenshot(path=str(output / "hosts-wide.png"), full_page=True, animations="disabled")
        checks.extend(["F01-host-normal-fields", "F01-host-state-first-column", "F01-host-four-capabilities", "F01-host-maintenance", "F01-host-edit", "F01-unreferenced-host-remove", "F01-host-filter", "F01-host-controls-aligned", "F02-project-directory-visible", "F02-project-directory-saved-and-editable"])
        page.get_by_role("button", name="软件管理", exact=True).click()
        page.get_by_text("尚未添加软件", exact=True).wait_for()
        assert page.get_by_role("tab", name="软件中心", exact=True).get_attribute("aria-selected") == "true"
        page.get_by_role("button", name="自动识别", exact=True).click()
        discovery_dialog = page.get_by_role("dialog", name="自动识别软件", exact=True)
        discovery_dialog.get_by_text("选择主机，识别已有软件", exact=True).wait_for()
        assert discovery_dialog.get_by_label("识别主机", exact=True).input_value()
        screenshot_ready(discovery_dialog)
        host_box = discovery_dialog.get_by_label("识别主机", exact=True).bounding_box()
        scan_box = discovery_dialog.get_by_role("button", name="开始识别", exact=True).bounding_box()
        assert host_box and scan_box and abs(host_box["height"] - scan_box["height"]) <= 1 and abs(host_box["y"] - scan_box["y"]) <= 1
        with page.expect_response(lambda response: response.url.endswith("/software-discovery")) as discovery_response:
            discovery_dialog.get_by_role("button", name="开始识别", exact=True).click()
        result = discovery_response.value
        assert result.request.post_data_json["expectedVersion"]
        if platform.system() == "Windows":
            assert result.status == 409 and result.json()["code"] == "LINUX_REQUIRED"
            discovery_dialog.get_by_role("alert").wait_for()
            assert discovery_dialog.locator(".discovery-table tbody tr").count() == 0
            checks.append("F02-discovery-Windows-not-Linux-claim")
        else:
            assert result.status == 200, result.text()
        screenshot_ready(discovery_dialog)
        discovery_dialog.screenshot(path=str(output / "software-discovery-wide.png"), animations="disabled")
        page.set_viewport_size({"width": 390, "height": 844})
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
        screenshot_ready(discovery_dialog)
        discovery_dialog.screenshot(path=str(output / "software-discovery-narrow.png"), animations="disabled")
        page.set_viewport_size({"width": 1440, "height": 900})
        discovery_dialog.get_by_role("button", name="Close this dialog").click()
        page.get_by_text("尚未添加软件", exact=True).wait_for()
        checks.extend(["F02-discovery-explicit-scan", "F02-discovery-host-version-bound", "F02-discovery-no-automatic-registration", "F02-discovery-controls-aligned-and-narrow"])
        # Only discovery responses below are UI fixtures. Registration and
        # unmanagement continue to call the real binary in isolated state.
        discovery_route = "**/ops/api/v1/hosts/*/software-discovery"
        fixture_name = {"stale": False}
        pending_discovery = []
        hold_discovery = {"enabled": False}

        def discovery_fixture(route):
            request = route.request
            if hold_discovery["enabled"]:
                pending_discovery.append(route)
                return
            fulfil_discovery(route)

        def fulfil_discovery(route):
            request = route.request
            route.fulfill(status=200, json={"code": "SUCCESS", "data": {
                "hostId": request.url.split("/hosts/")[1].split("/")[0],
                "hostVersion": request.post_data_json["expectedVersion"],
                "observedAt": "2026-10-20T08:00:00Z", "limited": True,
                "checks": [{"source": "SYSTEMD", "status": "AVAILABLE", "code": "SOFTWARE_SOURCE_PARTIAL"},
                           {"source": "DOCKER", "status": "AVAILABLE", "code": "SOFTWARE_SOURCE_OK"},
                           {"source": "TOOLS", "status": "AVAILABLE", "code": "SOFTWARE_SOURCE_OK"},
                           {"source": "CONFIG", "status": "AVAILABLE", "code": "CONFIG_PARTIAL_READ"}],
                "candidates": [
                    {"name": "过期识别结果" if fixture_name["stale"] else "识别的 MySQL",
                     "softwareType": "MYSQL", "runtimeKind": "SYSTEMD", "locator": "mysql.service",
                     "state": "RUNNING", "version": "8.0.43", "versionSource": "PACKAGE", "packageName": "mysql-server-8.0", "serviceFile": "/lib/systemd/system/mysql.service", "executablePath": "/usr/sbin/mysqld", "listeningPorts": ["0.0.0.0:3306/tcp"], "ports": ["3306/tcp"], "configPaths": []},
                    {"name": "识别的 Redis", "softwareType": "REDIS", "runtimeKind": "DOCKER", "locator": "a" * 64,
                     "state": "STOPPED", "version": "redis:7", "ports": ["127.0.0.1:6379->6379/tcp"], "configPaths": []},
                    {"name": "识别的 Python", "softwareType": "PYTHON", "runtimeKind": "TOOL", "locator": "python",
                     "state": "INSTALLED", "version": "Python 3.12.8", "ports": [], "configPaths": []}],
                "configurations": [{"softwareType": "MYSQL", "path": "/etc/mysql/my.cnf", "port": 3306, "bindAddress": "127.0.0.1"}]}})

        page.route(discovery_route, discovery_fixture)
        page.get_by_role("button", name="自动识别", exact=True).click()
        discovery_dialog = page.get_by_role("dialog", name="自动识别软件", exact=True)
        discovery_dialog.get_by_role("button", name="开始识别", exact=True).click()
        discovery_dialog.locator(".discovery-table").wait_for()
        assert discovery_dialog.locator(".discovery-table th").first.inner_text() == "当前状态"
        discovery_dialog.get_by_text("本次结果不完整", exact=False).wait_for()
        discovery_dialog.locator(".discovery-partial details summary").click()
        discovery_dialog.get_by_text("SOFTWARE_SOURCE_PARTIAL", exact=True).wait_for()
        discovery_dialog.get_by_text("CONFIG_PARTIAL_READ", exact=True).wait_for()
        discovery_dialog.locator(".host-configurations summary").click()
        discovery_dialog.get_by_text("/etc/mysql/my.cnf", exact=True).wait_for()
        mysql_row = discovery_dialog.locator("tr").filter(has=page.get_by_text("识别的 MySQL", exact=True))
        mysql_row.get_by_role("button", name="详情", exact=True).click()
        discovery_dialog.get_by_text("3306/tcp", exact=True).first.wait_for()
        discovery_dialog.get_by_text("8.0.43", exact=True).wait_for()
        discovery_dialog.get_by_text("安装包版本", exact=True).wait_for()
        discovery_dialog.get_by_text("/usr/sbin/mysqld", exact=True).wait_for()
        discovery_dialog.get_by_text("/lib/systemd/system/mysql.service", exact=True).wait_for()
        screenshot_ready(discovery_dialog)
        discovery_dialog.screenshot(path=str(output / "software-discovery-fixture-wide.png"), animations="disabled")
        page.set_viewport_size({"width": 390, "height": 844})
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
        assert discovery_dialog.locator(".discovery-table").evaluate("el => el.scrollWidth <= el.clientWidth + 1")
        screenshot_ready(discovery_dialog)
        discovery_dialog.screenshot(path=str(output / "software-discovery-fixture-narrow.png"), animations="disabled")
        page.set_viewport_size({"width": 1440, "height": 900})
        checks.extend(["F02-version-source-not-runtime-claim", "F02-package-binary-unit-evidence", "F02-discovery-no-inner-horizontal-scroll"])
        discovery_dialog.get_by_role("button", name="Close this dialog").click()

        for software_name, kind, software_type, locator in [
            ("识别的 MySQL", "SYSTEMD", "MYSQL", "mysql.service"),
            ("识别的 Redis", "DOCKER", "REDIS", "a" * 64),
            ("识别的 Python", "TOOL", "PYTHON", "python"),
        ]:
            page.get_by_role("button", name="自动识别", exact=True).click()
            discovery_dialog = page.get_by_role("dialog", name="自动识别软件", exact=True)
            discovery_dialog.get_by_role("button", name="开始识别", exact=True).click()
            candidate_row = discovery_dialog.locator("tr").filter(has=page.get_by_text(software_name, exact=True))
            candidate_row.get_by_role("button", name="登记", exact=True).click()
            dialog = page.get_by_role("dialog", name="登记已有软件", exact=True)
            assert dialog.get_by_label("软件名称", exact=True).input_value() == software_name
            assert dialog.get_by_label("运行方式", exact=True).input_value() == kind
            assert dialog.get_by_label("软件类型", exact=True).input_value() == software_type
            if kind == "SYSTEMD":
                assert dialog.get_by_label("systemd unit", exact=True).input_value() == locator
            elif kind == "DOCKER":
                assert dialog.get_by_label("容器名称 / ID", exact=True).input_value() == locator
                dialog.locator(".software-advanced summary").click()
                assert dialog.get_by_label("标记版本", exact=True).input_value() == ""
            else:
                assert dialog.locator(".tool-command code").inner_text() == locator
            with page.expect_response(lambda response: response.url.endswith("/software-instances") and response.request.method == "POST") as registration_response:
                dialog.get_by_role("button", name="只读登记", exact=True).click()
            assert registration_response.value.status == 201
            detail = page.get_by_role("dialog", name=software_name, exact=True)
            detail.get_by_role("button", name="解除管理", exact=True).click()
            page.locator(".el-message-box").get_by_role("button", name="解除管理", exact=True).click()
            detail.get_by_text("已解除", exact=True).first.wait_for()
            detail.get_by_role("button", name="Close this dialog").click()

        # Hold a response, close and reopen, then release it. No old results
        # may appear in the new dialog generation.
        hold_discovery["enabled"] = True
        fixture_name["stale"] = True
        page.get_by_role("button", name="自动识别", exact=True).click()
        discovery_dialog = page.get_by_role("dialog", name="自动识别软件", exact=True)
        with page.expect_request(lambda request: request.url.endswith("/software-discovery")):
            discovery_dialog.get_by_role("button", name="开始识别", exact=True).click()
        discovery_dialog.get_by_role("button", name="Close this dialog").click()
        page.get_by_role("button", name="自动识别", exact=True).click()
        discovery_dialog = page.get_by_role("dialog", name="自动识别软件", exact=True)
        assert pending_discovery
        fulfil_discovery(pending_discovery.pop())
        hold_discovery["enabled"] = False
        discovery_dialog.get_by_role("button", name="开始识别", exact=True).wait_for()
        assert discovery_dialog.get_by_text("过期识别结果", exact=True).count() == 0
        discovery_dialog.get_by_text("选择主机，识别已有软件", exact=True).wait_for()
        discovery_dialog.get_by_role("button", name="Close this dialog").click()
        page.unroute(discovery_route, discovery_fixture)
        checks.extend(["F02-discovery-UI-fixture-state-first-column", "F02-discovery-UI-fixture-config-scope", "F02-discovery-UI-fixture-partial-source", "F02-discovery-UI-fixture-prefill-three-kinds-real-registration", "F02-discovery-UI-fixture-closed-response-discarded"])
        page.get_by_role("button", name="登记已有", exact=True).first.click()
        dialog = page.get_by_role("dialog", name="登记已有软件", exact=True)
        dialog.get_by_label("软件名称", exact=True).fill("已有 Java 工具")
        dialog.get_by_label("运行方式", exact=True).select_option("TOOL")
        dialog.get_by_label("软件类型", exact=True).select_option("JAVA")
        assert dialog.get_by_label("容器名称 / ID", exact=True).count() == 0
        assert dialog.get_by_label("systemd unit", exact=True).count() == 0
        assert dialog.get_by_role("button", name="只读登记", exact=True).is_enabled()
        screenshot_ready(dialog)
        dialog.screenshot(path=str(output / "software-form-wide.png"), animations="disabled")
        page.set_viewport_size({"width": 390, "height": 844})
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
        screenshot_ready(dialog)
        dialog.screenshot(path=str(output / "software-form-narrow.png"), animations="disabled")
        page.set_viewport_size({"width": 1440, "height": 900})
        dialog.get_by_role("button", name="只读登记", exact=True).click()
        dialog.wait_for(state="hidden")
        detail = page.get_by_role("dialog", name="已有 Java 工具", exact=True)
        detail.wait_for()
        assert detail.get_by_role("button", name="启动", exact=True).count() == 0
        assert detail.get_by_role("button", name="停止", exact=True).count() == 0
        assert detail.get_by_role("button", name="接管生命周期", exact=True).count() == 0
        with page.expect_response(lambda response: response.request.method == "POST" and response.url.endswith("/observe")) as observation_response:
            detail.get_by_role("button", name="检查状态", exact=True).click()
        observed = observation_response.value.json()["data"]
        detail.get_by_role("button", name="检查状态", exact=True).wait_for()
        if observed["latestAttempt"]["code"] == "READY":
            detail.get_by_text("观测有效", exact=False).wait_for()
        else:
            detail.get_by_text("当前无法确认", exact=False).wait_for()
            detail.get_by_text(observed["latestAttempt"]["code"], exact=True).first.wait_for()
        detail.get_by_role("button", name="Close this dialog").click()
        assert page.locator(".software-card-head .state-tag").count() == 1
        assert page.locator(".software-cards .state-tag").inner_text() != "运行中"
        search_box = page.get_by_label("搜索软件或服务标识", exact=True).bounding_box()
        host_filter = page.get_by_label("筛选软件主机", exact=True).bounding_box()
        assert search_box and host_filter and abs(search_box["height"] - host_filter["height"]) <= 1 and abs(search_box["y"] - host_filter["y"]) <= 1
        page.get_by_label("搜索软件或服务标识", exact=True).fill("不存在的软件")
        page.get_by_text("没有匹配的软件", exact=True).wait_for()
        page.get_by_label("搜索软件或服务标识", exact=True).fill("")
        screenshot_ready()
        page.screenshot(path=str(output / "software-wide.png"), full_page=True, animations="disabled")
        page.locator(".software-cards").get_by_role("button", name="详情", exact=True).click()
        detail = page.get_by_role("dialog", name="已有 Java 工具", exact=True)
        detail.get_by_role("button", name="解除管理", exact=True).click()
        page.locator(".el-message-box").get_by_role("button", name="解除管理", exact=True).click()
        detail.get_by_text("已解除", exact=True).first.wait_for()
        detail.get_by_role("button", name="Close this dialog").click()
        page.get_by_text("没有匹配的软件", exact=True).wait_for()
        page.get_by_label("筛选管理模式", exact=True).select_option("RELEASED")
        page.locator(".software-cards").get_by_text("已有 Java 工具", exact=True).wait_for()
        checks.extend(["F02-software-default-management", "F02-register-readonly-tool", "F02-tool-no-lifecycle-controls", "F02-tool-observe-no-running-claim", "F02-card-status-first", "F02-controls-aligned", "F02-software-filter", "F02-unmanage-keeps-history"])
        page.get_by_role("tab", name="软件目录", exact=True).click()
        page.locator(".software-recipes").wait_for()
        page.wait_for_function("() => document.querySelectorAll('.plugin-card').length === 12")
        assert page.get_by_label("目标主机", exact=True).input_value()
        assert page.get_by_role("heading", name="Maven", exact=True).count() == 1
        assert page.get_by_role("button", name="安装", exact=True).count() == 12
        java_version = page.get_by_label("Java版本", exact=True)
        assert java_version.locator("option").count() == 2
        java_version.select_option("17")
        assert java_version.input_value() == "17"
        page.get_by_role("button", name="构建工具", exact=True).click()
        assert page.locator(".plugin-card").count() == 1
        page.get_by_role("heading", name="Maven", exact=True).wait_for()
        page.get_by_role("button", name="全部软件", exact=True).click()
        page.get_by_label("搜索安装软件", exact=True).fill("17")
        assert page.locator(".plugin-card").count() == 1
        page.get_by_label("搜索安装软件", exact=True).fill("no-such-software")
        page.get_by_text("没有匹配的软件", exact=True).wait_for()
        page.get_by_label("搜索安装软件", exact=True).fill("")
        checks.extend(["UI01-catalog-grouped-software", "UI01-version-select", "UI01-category-filter", "UI01-catalog-search"])
        for tool_name in ("Java", "Maven"):
            tool_card = page.locator(".plugin-card").filter(has=page.get_by_role("heading", name=tool_name, exact=True))
            assert tool_card.get_by_role("button", name="安装", exact=True).is_enabled()
        assert page.get_by_label("宿主端口", exact=True).count() == 0
        assert page.get_by_label("控制器名称", exact=True).count() == 0
        page.wait_for_function("() => document.querySelectorAll('.el-message').length === 0")
        screenshot_ready()
        page.screenshot(path=str(output / "plugins-wide.png"), full_page=True, animations="disabled")
        checks.extend(["I02-P1-catalog-seven-tool-versions-and-six-services", "I02-P1-tools-can-open-install-preflight-with-diagnostics", "I02-P1-service-port-form-only-inside-installation-drawer"])
        page.get_by_role("button", name="应用实例", exact=True).click()
        page.locator(".dependency-section > summary").click()
        page.get_by_role("button", name="绑定已有服务", exact=True).click()
        dialog = page.get_by_role("dialog", name="绑定已有依赖", exact=True)
        dialog.get_by_label("类型", exact=True).select_option("REDIS")
        dialog.get_by_label("地址", exact=True).fill("127.0.0.1")
        dialog.get_by_role("button", name="登记绑定", exact=True).click()
        dialog.wait_for(state="hidden")
        page.get_by_role("cell", name="REDIS", exact=True).wait_for()
        source = page.get_by_label("检查来源：127.0.0.1", exact=True)
        source.select_option(index=1)
        assert page.get_by_label("实例网络：127.0.0.1", exact=True).input_value() == ""
        page.get_by_role("button", name="TCP 检查", exact=True).click()
        page.locator(".probe-result summary").wait_for()
        page.locator(".probe-result summary").click()
        assert page.get_by_text("未验证", exact=True).count() == 2
        checks.append("UX-OPS-dependency-host-only-does-not-claim-authentication")
        page.get_by_role("button", name="应用实例", exact=True).click()
        page.get_by_role("button", name="新增实例", exact=True).click()
        dialog = page.get_by_role("dialog", name="实例配置草稿", exact=True)
        dialog.get_by_label("实例名称", exact=True).fill("视频流后端")
        dialog.get_by_label("环境标签", exact=True).fill("test")
        dialog.get_by_label("Spring profile", exact=True).fill("dev")
        dialog.get_by_role("button", name="保存草稿", exact=True).click()
        dialog.wait_for(state="hidden")
        page.get_by_role("cell").filter(has_text="视频流后端").first.wait_for()
        page.get_by_role("button", name="配置", exact=True).click()
        dialog = page.get_by_role("dialog", name="实例配置草稿", exact=True)
        assert dialog.get_by_label("Spring profile", exact=True).input_value() == "dev"
        dialog.get_by_role("button", name="Close this dialog").click()
        page.get_by_role("button", name="版本", exact=True).click()
        page.get_by_text("暂无版本", exact=True).wait_for()
        page.get_by_role("dialog").get_by_role("button", name="Close this dialog").click()
        checks.extend(["I02-target-register", "I02-external-binding", "I02-instance-draft-and-reload", "I02-empty-release-history"])
        page.get_by_role("button", name="版本制品", exact=True).click()
        page.get_by_role("button", name="导入制品", exact=True).click()
        dialog = page.get_by_role("dialog", name="导入制品", exact=True)
        dialog.get_by_label("来源", exact=True).select_option("JAR")
        assert dialog.get_by_label("配方", exact=True).is_disabled()
        assert dialog.get_by_label("Java21 运行环境", exact=True).locator("option").count() == 1
        screenshot_ready(dialog)
        dialog.screenshot(path=str(output / "jar-import-wide.png"), animations="disabled")
        dialog.get_by_label("来源", exact=True).select_option("DOCKER_ARCHIVE")
        assert dialog.get_by_label("Java21 运行环境", exact=True).count() == 0
        dialog.get_by_role("button", name="Close this dialog").click()
        csrf = page.request.get(url + "/ops/api/v1/auth/csrf").json()["data"]["csrfToken"]
        queued = page.request.post(url + "/ops/api/v1/artifact-imports", headers={"Origin": url, "X-CSRF-Token": csrf}, data={"name":"browser-cancel-input", "archiveSha256":"a"*64, "byteSize":1, "idempotencyKey":"browser-cancel-"+secrets.token_hex(8)})
        assert queued.status == 202, queued.text()
        page.get_by_role("button", name="发布作业", exact=True).click()
        page.get_by_role("button", name="取消排队", exact=True).click()
        page.get_by_role("cell", name="已取消", exact=True).wait_for()
        assert page.get_by_role("button", name="取消排队", exact=True).count() == 0
        checks.extend(["UX-OPS-JAR-fixed-recipe-ready-Java21-only", "UX-OPS-complete-image-no-JRE-override", "UX-OPS-real-queued-import-cancel"])
        page.get_by_role("button", name="系统设置", exact=True).click()
        page.get_by_role("button", name="修改密码", exact=True).click()
        dialog = page.get_by_role("dialog", name="修改密码", exact=True)
        changed = "Changed-" + secrets.token_hex(12)
        dialog.get_by_label("当前密码", exact=True).fill(password)
        dialog.get_by_label("新密码", exact=True).fill(changed)
        dialog.get_by_label("确认新密码", exact=True).fill(changed)
        dialog.get_by_role("button", name="修改并重新登录", exact=True).click()
        page.get_by_role("heading", name="控制器登录", exact=True).wait_for()
        page.get_by_label("密码", exact=True).fill(changed)
        page.get_by_role("button", name="登录", exact=True).click()
        page.get_by_role("button", name="系统设置", exact=True).click()
        page.get_by_label("控制器名称", exact=True).wait_for()
        assert page.get_by_label("控制器名称", exact=True).input_value() == "测试控制器"
        page.get_by_role("button", name="运行总览", exact=True).click()
        page.get_by_role("heading", name="运行总览", exact=True).wait_for()
        checks.append("password-change-relogin")
        page.set_viewport_size({"width": 390, "height": 844})
        for name in ["运行总览", "主机管理", "软件管理", "应用实例", "版本制品", "发布作业", "连接凭据", "操作记录", "系统设置"]:
            page.get_by_role("button", name="展开导航", exact=True).click()
            page.get_by_role("button", name=name, exact=True).click()
            if name == "主机管理":
                page.get_by_text("浏览器测试主机", exact=True).wait_for()
                screenshot_ready()
                page.screenshot(path=str(output / "hosts-narrow.png"), full_page=True, animations="disabled")
                page.get_by_role("button", name="详情", exact=True).first.click()
                detail = page.get_by_role("dialog", name="浏览器测试主机", exact=True)
                screenshot_ready(detail)
                assert detail.locator(".host-detail, .host-detail .el-tabs, .host-detail .el-tabs__content").evaluate_all("elements => elements.every(e => { const r=e.getBoundingClientRect(); return r.left >= -1 && r.right <= innerWidth+1; })")
                detail.get_by_role("button", name="Close this dialog").click()
                detail.wait_for(state="hidden")
                checks.append("host-detail-internal-tabs-within-390px")
            if name == "软件管理":
                page.get_by_label("筛选管理模式", exact=True).select_option("RELEASED")
                page.locator(".software-cards").get_by_text("已有 Java 工具", exact=True).wait_for()
                screenshot_ready()
                page.screenshot(path=str(output / "software-narrow.png"), full_page=True, animations="disabled")
                page.locator(".software-card").filter(has=page.get_by_text("已有 Java 工具", exact=True)).get_by_role("button", name="详情", exact=True).click()
                detail = page.get_by_role("dialog", name="已有 Java 工具", exact=True)
                screenshot_ready(detail)
                assert detail.locator(".software-detail, .software-detail .el-tabs, .software-detail .el-tabs__content").evaluate_all("elements => elements.every(e => { const r=e.getBoundingClientRect(); return r.left >= -1 && r.right <= innerWidth+1; })")
                detail.get_by_role("button", name="Close this dialog").click()
                detail.wait_for(state="hidden")
                checks.append("software-detail-internal-tabs-within-390px")
                page.get_by_role("tab", name="软件目录", exact=True).click()
                page.locator(".software-recipes").wait_for()
                page.wait_for_function("() => document.querySelectorAll('.plugin-card').length === 12")
                page.wait_for_function("() => document.querySelectorAll('.el-message').length === 0")
                screenshot_ready()
                page.screenshot(path=str(output / "plugins-narrow.png"), full_page=True, animations="disabled")
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), name
        page.get_by_role("button", name="展开导航", exact=True).click()
        page.get_by_role("button", name="运行总览", exact=True).click()
        page.wait_for_function("() => document.querySelectorAll('.el-message').length === 0")
        screenshot_ready()
        page.screenshot(path=str(output / "overview-narrow.png"), full_page=True, animations="disabled")
        checks.append("nine-pages-narrow-no-overflow")
        page.get_by_role("button", name="退出", exact=True).click()
        page.get_by_role("heading", name="控制器登录", exact=True).wait_for()
        checks.append("logout")
        browser.close()
    assert not errors, errors
    result = {"passed": checks, "pageErrors": errors, "backend": "real binary / isolated state", "browserUIFixtures": ["software-discovery candidate responses only; registration and unmanage use the real API"], "productionAcceptance": False}
    (output / "result.json").write_text(json.dumps(result, indent=2, ensure_ascii=False))
    print(json.dumps(result, ensure_ascii=False))
finally:
    process.terminate()
    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)
    stderr.close()
