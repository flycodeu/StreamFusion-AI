"""Browser acceptance for Ops, including F01 hosts and F02 software. Isolated state only."""
import argparse
import json
import pathlib
import platform
import re
import secrets
import socket
import sqlite3
import subprocess
import tarfile
import time
import urllib.request
from playwright.sync_api import expect, sync_playwright

parser = argparse.ArgumentParser()
parser.add_argument("--binary", required=True)
parser.add_argument("--output", required=True)
parser.add_argument("--browser-executable", help="已有Chromium可执行文件；默认使用Playwright匹配版本")
parser.add_argument("--error-docs-only", action="store_true", help="仅验证系统设置的错误说明文档")
parser.add_argument("--settings-only", action="store_true", help="仅验证系统设置与运行监控")
parser.add_argument("--logs-only", action="store_true", help="仅验证执行记录分页与原位日志弹窗")
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
        if args.logs_only:
            # Terminal history fixtures live only in this script's disposable
            # state. Every row is already SUCCEEDED; none is a queued operation.
            host_id = "11111111-1111-4111-8111-111111111114"
            software_id = "22222222-2222-4222-8222-222222222214"
            stamp = "2026-11-09T12:00:00Z"
            job_id = lambda index: f"00000000-0000-4000-8000-{index:012d}"
            latest = job_id(126)
            with sqlite3.connect(state / "controller.sqlite") as db:
                admin = db.execute("SELECT id FROM admins LIMIT 1").fetchone()[0]
                db.execute("INSERT INTO targets(id,name,install_root,kind,address,created_at) VALUES(?,?,?,'LOCAL','127.0.0.1',?)", (host_id, "日志测试主机", str(output / "fixture-target"), stamp))
                db.execute("INSERT INTO software_instances(id,name,host_id,software_type,software_version,runtime_kind,locator,management_mode,desired_state,actual_state,last_job_id,created_at,updated_at) VALUES(?,?,?,'OTHER','1.0','SYSTEMD','log-fixture.service','OBSERVE','UNCHANGED','STOPPED',?,?,?)", (software_id, "日志测试软件", host_id, latest, stamp, stamp))
                for index in range(127):
                    raw = json.dumps({"software": {"id": software_id}, "installation": {"softwareId": software_id, "config": {"name": f"分页测试 {index:03d}"}}, "targetId": host_id, "secrets": {"password": "history-fixture-secret-never-render"}})
                    db.execute("INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'SOFTWARE_START',?,?,?,'SUCCEEDED','SOFTWARE_STATE_VERIFIED',?,'logs-fixture',?,?)", (job_id(index), admin, host_id, job_id(index), job_id(index), raw, stamp, stamp))
            page.get_by_role("button", name="日志记录", exact=True).click()
            logs = page.locator('section[aria-label="执行记录"]')
            records = logs.get_by_label("日志列表", exact=True)
            expect(records.get_by_role("button")).to_have_count(20)
            expect(records.get_by_role("button").first).to_have_attribute("aria-label", "分页测试 126")
            expect(logs.locator(".receipt-summary")).to_contain_text("已完成")
            expect(logs.locator(".execution-terminal")).to_have_count(0)
            expect(logs.get_by_text("SOFTWARE_STATE_VERIFIED", exact=True)).not_to_be_visible()
            assert "history-fixture-secret-never-render" not in logs.inner_text()
            checks.append("real-history-first-page-and-receipt-card-hide-raw-codes-and-private-input")
            for index in range(6):
                logs.get_by_role("button", name="下一页", exact=True).click()
                expect(logs.get_by_text(f"第 {index+2} 页", exact=True)).to_be_visible()
                expect(records.get_by_role("button").first).to_have_attribute("aria-label", f"分页测试 {106-index*20:03d}")
            expect(records.get_by_role("button")).to_have_count(7)
            expect(logs.get_by_role("button", name="下一页", exact=True)).to_be_disabled()
            expect(records.get_by_role("button").last).to_have_attribute("aria-label", "分页测试 000")
            logs.get_by_role("button", name="上一页", exact=True).click()
            expect(records.get_by_role("button")).to_have_count(20)
            checks.append("cursor-pagination-reaches-all-127-same-timestamp-records-and-returns")
            logs.get_by_label("搜索日志", exact=True).fill("分页测试 005")
            expect(records.get_by_role("button")).to_have_count(1)
            expect(records.get_by_role("button").first).to_have_attribute("aria-label", "分页测试 005")
            expect(logs.get_by_text("第 1 页", exact=True)).to_be_visible()
            logs.get_by_label("搜索日志", exact=True).fill("__NO_LOG_MATCH__")
            expect(logs.get_by_text("没有匹配的日志", exact=True)).to_be_visible()
            expect(logs.locator(".receipt-summary")).to_have_count(0)
            logs.get_by_label("搜索日志", exact=True).fill("")
            expect(records.get_by_role("button")).to_have_count(20)
            logs.get_by_label("日志每页条数", exact=True).select_option("50")
            expect(records.get_by_role("button")).to_have_count(50)
            checks.append("server-search-before-pagination-clears-old-selection-and-page-size-resets-cursor")
            page.get_by_role("button", name="软件管理", exact=True).click()
            card = page.locator("article.software-card").filter(has=page.get_by_role("heading", name="日志测试软件", exact=True))
            card.get_by_role("button", name=re.compile("^(详情|管理)$")).first.click()
            detail = page.get_by_role("dialog", name="日志测试软件", exact=True)
            detail.get_by_role("tab", name="执行记录", exact=True).click()
            detail.get_by_role("button", name="查看日志", exact=True).click()
            detail.wait_for(state="hidden")
            modal = page.get_by_role("dialog", name="执行日志", exact=True)
            expect(modal).to_be_visible()
            expect(page.get_by_role("heading", name="软件管理", exact=True)).to_be_visible()
            expect(modal.locator(".receipt-summary")).to_contain_text("已完成")
            expect(modal.locator(".execution-terminal")).to_have_count(0)
            for width, height in [(1440, 900), (390, 500)]:
                page.set_viewport_size({"width": width, "height": height})
                screenshot_ready(modal)
                page.screenshot(path=str(output / f"execution-log-modal-{width}.png"), animations="disabled")
                assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
                assert modal.evaluate("el => {const r=el.getBoundingClientRect();return r.left>=0&&r.right<=innerWidth+1&&r.top>=0&&r.bottom<=innerHeight+1}"), (width, height, modal.bounding_box())
            page.mouse.click(3, 3)
            modal.wait_for(state="hidden")
            expect(page.get_by_role("heading", name="软件管理", exact=True)).to_be_visible()
            checks.append("software-log-opens-one-responsive-modal-without-changing-page-and-outside-closes")
            page.set_viewport_size({"width": 1440, "height": 900})
            initial_job = page.request.get(url + "/ops/api/v1/jobs/" + latest).json()["data"]
            log_reads, held_reads = [], []
            hold = {"logs": False}
            def running_job(route):
                route.fulfill(json={"data": {**initial_job, "status": "RUNNING"}})
            def execution_output(route):
                log_reads.append(route.request.url)
                if hold["logs"]:
                    held_reads.append(route)
                else:
                    route.fulfill(json={"data": {"text": f"测试命令输出 {len(log_reads)}", "source": "TARGET_EXECUTION", "limited": False}})
            page.route("**/ops/api/v1/jobs/" + latest, running_job)
            page.route("**/ops/api/v1/jobs/" + latest + "/logs", execution_output)
            card.get_by_role("button", name=re.compile("^(详情|管理)$")).first.click()
            detail.get_by_role("tab", name="执行记录", exact=True).click()
            detail.get_by_role("button", name="查看日志", exact=True).click()
            expect(modal.get_by_label("执行过程输出", exact=True)).to_contain_text("测试命令输出")
            first_reads = len(log_reads)
            page.wait_for_timeout(3300)
            assert len(log_reads) > first_reads
            modal.get_by_role("button", name="暂停刷新", exact=True).click()
            paused_reads = len(log_reads)
            page.wait_for_timeout(3300)
            assert len(log_reads) == paused_reads
            modal.get_by_role("button", name="继续刷新", exact=True).click()
            expect(modal.get_by_label("执行过程输出", exact=True)).to_contain_text(f"测试命令输出 {paused_reads+1}")
            checks.append("shared-modal-viewer-polls-real-output-and-preserves-pause-resume")
            modal.get_by_role("button", name="暂停刷新", exact=True).click()
            hold["logs"] = True
            modal.get_by_role("button", name="继续刷新", exact=True).click()
            page.wait_for_function("() => document.querySelector('.software-log-dialog button[disabled]') !== null")
            page.wait_for_timeout(150)
            assert held_reads, "test must hold a dispatched log response before closing"
            modal.get_by_role("button", name="Close this dialog", exact=True).click()
            modal.wait_for(state="hidden")
            hold["logs"] = False
            for old in held_reads:
                old.fulfill(json={"data": {"text": "关闭后的旧输出", "source": "TARGET_EXECUTION"}})
            read_count = len(log_reads)
            page.wait_for_timeout(3300)
            assert len(log_reads) == read_count
            expect(page.get_by_text("关闭后的旧输出", exact=True)).to_have_count(0)
            assert page.request.get(url + "/ops/api/v1/jobs/" + latest).json()["data"]["status"] == "SUCCEEDED"
            checks.append("closing-log-cancels-read-and-drops-late-response-without-changing-underlying-job")
            page.get_by_role("button", name="日志记录", exact=True).click()
            expect(records.get_by_role("button")).to_have_count(20)
            for width, height in [(1440, 900), (390, 900)]:
                page.set_viewport_size({"width": width, "height": height})
                assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
                page.screenshot(path=str(output / f"execution-history-{width}.png"), full_page=True, animations="disabled")
            with sqlite3.connect(state / "controller.sqlite") as db:
                assert db.execute("SELECT count(*) FROM jobs").fetchone()[0] == 127
                assert db.execute("SELECT count(*) FROM jobs WHERE kind='SOFTWARE_INSTALL'").fetchone()[0] == 0
                assert db.execute("SELECT count(*) FROM operation_locks").fetchone()[0] == 0
            assert not errors, errors
            result = {"passed": checks, "pageErrors": errors, "backend": "real binary / disposable state and 127 terminal history rows", "browserUIFixtures": ["running job read and command output only; no target operation"], "remoteInstalls": 0, "productionAcceptance": False}
            (output / "result.json").write_text(json.dumps(result, indent=2, ensure_ascii=False), encoding="utf8")
            print(json.dumps(result, ensure_ascii=False))
            browser.close()
            raise SystemExit(0)
        if args.settings_only:
            before_settings = page.request.get(url + "/ops/api/v1/settings").json()["data"]
            plain_status = page.request.get(url + "/ops/api/v1/status").json()["data"]
            real_status = page.request.get(url + "/ops/api/v1/status?system=1").json()["data"]
            system = real_status["system"]
            assert "system" not in plain_status
            assert system["hostname"] and system["platform"] and system["arch"] and system["observedAt"]
            assert system["cpu"]["cores"] > 0
            for resource in ("memory", "disk"):
                metric = system[resource]
                assert metric["available"] and 0 <= metric["usedBytes"] <= metric["totalBytes"] and metric["totalBytes"] > 0
                assert 0 <= metric["usagePercent"] <= 100
            if platform.system() == "Windows":
                assert system["platform"] == "windows" and "Windows" in system["platformVersion"], {key: system[key] for key in ("platform", "platformVersion")}
            checks.append("authenticated-real-system-metrics-without-expanding-lightweight-status")

            page.get_by_role("button", name="系统设置", exact=True).click()
            settings = page.locator("section.content.settings")
            monitor = settings.get_by_role("region", name="运行监控", exact=True)
            cpu = monitor.get_by_role("article", name="CPU 使用情况", exact=True)
            memory = monitor.get_by_role("article", name="内存 使用情况", exact=True)
            disk = monitor.get_by_role("article", name="数据磁盘 使用情况", exact=True)
            expect(monitor.get_by_text(system["hostname"], exact=True)).to_be_visible()
            expect(settings.locator(".settings-info-card")).to_have_count(2)
            expect(settings.get_by_text("控制器 ID", exact=True)).to_have_count(0)
            expect(page.get_by_label("控制器名称", exact=True)).to_have_count(0)
            expect(settings.get_by_role("button", name="保存名称", exact=True)).to_have_count(0)
            expect(settings.locator(".settings-meta").get_by_text(plain_status["listen"], exact=True)).to_be_visible()
            expect(settings.locator(".settings-meta").get_by_text("v" + plain_status["release"], exact=True)).to_be_visible()
            expect(memory.locator(".resource-value")).to_have_text(re.compile(r"\d+\.\d%"))
            expect(disk.locator(".resource-value")).to_have_text(re.compile(r"\d+\.\d%"))
            if system["cpu"]["usagePercent"] is None:
                page.wait_for_timeout(2100)
                real_status = page.request.get(url + "/ops/api/v1/status?system=1").json()["data"]
                system = real_status["system"]
            assert system["cpu"]["available"] and system["cpu"]["usagePercent"] is not None and 0 <= system["cpu"]["usagePercent"] <= 100
            monitor.get_by_role("button", name="刷新监控", exact=True).click()
            expect(cpu.locator(".resource-value")).to_have_text(re.compile(r"\d+\.\d%"))
            checks.append("settings-has-two-service-cards-real-monitoring-and-no-name-or-controller-id-input")

            for width, height in ((1440, 900), (1000, 800), (390, 900)):
                page.set_viewport_size({"width": width, "height": height})
                screenshot_ready()
                assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), width
                assert settings.locator(".settings-info-card, .resource-card, .setting-action").evaluate_all(
                    "els => els.every(el => { const r=el.getBoundingClientRect(); return r.width>0 && r.left>=0 && r.right<=innerWidth+1; })"
                ), width
                page.screenshot(path=str(output / f"settings-monitor-{width}.png"), full_page=True, animations="disabled")
                checks.append(f"settings-service-monitor-maintenance-layout-{width}")
            page.set_viewport_size({"width": 1440, "height": 900})
            settings.get_by_role("button", name="查看文档", exact=True).click()
            docs = page.get_by_role("dialog", name="错误说明", exact=True)
            expect(docs.get_by_role("article", name="错误说明正文", exact=True)).to_be_visible()
            docs.get_by_role("button", name="Close this dialog", exact=True).click()
            docs.wait_for(state="hidden")
            with page.expect_download() as download:
                settings.get_by_role("button", name="下载备份", exact=True).click()
            backup = output / "settings-backup.tar.gz"
            download.value.save_as(str(backup))
            with tarfile.open(backup) as archive:
                assert "manifest.json" in archive.getnames() and "master.key" not in archive.getnames()
            assert page.request.get(url + "/ops/api/v1/settings").json()["data"] == before_settings
            checks.append("error-document-and-real-backup-remain-usable-without-changing-controller-settings")

            page.clock.install()
            fixture = json.loads(json.dumps(real_status))
            fixture["system"].update({"hostname": "settings-monitor-fixture", "platformVersion": "Windows 11 测试系统", "observedAt": "2026-11-09T10:00:00Z"})
            fixture["system"]["cpu"].update({"available": True, "cores": 8, "model": "Test CPU", "usagePercent": None})
            fixture["system"]["memory"].update({"available": True, "totalBytes": 8 * 1024**3, "usedBytes": 4 * 1024**3, "usagePercent": 50})
            fixture["system"]["disk"].update({"available": True, "path": "D:\\", "totalBytes": 100 * 1024**3, "usedBytes": 25 * 1024**3, "freeBytes": 75 * 1024**3, "usagePercent": 25})
            fixture_mode, monitor_requests, held_monitor = "first", [], []
            monitor_route = "**/ops/api/v1/status?system=1"
            def monitor_fixture(route):
                assert route.request.method == "GET"
                monitor_requests.append(route.request.url)
                if fixture_mode == "hold":
                    held_monitor.append(route)
                elif fixture_mode == "network":
                    route.abort("connectionfailed")
                elif fixture_mode == "unauthorized":
                    route.fulfill(status=401, json={"code": "AUTH_REQUIRED", "msg": "请先登录", "traceId": "settings-auth-fixture", "timestamp": "2026-11-09T10:00:00Z"})
                else:
                    value = json.loads(json.dumps(fixture))
                    if fixture_mode == "zero":
                        value["system"]["cpu"]["usagePercent"] = 0
                    elif fixture_mode == "partial":
                        value["system"]["cpu"]["available"] = False
                        value["system"]["disk"]["available"] = False
                    route.fulfill(status=200, json={"data": value})
            page.route(monitor_route, monitor_fixture)
            refresh = monitor.get_by_role("button", name="刷新监控", exact=True)
            refresh.click()
            expect(cpu.locator(".resource-value")).to_have_text("采集中")
            fixture_mode = "zero"
            refresh.click()
            expect(cpu.locator(".resource-value")).to_have_text("0.0%")
            expect(cpu.get_by_role("progressbar", name="CPU 使用率", exact=True)).to_have_attribute("value", "0")
            checks.append("first-cpu-sample-is-collecting-and-valid-zero-remains-zero")
            fixture_mode = "partial"
            refresh.click()
            expect(cpu.locator(".resource-value")).to_have_text("暂不可用")
            expect(disk.locator(".resource-value")).to_have_text("暂不可用")
            expect(memory.locator(".resource-value")).to_have_text("50.0%")
            checks.append("unavailable-cpu-or-disk-keeps-available-memory-and-service-information")
            old_time = monitor.locator(".monitor-update").inner_text()
            fixture_mode = "network"
            refresh.click()
            expect(monitor.get_by_text("监控暂未更新", exact=True)).to_be_visible()
            expect(memory.locator(".resource-value")).to_have_text("50.0%")
            assert monitor.locator(".monitor-update").inner_text() == old_time
            fixture_mode = "zero"
            refresh.click()
            expect(cpu.locator(".resource-value")).to_have_text("0.0%")
            expect(monitor.locator(".monitor-error")).to_have_count(0)
            checks.append("network-failure-retains-last-snapshot-and-time-manual-refresh-recovers")

            fixture_mode = "hold"
            refresh.click()
            expect(monitor.get_by_role("button", name="读取中…", exact=True)).to_be_disabled()
            request_count = len(monitor_requests)
            page.clock.fast_forward(6000)
            assert len(monitor_requests) == request_count and len(held_monitor) == 1
            checks.append("slow-refresh-disables-duplicate-manual-action-and-never-overlaps-polling")
            page.evaluate("() => { window.settingsHidden=true; Object.defineProperty(document,'hidden',{configurable:true,get:()=>window.settingsHidden}); document.dispatchEvent(new Event('visibilitychange')); }")
            late = json.loads(json.dumps(fixture))
            late["system"]["hostname"] = "late-monitor-must-not-render"
            held_monitor.pop().fulfill(status=200, json={"data": late})
            page.clock.fast_forward(10000)
            assert len(monitor_requests) == request_count
            expect(monitor.get_by_text("late-monitor-must-not-render", exact=True)).to_have_count(0)
            fixture_mode = "zero"
            page.evaluate("() => { window.settingsHidden=false; document.dispatchEvent(new Event('visibilitychange')); }")
            expect(cpu.locator(".resource-value")).to_have_text("0.0%")
            expect(refresh).to_be_enabled()
            assert len(monitor_requests) == request_count + 1
            checks.append("hidden-page-aborts-pending-read-stops-polling-and-resumes-with-fresh-snapshot")

            fixture_mode = "hold"
            refresh.click()
            expect(monitor.get_by_role("button", name="读取中…", exact=True)).to_be_disabled()
            page.get_by_role("button", name="运行总览", exact=True).click()
            expect(monitor).to_have_count(0)
            request_count = len(monitor_requests)
            held_monitor.pop().fulfill(status=200, json={"data": late})
            page.clock.fast_forward(6000)
            assert len(monitor_requests) == request_count
            expect(page.get_by_text("late-monitor-must-not-render", exact=True)).to_have_count(0)
            fixture_mode = "zero"
            page.get_by_role("button", name="系统设置", exact=True).click()
            expect(cpu.locator(".resource-value")).to_have_text("0.0%")
            checks.append("leaving-settings-aborts-old-read-without-late-output-or-new-monitoring-requests")

            fixture_mode = "hold"
            refresh.click()
            expect(monitor.get_by_role("button", name="读取中…", exact=True)).to_be_disabled()
            page.get_by_role("button", name="退出", exact=True).click()
            page.get_by_role("heading", name="控制器登录", exact=True).wait_for()
            request_count = len(monitor_requests)
            held_monitor.pop().fulfill(status=200, json={"data": late})
            page.clock.fast_forward(6000)
            assert len(monitor_requests) == request_count
            expect(monitor).to_have_count(0)
            checks.append("logout-aborts-old-monitor-and-stops-its-timer")
            fixture_mode = "zero"
            page.get_by_label("密码", exact=True).fill(password)
            page.get_by_role("button", name="登录", exact=True).click()
            expect(cpu.locator(".resource-value")).to_have_text("0.0%")
            fixture_mode = "unauthorized"
            refresh.click()
            page.get_by_role("heading", name="控制器登录", exact=True).wait_for()
            request_count = len(monitor_requests)
            page.clock.fast_forward(6000)
            assert len(monitor_requests) == request_count
            checks.append("monitor-authentication-expiry-returns-to-login-and-stops-monitoring")
            assert not errors, errors
            result = {"passed": checks, "pageErrors": errors, "backend": "real binary / isolated state",
                      "browserUIFixtures": ["read-only system metrics for sampling, partial failure, transport failure and delayed/authentication replies", "synthetic page visibility and browser clock"],
                      "realSystem": {"platform": system["platform"], "memoryAvailable": system["memory"]["available"], "diskAvailable": system["disk"]["available"]},
                      "controllerSettingsUnchanged": True, "remoteInstalls": 0, "productionAcceptance": False}
            (output / "result.json").write_text(json.dumps(result, indent=2, ensure_ascii=False), encoding="utf8")
            print(json.dumps(result, ensure_ascii=False))
            browser.close()
            raise SystemExit(0)
        if args.error_docs_only:
            page.get_by_role("button", name="系统设置", exact=True).click()
            settings = page.locator("section.content.settings")
            expect(settings.locator('[aria-label="错误说明列表"]')).to_have_count(0)
            expect(page.get_by_text("点击条目查看检查步骤。具体原因需结合出错主机、时间和 traceId 核对。", exact=True)).to_have_count(0)
            page.screenshot(path=str(output / "settings-error-docs-entry-1440.png"), animations="disabled")
            settings.get_by_role("button", name="查看文档", exact=True).click()
            dialog = page.get_by_role("dialog", name="错误说明", exact=True)
            screenshot_ready(dialog)
            docs = dialog.get_by_role("region", name="错误说明列表", exact=True)
            content = docs.get_by_role("article", name="错误说明正文", exact=True)
            expect(content.get_by_role("heading", name="错误含义", exact=True)).to_be_visible()
            expect(content.get_by_role("heading", name="检查步骤", exact=True)).to_be_visible()
            checks.extend(["settings-has-button-without-embedded-reference-or-intro", "reference-opens-as-document-with-selected-entry"])

            category = docs.get_by_label("错误分类", exact=True)
            category.select_option(label="软件安装")
            expect(docs.get_by_role("button", name=re.compile("COMMAND_TRUST_REQUIRED"))).to_have_attribute("aria-current", "page")
            category.select_option(label="全部分类")
            query = docs.get_by_label("查询错误说明", exact=True)
            query.fill("OPERATION_LOCKED")
            expect(content.get_by_role("heading", name="主机存在未决操作", exact=True)).to_be_visible()
            expect(docs.get_by_role("button", name=re.compile("OPERATION_LOCKED"))).to_have_attribute("aria-current", "page")
            query.fill("")
            docs.get_by_role("button", name=re.compile("PORT_OBSERVATION_DENIED")).click()
            expect(content.get_by_role("heading", name="监听端口读取受限", exact=True)).to_be_visible()
            checks.extend(["category-selects-matching-first-entry", "code-search-and-entry-selection-update-document"])
            query.fill("")
            first = docs.locator('.reference-heading[aria-current="page"]')
            first.focus()
            initial = first.get_attribute("aria-label")
            first.press("ArrowDown")
            expect(docs.locator('.reference-heading[aria-current="page"]')).not_to_have_attribute("aria-label", initial)
            page.keyboard.press("Tab")
            expect(content).to_be_focused()
            page.keyboard.press("Shift+Tab")
            page.keyboard.press("ArrowUp")
            expect(docs.locator('.reference-heading[aria-current="page"]')).to_have_attribute("aria-label", initial)
            checks.append("reference-keyboard-selection-and-document-focus")

            query.fill("UNKNOWN_ERROR_DOCS_TEST")
            expect(docs.get_by_text("没有匹配的错误说明", exact=True)).to_be_visible()
            expect(content).to_have_count(0)
            query.fill("PORT_OBSERVATION_DENIED")
            expect(content.get_by_role("heading", name="监听端口读取受限", exact=True)).to_be_visible()
            checks.append("unknown-search-shows-concise-empty-state-and-recovers")

            for width, height in ((1440, 900), (390, 900), (390, 500)):
                page.set_viewport_size({"width": width, "height": height})
                screenshot_ready(dialog)
                expect(content.get_by_role("heading", name="检查步骤", exact=True)).to_be_visible()
                assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), width
                assert dialog.evaluate("el => {const r=el.getBoundingClientRect(); return r.left>=0 && r.right<=innerWidth+1 && r.top>=0 && r.bottom<=innerHeight+1}"), width
                entry_bounds = docs.get_by_role("button", name=re.compile("PORT_OBSERVATION_DENIED")).bounding_box()
                content_bounds = content.bounding_box()
                assert entry_bounds and content_bounds
                if width == 1440:
                    assert entry_bounds["x"] + entry_bounds["width"] <= content_bounds["x"] + 1
                else:
                    assert content_bounds["y"] >= entry_bounds["y"]
                assert content_bounds["height"] >= 180, (width, height, content_bounds)
                page.screenshot(path=str(output / f"error-docs-{width}-{height}.png"), animations="disabled")
                checks.append(f"document-layout-{width}-{height}-within-viewport")

            page.mouse.click(3, 3)
            dialog.wait_for(state="hidden")
            checks.append("background-click-closes-document")
            assert not errors, errors
            result = {"passed": checks, "pageErrors": errors, "backend": "real binary / isolated state",
                      "browserUIFixtures": [], "initialCodeCoverage": "existing errorReference.test.mjs", "productionAcceptance": False}
            (output / "result.json").write_text(json.dumps(result, indent=2, ensure_ascii=False), encoding="utf8")
            print(json.dumps(result, ensure_ascii=False))
            browser.close()
            raise SystemExit(0)
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
        expect(page.get_by_label("控制器名称", exact=True)).to_have_count(0)
        expect(page.locator(".settings-info-card")).to_have_count(2)
        expect(page.get_by_text("控制器 ID", exact=True)).to_have_count(0)
        with page.expect_download() as download:
            page.get_by_role("button", name="下载备份", exact=True).click()
        backup = output / "ui-backup.tar.gz"
        download.value.save_as(str(backup))
        with tarfile.open(backup) as archive:
            assert "manifest.json" in archive.getnames() and "master.key" not in archive.getnames()
        checks.extend(["settings-cards-without-controller-name-or-id", "browser-backup-download"])
        page.get_by_role("button", name="日志记录", exact=True).click()
        page.get_by_role("tab", name="操作记录", exact=True).click()
        page.get_by_text("创建备份", exact=True).wait_for()
        page.get_by_role("button", name="详情", exact=True).first.click()
        page.get_by_role("dialog", name="记录详情", exact=True).wait_for()
        page.get_by_role("dialog", name="记录详情", exact=True).get_by_role("button", name="Close this dialog").click()
        checks.append("audit-detail")
        audit_route = "**/ops/api/v1/audit?*"
        audit_items = [
            {"id":"audit-success","action":"SOFTWARE_INSTALL_RESULT","result":"SUCCEEDED","actor":"worker","objectId":"software-fixture","traceId":"audit-success-trace","createdAt":"2026-11-02T00:00:00Z"},
            {"id":"audit-unknown","action":"NODE_ENVIRONMENT_CHANGE_RESULT","result":"UNKNOWN","actor":"worker","objectId":"node-fixture","traceId":"audit-unknown-trace","createdAt":"2026-11-02T00:00:01Z"},
            {"id":"audit-registry","action":"NODE_REGISTRY_UPDATED","result":"SUCCESS","actor":"admin","objectId":"registry-fixture","traceId":"audit-registry-trace","createdAt":"2026-11-02T00:00:02Z"},
        ]
        def audit_fixture(route):
            assert route.request.method == "GET", "audit fixture only permits reads"
            route.fulfill(status=200, json={"data":{"items":audit_items,"nextCursor":""}})
        page.route(audit_route, audit_fixture)
        page.get_by_role("button", name="最新记录", exact=True).click()
        success_row = page.locator("tbody tr").filter(has=page.get_by_text("服务安装结果", exact=True))
        success_row.get_by_text("成功", exact=True).wait_for()
        assert success_row.get_by_text("失败", exact=True).count() == 0
        unknown_row = page.locator("tbody tr").filter(has=page.get_by_text("Node 操作结果", exact=True))
        unknown_row.get_by_text("待确认", exact=True).wait_for()
        registry_row = page.locator("tbody tr").filter(has=page.get_by_text("修改 npm 镜像地址", exact=True))
        registry_row.get_by_text("成功", exact=True).wait_for()
        for width in (1440, 390):
            page.set_viewport_size({"width":width,"height":900})
            screenshot_ready()
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
            page.screenshot(path=str(output / f"audit-status-{width}.png"), full_page=True, animations="disabled")
            unknown_row.get_by_role("button", name="详情", exact=True).click()
            audit_dialog = page.get_by_role("dialog", name="记录详情", exact=True)
            audit_dialog.get_by_text("待确认", exact=True).wait_for()
            audit_dialog.get_by_text("原始标识", exact=True).click()
            audit_dialog.get_by_text("NODE_ENVIRONMENT_CHANGE_RESULT", exact=True).wait_for()
            audit_dialog.get_by_text("UNKNOWN", exact=True).wait_for()
            audit_dialog.get_by_text("audit-unknown-trace", exact=True).wait_for()
            screenshot_ready(audit_dialog)
            audit_dialog.screenshot(path=str(output / f"audit-detail-{width}.png"), animations="disabled")
            audit_dialog.get_by_role("button", name="Close this dialog", exact=True).click()
            audit_dialog.wait_for(state="hidden")
        page.set_viewport_size({"width":1440,"height":900})
        page.unroute(audit_route, audit_fixture)
        page.get_by_role("button", name="最新记录", exact=True).click()
        page.get_by_text("创建备份", exact=True).wait_for()
        checks.extend(["audit-worker-success-not-failure", "audit-unknown-not-failure", "audit-registry-action-chinese", "audit-raw-identifiers-retained", "audit-wide-and-narrow-no-overflow"])
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
        assert page.get_by_role("button", name="软件管理", exact=True).get_attribute("aria-current") == "page"
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
            detail.locator("details.software-diagnostics > summary").click()
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
        page.get_by_role("button", name="软件安装", exact=True).click()
        page.locator(".software-recipes").wait_for()
        catalog = page.request.get(url + "/ops/api/v1/software-recipes").json()["data"]
        catalog_keys = {entry["key"] for entry in catalog if entry["key"] != "node"}
        expected_cards = len(catalog_keys) + 1  # Node has one version-management entry.
        page.wait_for_function("count => document.querySelectorAll('.plugin-card').length === count", arg=expected_cards)
        page.get_by_role("heading", name="Node.js", exact=True).wait_for()
        assert page.get_by_label("目标主机", exact=True).input_value()
        assert page.get_by_role("heading", name="Maven", exact=True).count() == 1
        assert page.get_by_role("button", name="安装", exact=True).count() == len(catalog_keys)
        java_card = page.locator(".plugin-card").filter(has=page.get_by_role("heading", name="Java", exact=True))
        assert java_card.locator("select").count() == 0
        java_card.get_by_role("button", name="安装", exact=True).click()
        java_install = page.locator(".el-drawer:visible").last
        java_install.get_by_role("button", name="原生安装", exact=True).click()
        java_install.get_by_label("原生安装来源", exact=True).select_option("OFFLINE")
        java_version = java_install.get_by_label("安装版本", exact=True)
        java_native = [entry for entry in catalog if entry["key"] == "java" and "NATIVE" in entry["runtimeModes"]]
        assert java_version.locator('option[value]:not([value=""])').count() == len(java_native)
        java17 = next(entry for entry in java_native if entry["version"] == "17")
        java_version.select_option(java17["revision"])
        assert java_version.input_value() == java17["revision"]
        java_install.get_by_role("button", name="取消", exact=True).click()
        discard = page.get_by_role("dialog", name="关闭窗口", exact=True)
        if discard.is_visible():
            discard.get_by_role("button", name="放弃修改", exact=True).click()
        java_install.wait_for(state="hidden")
        page.get_by_role("button", name="构建工具", exact=True).click()
        assert page.locator(".plugin-card").count() == 2
        page.get_by_role("heading", name="Maven", exact=True).wait_for()
        page.get_by_role("heading", name=next(entry["name"] for entry in catalog if entry["key"] == "git"), exact=True).wait_for()
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
        checks.extend(["I02-P1-catalog-matches-recipes-and-Node-entry", "I02-P1-tools-can-open-install-preflight-with-diagnostics", "I02-P1-service-port-form-only-inside-installation-drawer"])
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
        expect(page.locator(".settings-info-card")).to_have_count(2)
        expect(page.get_by_label("控制器名称", exact=True)).to_have_count(0)
        page.get_by_role("button", name="运行总览", exact=True).click()
        page.get_by_role("heading", name="运行总览", exact=True).wait_for()
        checks.append("password-change-relogin")
        page.set_viewport_size({"width": 390, "height": 844})
        for name in ["运行总览", "主机管理", "软件管理", "应用实例", "版本制品", "发布作业", "连接凭据", "日志记录", "系统设置"]:
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
                page.get_by_role("button", name="展开导航", exact=True).click()
                page.get_by_role("button", name="软件安装", exact=True).click()
                page.locator(".software-recipes").wait_for()
                page.wait_for_function("count => document.querySelectorAll('.plugin-card').length === count", arg=expected_cards)
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
    result = {"passed": checks, "pageErrors": errors, "backend": "real binary / isolated state", "browserUIFixtures": ["software-discovery candidate responses only; registration and unmanage use the real API", "read-only audit list responses for translated results and raw identifiers"], "productionAcceptance": False}
    (output / "result.json").write_text(json.dumps(result, indent=2, ensure_ascii=False))
    print(json.dumps(result, ensure_ascii=False))
except Exception as cause:
    result = {"status": "FAIL", "passed": checks, "pageErrors": errors, "error": str(cause)}
    try:
        if "page" in locals() and not page.is_closed():
            page.screenshot(path=str(output / "failure.png"), full_page=True, animations="disabled")
            (output / "failure.html").write_text(page.content(), encoding="utf8")
    except Exception as capture_error:
        result["captureError"] = str(capture_error)
    (output / "result.json").write_text(json.dumps(result, indent=2, ensure_ascii=False), encoding="utf8")
    raise
finally:
    process.terminate()
    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)
    stderr.close()
