"""Observation UI/API acceptance using an isolated controller. No remote hosts."""
import argparse
import datetime
import json
import pathlib
import secrets
import socket
import subprocess
import time
import urllib.request
from playwright.sync_api import sync_playwright
from secure_request import protect_request

parser = argparse.ArgumentParser()
parser.add_argument("--binary", required=True)
parser.add_argument("--output", required=True)
parser.add_argument("--browser-executable", required=True)
args = parser.parse_args()
binary = str(pathlib.Path(args.binary).resolve())
output = pathlib.Path(args.output).resolve()
output.mkdir(parents=True, exist_ok=False)
state = output / "state"
password = "Observe-" + secrets.token_hex(12)
secret_file = output / ".password.secret"
secret_file.write_text(password)
try:
    subprocess.run([binary, "init-admin", "--state", str(state), "--password-file", str(secret_file)], check=True)
finally:
    secret_file.unlink(missing_ok=True)
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
url = f"http://127.0.0.1:{port}"
runtime_log = (output / "runtime.log").open("w")
process = None
checks = []
errors = []


def start():
    controller = subprocess.Popen([binary, "serve", "--state", str(state), "--listen", f"127.0.0.1:{port}"], stdout=runtime_log, stderr=runtime_log)
    for _ in range(100):
        try:
            urllib.request.urlopen(url + "/ops/api/v1/health", timeout=1).close()
            return controller
        except Exception:
            if controller.poll() is not None:
                raise AssertionError("controller exited")
            time.sleep(0.1)
    controller.terminate()
    controller.wait(timeout=10)
    raise AssertionError("controller failed to start")


