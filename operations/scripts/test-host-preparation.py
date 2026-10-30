"""Host onboarding UI + real archive checks in an isolated controller.

The SSH host row and online job responses are explicit test fixtures. No server
is installed. Authentication, package generation and download use the real API.
"""
import argparse
import hashlib
import json
from pathlib import Path
import secrets
import socket
import sqlite3
import subprocess
import tarfile
import time
import urllib.request
import uuid

from playwright.sync_api import sync_playwright
from secure_request import protect_request


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--browser-executable", required=True)
    parser.add_argument("--bash", help="Optional Bash executable for install.sh syntax validation")
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve())
    output = Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=False)
    state = output / "state"
    password = "Prepare-" + secrets.token_hex(16)
    password_file = output / ".password.secret"
    password_file.write_text(password)
    try:
        subprocess.run([binary, "init-admin", "--state", str(state), "--password-file", str(password_file)], check=True, capture_output=True)
    finally:
        password_file.unlink(missing_ok=True)
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    base = f"http://127.0.0.1:{port}"
    checks, errors = [], []
    with (output / "runtime.log").open("w", encoding="utf8") as runtime_log:
        process = subprocess.Popen([binary, "serve", "--state", str(state), "--listen", f"127.0.0.1:{port}"], stdout=runtime_log, stderr=runtime_log)
        try:
            for _ in range(100):
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
                page.on("pageerror", lambda error: errors.append(str(error)))
                page.goto(base)
                page.get_by_label("账号", exact=True).fill("admin")
                page.get_by_label("密码", exact=True).fill(password)
                page.get_by_role("button", name="登录", exact=True).click()
                page.get_by_role("heading", name="运行总览", exact=True).wait_for()

                def api(path, method="GET", data=None, expected=200):
                    data = protect_request(path, method, data, lambda target: api("/auth/secure-challenge", "POST", {"path": target}))
                    csrf = page.request.get(base + "/ops/api/v1/auth/csrf").json()["data"]["csrfToken"]
                    response = page.request.fetch(base + "/ops/api/v1" + path, method=method, data=data,
                        headers={"Origin": base, "X-CSRF-Token": csrf, "Content-Type": "application/json"})
                    assert response.status == expected, (path, response.status, response.json().get("code"))
                    return response.json()["data"]

                host = api("/hosts", "POST", {"name": "主机接入界面测试", "kind": "LOCAL"}, 201)
                credential = api("/credentials", "POST", {"name": "测试用 SSH 凭据", "kind": "PASSWORD", "secret": secrets.token_hex(20)}, 201)
                # Fixture the SSH identity only in this test's brand-new state.
                with sqlite3.connect(state / "controller.sqlite") as db:
                    db.execute("UPDATE targets SET kind='SSH',address='127.0.0.1',ssh_port=9,ssh_user='ubuntu',host_fingerprint=?,credential_id=?,credential_revision=1 WHERE id=?",
                        ("SHA256:" + "A" * 43, credential["id"], host["id"]))
                    db.execute("UPDATE observation_plans SET enabled=0 WHERE host_id=?", (host["id"],))
                preparation = api(f"/hosts/{host['id']}/preparation")
                assert preparation["package"]["available"], preparation["package"]
                current_job = {"value": None}
                def preparation_response(route):
                    data = dict(preparation)
                    if current_job["value"]:
                        data["remoteJob"] = current_job["value"]
                    route.fulfill(json={"code": "SUCCESS", "data": data})
                page.route(f"**/hosts/{host['id']}/preparation", preparation_response)
                page.get_by_role("button", name="主机管理", exact=True).click()
                page.get_by_role("button", name="详情", exact=True).click()
                drawer = page.get_by_role("dialog", name=host["name"], exact=True)
                drawer.get_by_role("tab", name="安装准备", exact=True).click()
                panel = drawer.locator(".host-preparation")
                panel.get_by_role("heading", name="一键安装到服务器", exact=True).wait_for()
                assert panel.get_by_role("button", name="开始安装", exact=True).is_enabled()
                assert panel.locator('input[type="password"]').count() == 0
                assert panel.get_by_text(preparation["installRoot"], exact=True).is_visible()
                permission_text = panel.locator(".target-meta").inner_text()
                assert "ubuntu" in permission_text and "sudo" in permission_text, permission_text
                assert panel.locator(".diagnostics,.technical-details").count() == 0
                checks.extend(["single-online-action", "online-default", "install-root-visible", "required-privilege-visible", "technical-sections-removed"])
                panel.screenshot(path=str(output / "online-wide.png"), animations="disabled")
                page.set_viewport_size({"width": 390, "height": 844})
                assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
                assert panel.evaluate("e => e.scrollWidth <= e.clientWidth + 1")
                panel.screenshot(path=str(output / "online-narrow.png"), animations="disabled")
                page.set_viewport_size({"width": 1440, "height": 1000})
                panel.get_by_role("button", name="离线", exact=True).click()
                panel.get_by_role("heading", name="下载后运行一条命令", exact=True).wait_for()
                assert panel.locator(".single-command code").inner_text() == "bash install.sh"
                with page.expect_download() as download:
                    panel.get_by_role("button", name="下载离线安装包", exact=True).click()
                archive_path = output / download.value.suggested_filename
                download.value.save_as(str(archive_path))
                with tarfile.open(archive_path) as archive:
                    assert set(archive.getnames()) == {"streamfusion-ops", "install.sh", "SHA256SUMS", "HOST-SETUP.md"}
                    files = {name: archive.extractfile(name).read() for name in archive.getnames()}
                    for line in files["SHA256SUMS"].decode().splitlines():
                        digest, name = line.split()
                        assert hashlib.sha256(files[name]).hexdigest() == digest
                    installer = files["install.sh"].decode()
                    assert host["id"] in installer and preparation["controllerId"] in installer
                    assert "--account 'ubuntu'" in installer and "--authorize-only --apply" in installer
                    assert files["streamfusion-ops"].startswith(b"\x7fELF")
                    assert password.encode() not in files["install.sh"]
                    (output / "install.sh").write_bytes(files["install.sh"])
                if args.bash:
                    subprocess.run([args.bash, "-n", str(output / "install.sh")], check=True)
                    checks.append("generated-install-sh-bash-syntax")
                checks.extend(["real-complete-package-download", "all-package-checksums", "host-bound-single-installer", "linux-elf-material", "one-offline-command"])
                panel.screenshot(path=str(output / "offline-wide.png"), animations="disabled")
                page.set_viewport_size({"width": 390, "height": 844})
                assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
                assert panel.evaluate("e => e.scrollWidth <= e.clientWidth + 1")
                panel.screenshot(path=str(output / "offline-narrow.png"), animations="disabled")
                checks.append("online-offline-narrow-no-overflow")
                page.set_viewport_size({"width": 1440, "height": 1000})
                available = {"status": "AVAILABLE", "code": "OK", "message": "可用"}
                checked_host = dict(host)
                checked_host["observedAt"] = "2026-10-29T01:00:00Z"
                checked_host["capabilities"] = {"reachability": available, "readOnly": available,
                    "mutation": {"status": "UNAVAILABLE", "code": "EXECUTOR_NOT_FOUND", "message": "请先安装执行器"}}
                page.route(f"**/hosts/{host['id']}/checks", lambda route: route.fulfill(json={"code": "SUCCESS", "data": checked_host}))
                panel.get_by_role("button", name="检查安装结果", exact=True).click()
                panel.get_by_text("服务器尚未接入", exact=True).wait_for()
                assert panel.get_by_text("请先安装执行器", exact=True).is_visible()
                checked_host["capabilities"]["mutation"] = available
                panel.get_by_role("button", name="检查安装结果", exact=True).click()
                panel.get_by_text("服务器已接入", exact=True).wait_for()
                assert panel.get_by_text("现在可以继续安装和管理软件。", exact=True).is_visible()
                checks.extend(["offline-check-shows-missing-executor", "offline-check-shows-ready"])
                panel.get_by_role("button", name="在线", exact=True).click()
                sudo_password = "ui-test-sudo-$-secret"
                install_attempts = {"count": 0}
                def install_response(route):
                    envelope = route.request.post_data_json
                    assert set(envelope) == {"challengeId", "clientPublicKey", "iv", "ciphertext"}
                    assert sudo_password not in route.request.post_data
                    install_attempts["count"] += 1
                    if install_attempts["count"] == 1:
                        current_job["value"] = {"id": str(uuid.uuid4()), "status": "FAILED", "phase": "INSTALLING_EXECUTOR", "errorCode": "PREPARATION_SUDO_REQUIRED", "message": "请输入当前 SSH 账号的系统密码", "traceId": "ui-sudo-required"}
                        route.fulfill(json={"code": "SUCCESS", "data": current_job["value"]})
                        return
                    current_job["value"] = {"id": str(uuid.uuid4()), "status": "UNKNOWN", "phase": "INSTALLING_EXECUTOR", "errorCode": "PREPARATION_RESULT_UNKNOWN", "message": "请核对安装结果", "createdAt": "2026-10-29T00:59:00Z", "updatedAt": "2026-10-29T01:00:00Z", "traceId": "ui-test-trace"}
                    route.fulfill(json={"code": "SUCCESS", "data": current_job["value"]})
                page.route(f"**/hosts/{host['id']}/preparation/install", install_response)
                panel.get_by_role("button", name="开始安装", exact=True).click()
                panel.get_by_label("ubuntu 的登录密码", exact=True).fill(sudo_password)
                assert panel.get_by_text("用于 sudo 提权，仅本次使用", exact=True).is_visible()
                panel.get_by_role("button", name="开始安装", exact=True).click()
                panel.get_by_text("安装结果待确认", exact=True).wait_for()
                assert panel.get_by_label("ubuntu 的登录密码", exact=True).count() == 0
                assert panel.get_by_role("button", name="开始安装", exact=True).count() == 0
                assert panel.get_by_role("button", name="检查安装结果", exact=True).is_enabled()
                assert panel.get_by_text("请检查服务器上的安装结果。", exact=True).is_visible()
                assert panel.get_by_text("最后确认到：安装接入组件", exact=True).is_visible()
                assert not panel.get_by_text("PREPARATION_RESULT_UNKNOWN", exact=True).is_visible()
                panel.screenshot(path=str(output / "unknown-wide.png"), animations="disabled")
                page.set_viewport_size({"width": 390, "height": 844})
                assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
                assert panel.evaluate("e => e.scrollWidth <= e.clientWidth + 1")
                panel.screenshot(path=str(output / "unknown-narrow.png"), animations="disabled")
                page.set_viewport_size({"width": 1440, "height": 1000})
                assert panel.get_by_text("任务编号", exact=True).count() == 0
                assert panel.get_by_text("请求标识", exact=True).count() == 0
                def reconcile_response(route):
                    route.fulfill(status=409, json={"code": "EXECUTOR_PATH_UNSAFE", "msg": "接入目录不满足安全要求，请让服务器管理员检查符号链接、所有者和写入权限。", "timestamp": "2026-10-29T01:01:00Z", "traceId": "ui-path-check"})
                page.route(f"**/hosts/{host['id']}/preparation/reconcile", reconcile_response)
                panel.get_by_role("button", name="检查安装结果", exact=True).click()
                panel.get_by_text("接入目录权限不符合要求", exact=True).wait_for()
                assert panel.get_by_text("接入目录权限有误，点击下方按钮修复。", exact=True).is_visible()
                assert not panel.get_by_text("EXECUTOR_PATH_UNSAFE", exact=True).is_visible()
                assert panel.get_by_text("只修复", exact=False).count() == 0
                assert panel.get_by_text("无需手动执行", exact=False).count() == 0
                assert panel.get_by_role("button", name="修复权限并继续安装", exact=True).is_enabled()
                repair_password = "ui-repair-sudo-$-secret"
                repair_input = panel.get_by_label("ubuntu 的登录密码", exact=True)
                repair_input.fill(repair_password)
                panel.screenshot(path=str(output / "permission-repair-wide.png"), animations="disabled")
                page.set_viewport_size({"width": 390, "height": 844})
                assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
                assert panel.evaluate("e => e.scrollWidth <= e.clientWidth + 1")
                panel.screenshot(path=str(output / "permission-repair-narrow.png"), animations="disabled")
                page.set_viewport_size({"width": 1440, "height": 1000})
                def repair_response(route):
                    envelope = route.request.post_data_json
                    assert set(envelope) == {"challengeId", "clientPublicKey", "iv", "ciphertext"}
                    assert repair_password not in route.request.post_data
                    current_job["value"] = {**current_job["value"], "status": "SUCCEEDED", "errorCode": "PREPARATION_READY", "message": "主机接入完成，可以继续安装软件。"}
                    route.fulfill(json={"code": "SUCCESS", "data": current_job["value"]})
                page.route(f"**/hosts/{host['id']}/preparation/repair", repair_response)
                panel.get_by_role("button", name="修复权限并继续安装", exact=True).click()
                panel.get_by_role("heading", name="服务器已接入", exact=True).wait_for()
                assert panel.get_by_label("ubuntu 的登录密码", exact=True).count() == 0
                assert panel.get_by_role("button", name="开始安装", exact=True).count() == 0
                checks.extend(["sudo-only-when-required", "sudo-account-named", "sudo-request-encrypted", "sudo-input-cleared", "unknown-blocks-repeat-install", "unknown-primary-action", "unknown-human-summary", "unknown-last-confirmed-phase", "job-identifiers-hidden", "unknown-narrow-no-overflow", "path-problem-human-result", "repair-action-visible", "redundant-repair-notes-removed", "repair-request-encrypted", "repair-input-cleared", "repair-narrow-no-overflow", "success-hides-install-form"])
                current_job["value"]["status"] = "RUNNING"
                current_job["value"]["phase"] = "VERIFYING_EXECUTOR"
                panel.get_by_role("button", name="重新加载", exact=True).click()
                panel.get_by_role("heading", name="验证接入结果", exact=True).wait_for()
                def fail_poll_once(route):
                    route.fulfill(status=503, json={"code": "TEST_TEMPORARY_UNAVAILABLE", "msg": "测试临时连接失败"})
                page.route(f"**/hosts/{host['id']}/preparation", fail_poll_once, times=1)
                panel.get_by_role("alert").filter(has_text="安装进度将在稍后重试").wait_for()
                current_job["value"]["status"] = "SUCCEEDED"
                current_job["value"]["phase"] = "PREPARATION_RESULT"
                current_job["value"]["errorCode"] = "PREPARATION_READY"
                current_job["value"]["message"] = "主机接入完成，可以继续安装软件。"
                panel.get_by_text("服务器已接入", exact=True).wait_for()
                checks.append("progress-poll-recovers-after-temporary-failure")
                assert not errors, errors
                (output / "result.json").write_text(json.dumps({"passed": checks, "pageErrors": errors, "fixtures": ["isolated SSH host identity", "preparation job responses", "host check responses"], "real": ["authentication", "package generation and download"], "remoteInstallationAccepted": False}, ensure_ascii=False, indent=2), encoding="utf8")
                print(json.dumps({"checks": len(checks), "pageErrors": len(errors), "output": str(output), "remoteInstallationAccepted": False}))
                browser.close()
        finally:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)


if __name__ == "__main__":
    main()
