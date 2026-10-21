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
        page.goto(url)
        page.get_by_label("账号", exact=True).fill("admin")
        page.get_by_label("密码", exact=True).fill(password)
        page.get_by_role("button", name="登录", exact=True).click()
        page.get_by_role("heading", name="运行总览", exact=True).wait_for()

        def api(path, method="GET", data=None):
            csrf = page.request.get(url + "/ops/api/v1/auth/csrf").json()["data"]["csrfToken"]
            response = page.request.fetch(url + "/ops/api/v1" + path, method=method, data=data,
                                          headers={"Origin": url, "X-CSRF-Token": csrf, "Content-Type": "application/json"})
            assert response.ok, (response.status, response.text())
            return response.json()["data"]

        host = api("/hosts", "POST", {"name": "隔离测试主机", "kind": "LOCAL"})
        software = api("/software-instances", "POST", {"name": "测试 Java", "hostId": host["id"], "softwareType": "JAVA", "runtimeKind": "TOOL", "locator": "java"})
        page.get_by_role("button", name="软件管理", exact=True).click()
        page.get_by_role("button", name="一键检测（1）", exact=True).click()
        page.locator(".observation-batch").wait_for()
        page.wait_for_function("() => document.querySelector('.observation-batch summary')?.textContent.includes('已结束')")
        batch = api("/observation-batches")
        assert len(batch["items"]) == 1 and batch["items"][0]["objectId"] == software["id"]
        assert batch["status"] in ("SUCCEEDED", "PARTIAL")
        checks.extend(["real-oneclick-batch", "background-progress-without-manual-probe"])
        page.locator(".observation-batch summary").click()
        page.locator(".batch-item").wait_for()
        assert "测试 Java" in page.locator(".batch-item").inner_text()
        page.get_by_role("button", name="运行总览", exact=True).click()
        page.get_by_role("button", name="软件管理", exact=True).click()
        page.locator(".observation-batch").wait_for()
        checks.append("batch-persists-across-page-unmount")

        subprocess.run([binary, "stop", "--state", str(state)], check=True, capture_output=True)
        process.wait(timeout=15)
        process = start()
        assert api("/observation-batches")["id"] == batch["id"]
        page.reload()
        page.get_by_role("heading", name="运行总览", exact=True).wait_for()
        checks.append("batch-persists-across-real-controller-restart")

        # Only this read response is a UI fixture: prove stale values remain
        # visible after a failed attempt. It does not prove physical metrics.
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
            if width == 390:
                page.get_by_role("button", name="展开导航", exact=True).click()
            page.get_by_role("button", name="运行总览", exact=True).click()
            checks.append(f"layout-{width}-no-horizontal-overflow")
        browser.close()
    assert not errors, errors
    result = {"passed": checks, "pageErrors": errors, "backend": "real isolated controller", "fixture": "only stale host-observations response", "hardwareAcceptance": False}
    (output / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf8")
    print(json.dumps(result, ensure_ascii=False))
finally:
    if process and process.poll() is None:
        subprocess.run([binary, "stop", "--state", str(state)], capture_output=True, timeout=20)
        process.wait(timeout=15)
    runtime_log.close()