try:
    process = start()
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True, executable_path=args.browser_executable)
        page = browser.new_page(viewport={"width": 1440, "height": 900})
        page.on("pageerror", lambda error: errors.append(str(error)))
        cancellations = []
        page.on("request", lambda request: cancellations.append(request.url)
                if request.method == "POST" and request.url.endswith("/cancel") else None)
        page.goto(url)
        page.get_by_label("账号", exact=True).fill("admin")
        page.get_by_label("密码", exact=True).fill(password)
        page.get_by_role("button", name="登录", exact=True).click()
        page.get_by_role("heading", name="运行总览", exact=True).wait_for()

        def api(path, method="GET", data=None):
            data = protect_request(path, method, data,
                                   lambda target: api("/auth/secure-challenge", "POST", {"path": target}))
            csrf = page.request.get(url + "/ops/api/v1/auth/csrf").json()["data"]["csrfToken"]
            response = page.request.fetch(url + "/ops/api/v1" + path, method=method, data=data,
                                          headers={"Origin": url, "X-CSRF-Token": csrf, "Content-Type": "application/json"})
            assert response.ok, (response.status, response.text())
            return response.json()["data"]

        host = api("/hosts", "POST", {"name": "隔离测试主机", "kind": "LOCAL"})
        software = api("/software-instances", "POST", {"name": "测试 Java", "hostId": host["id"], "softwareType": "JAVA", "runtimeKind": "TOOL", "locator": "java"})
        page.get_by_role("button", name="软件管理", exact=True).click()
        trigger = page.get_by_role("button", name="检测记录", exact=True)
        panel = page.locator('section[aria-label="最近检测结果"]')

        def close_records_outside():
            tabs = page.locator("main.main > header").bounding_box()
            assert tabs
            page.mouse.click(tabs["x"] + tabs["width"] - 8, tabs["y"] + 5)
            panel.wait_for(state="hidden")

        page.get_by_role("button", name="一键检测（1）", exact=True).click()
        trigger.wait_for()
        assert trigger.evaluate("button => !!button.closest('.software-toolbar-actions')")
        assert not panel.is_visible()
        assert page.locator("details.observation-batch").count() == 0
        trigger.click()
        panel.get_by_text("已结束", exact=True).wait_for()
        batch = api("/observation-batches")
        assert len(batch["items"]) == 1 and batch["items"][0]["objectId"] == software["id"]
        assert batch["status"] in ("SUCCEEDED", "PARTIAL")
        checks.extend(["real-oneclick-batch", "background-progress-without-manual-probe"])
        panel.locator(".batch-item").wait_for()
        assert "测试 Java" in panel.locator(".batch-item").inner_text()
        page.get_by_role("button", name="关闭检测记录", exact=True).click()
        panel.wait_for(state="hidden")
        trigger.click()
        close_records_outside()
        assert not cancellations
        checks.extend(["toolbar-records-button-without-summary-row", "records-close-button-and-outside-click"])

        # This read-only fixture makes pending progress deterministic: the real
        # single LOCAL check can finish before the browser first renders it.
        pending = dict(batch, status="RUNNING", items=[dict(item, status="RUNNING", errorCode="", observedAt="") for item in batch["items"]])

        def pending_batch(route):
            if route.request.method == "GET":
                route.fulfill(json={"code": "SUCCESS", "data": pending})
            else:
                route.continue_()

        page.route("**/ops/api/v1/observation-batches", pending_batch)
        page.get_by_role("button", name="刷新", exact=True).click()
        page.wait_for_function("() => /0\\s*\\/\\s*1/.test(document.querySelector('[aria-label=\"检测记录\"]')?.textContent || '')")
        trigger.click()
        panel.get_by_role("button", name="取消排队", exact=True).wait_for()
        close_records_outside()
        assert not cancellations
        checks.append("pending-fixture-progress-and-outside-close-without-cancellation")
        page.unroute("**/ops/api/v1/observation-batches", pending_batch)
        page.get_by_role("button", name="运行总览", exact=True).click()
        page.get_by_role("button", name="软件管理", exact=True).click()
        trigger.wait_for()
        assert not panel.is_visible()
        trigger.click()
        panel.get_by_text("已结束", exact=True).wait_for()
        close_records_outside()
        checks.append("batch-persists-across-page-unmount")

        subprocess.run([binary, "stop", "--state", str(state)], check=True, capture_output=True)
        process.wait(timeout=15)
        process = start()
        assert api("/observation-batches")["id"] == batch["id"]
        page.reload()
        page.get_by_role("heading", name="运行总览", exact=True).wait_for()
        checks.append("batch-persists-across-real-controller-restart")

        # A second read-only fixture proves stale values remain visible after
        # a failed attempt. It does not prove physical metrics.
        old = (datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(hours=1)).isoformat()
        def stale_snapshot(route):
            route.fulfill(json={"code": "SUCCESS", "data": [{"hostId": host["id"], "enabled": True,
                "freshness": "STALE", "latestAttempt": {"code": "SSH_TIMEOUT", "at": old},
                "lastConfirmed": {"code": "READY", "observedAt": old, "hostname": "fixture",
                    "cpuCores": 8, "cpuPercent": 50, "memoryTotalBytes": 1000,
                    "memoryAvailableBytes": 500, "rootTotalBytes": 1000, "rootUsedBytes": 400}}]})
        page.route("**/ops/api/v1/host-observations", stale_snapshot)
        page.reload()
        page.locator(".resource-gauge strong").first.wait_for()
        page.wait_for_function("() => document.querySelector('.resource-gauge strong')?.textContent.includes('50.0')")
        assert "上次确认" in page.locator(".observation-notice").inner_text()
        assert page.locator(".host-dot.available").count() == 0
        checks.append("stale-fixture-value-retained-with-failure-and-time")
        for width in (1440, 1280, 390):
            page.set_viewport_size({"width": width, "height": 900})
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), width
            if width == 390:
                page.get_by_role("button", name="展开导航", exact=True).click()
            page.get_by_role("button", name="软件管理", exact=True).click()
            page.get_by_role("button", name="一键检测（1）", exact=True).wait_for()
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), width
            page.screenshot(path=str(output / f"software-{width}.png"), full_page=True)
            trigger.click()
            panel.wait_for()
            page.wait_for_function("""() => {
                const popup = document.querySelector('section[aria-label="最近检测结果"]')?.closest('.el-popper');
                return popup && getComputedStyle(popup).opacity === '1';
            }""")
            bounds = panel.locator("xpath=ancestor::*[contains(@class, 'el-popper')][1]").bounding_box()
            assert bounds and bounds["x"] >= 15 and bounds["x"] + bounds["width"] <= width - 15, (width, bounds)
            assert "测试 Java" in panel.inner_text()
            page.screenshot(path=str(output / f"software-{width}-records.png"), full_page=True)
            close_records_outside()
            if width == 390:
                page.get_by_role("button", name="展开导航", exact=True).click()
            page.get_by_role("button", name="运行总览", exact=True).click()
            checks.append(f"layout-{width}-no-horizontal-overflow")
        browser.close()
    assert not errors, errors
    result = {"passed": checks, "pageErrors": errors, "backend": "real isolated controller", "fixture": "read-only pending batch and stale host-observations responses", "hardwareAcceptance": False}
    (output / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf8")
    print(json.dumps(result, ensure_ascii=False))
finally:
    if process and process.poll() is None:
        subprocess.run([binary, "stop", "--state", str(state)], capture_output=True, timeout=20)
        process.wait(timeout=15)
    runtime_log.close()
