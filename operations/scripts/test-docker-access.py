"""OPS-DOCKER-ACCESS-01 UI checks using an isolated real controller and named fixtures.

Catalog/auth/assets are real. Host capabilities, Docker account access, component updates and
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
              "scope": "Real isolated controller catalog/auth/assets; Docker account checks, updates and jobs are explicit browser fixtures",
              "targetMutationExecuted": False, "unexpectedMutations": blocked,
              "limitations": ["No real Linux group, socket or login-session changes", "No existing controller state opened"]}
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
                definitions = api("/software-recipes")
                docker = next(item for item in definitions if item["key"] == "docker")
                now = datetime.now(timezone.utc)
                stamp = now.isoformat().replace("+00:00", "Z")
                expiry = (now + timedelta(hours=1)).isoformat().replace("+00:00", "Z")
                cap = {"status": "AVAILABLE", "code": "UI_FIXTURE", "message": "界面夹具", "observedAt": stamp, "source": "UI_FIXTURE"}
                fixture_host = {**host, "name": "界面夹具 Ubuntu 主机", "kind": "SSH", "address": "192.0.2.77", "sshUser": "ubuntu", "version": "1", "maintenance": False,
                                "installRoot": "/opt/streamfusion-ops", "capabilities": {"reachability": cap, "readOnly": cap, "mutation": cap, "docker": cap, "expiresAt": expiry}}
                engine = {"id": str(uuid.uuid4()), "hostId": host["id"], "hostName": fixture_host["name"], "name": "界面夹具 Docker Engine", "softwareType": "DOCKER", "runtimeKind": "SYSTEMD",
                          "locator": "docker.service", "managementMode": "MANAGED", "dataOwnership": "OPS", "installationStatus": "READY", "version": "1", "lastJobId": "", "freshness": "FRESH",
                          "actualState": "RUNNING", "desiredState": "RUNNING", "errorCode": "", "installation": {"definition": docker, "config": {"name": "Docker", "runtimeMode": "NATIVE", "memoryMiB": 512}},
                          "observation": {"state": "RUNNING", "identityHash": "a" * 64, "observedAt": stamp, "source": "UI_FIXTURE", "version": docker["version"]}}
                external = {**deepcopy(engine), "id": str(uuid.uuid4()), "name": "界面夹具 外部 Engine", "managementMode": "OBSERVE", "dataOwnership": "EXTERNAL"}
                container = {**deepcopy(engine), "id": str(uuid.uuid4()), "name": "界面夹具 Docker 容器", "runtimeKind": "DOCKER", "locator": "b" * 64}
                rows = [engine, external, container]
                def account(name, uid, eligible=True):
                    return {"name": name, "uid": uid, "gid": uid, "shell": "/bin/bash" if eligible else "/usr/sbin/nologin", "eligible": eligible, "reason": "" if eligible else "DOCKER_ACCOUNT_LOGIN_DISABLED", "member": False, "primaryGroup": False, "groups": []}
                observation = {"softwareId": engine["id"], "engineIdentityHash": "a" * 64, "dockerGid": "999", "uidMin": 1000, "uidMax": 60000, "fingerprint": "a" * 64, "observedAt": stamp,
                               "socket": {"path": "/run/docker.sock", "uid": "0", "gid": "999", "mode": "660", "ready": True}, "accounts": [account("ubuntu", "1000"), account("operator", "1001"), account("disabled", "1002", False)]}
                jobs, requests, updates = {}, [], []
                supported, lost_reply, reject_next = True, True, False
                def route_api(route):
                    nonlocal supported, lost_reply, reject_next
                    request = route.request
                    path = urlsplit(request.url).path.removeprefix("/ops/api/v1")
                    calls.append({"path": path, "method": request.method})
                    if path.startswith("/auth/"): route.continue_()
                    elif path == "/hosts": route.fulfill(json={"data": [fixture_host]})
                    elif path == "/software-instances": route.fulfill(json={"data": rows})
                    elif path == "/jobs": route.fulfill(json={"data": {"items": list(jobs.values()), "nextCursor": ""}})
                    elif path == f"/software-instances/{engine['id']}/docker-access":
                        if supported: route.fulfill(json={"data": {"softwareId": engine["id"], "expectedVersion": engine["version"], "expectedHostVersion": fixture_host["version"], "sshAccount": "ubuntu", "lastJobId": engine["lastJobId"], "observation": observation}})
                        else: route.fulfill(status=409, json={"code": "EXECUTOR_DOCKER_ACCESS_UNSUPPORTED", "msg": "接入组件需要更新", "timestamp": stamp, "traceId": "UI_FIXTURE_OLD_COMPONENT"})
                    elif path == f"/hosts/{host['id']}/preparation":
                        route.fulfill(json={"data": {"hostId": host["id"], "expectedHostVersion": fixture_host["version"], "account": "ubuntu", "installRoot": fixture_host["installRoot"],
                                                    "package": {"available": True, "fileName": "ui-fixture.tar.gz", "sha256": "b" * 64}, "remoteJob": {"id": "ui-old-component", "status": "SUCCEEDED", "phase": "VERIFYING_EXECUTOR", "updatedAt": stamp}}})
                    elif path == f"/hosts/{host['id']}/preparation/install":
                        assert request.post_data_json.get("ciphertext")
                        updates.append({"encrypted": True})
                        supported = True
                        fixture_host["version"] = engine["version"] = "2"
                        route.fulfill(json={"data": {"id": "ui-updated-component", "status": "SUCCEEDED", "phase": "VERIFYING_EXECUTOR", "updatedAt": stamp}})
                    elif path == f"/hosts/{host['id']}/checks": route.fulfill(json={"data": fixture_host})
                    elif path == f"/software-instances/{engine['id']}/actions":
                        body = request.post_data_json
                        assert body["action"] == "DOCKER_ACCESS" and body["confirmed"] is True
                        assert body["dockerAccess"]["account"] == "operator"
                        requests.append(body)
                        if reject_next:
                            reject_next = False
                            route.fulfill(status=409, json={"code": "DOCKER_ACCESS_STATE_CHANGED", "msg": "账号状态已变化，请重新检查", "timestamp": stamp, "traceId": "UI_FIXTURE_REJECTED"})
                            return
                        key = body["idempotencyKey"]
                        if key not in jobs:
                            action = body["dockerAccess"]["action"]
                            assert body["expectedVersion"] == engine["version"] and body["expectedHostVersion"] == fixture_host["version"]
                            assert body["dockerAccess"]["expectedStateHash"] == observation["fingerprint"]
                            jobs[key] = {"id": key, "kind": "SOFTWARE_DOCKER_ACCESS", "status": "UNKNOWN" if action == "GRANT" else "SUCCEEDED", "phase": "DOCKER_ACCESS_UPDATED", "errorCode": "",
                                         "traceId": "UI_FIXTURE_ACCESS", "updatedAt": stamp, "targetAckPending": False, "result": {"dockerAccess": {"action": action, "account": "operator", "member": action == "GRANT"}}}
                            engine["lastJobId"] = key
                            engine["version"] = str(int(engine["version"]) + 1)
                            if action == "REVOKE": observation["accounts"][1]["member"] = False
                        if lost_reply:
                            lost_reply = False
                            route.abort("connectionfailed")
                        else: route.fulfill(status=202, json={"data": {"jobId": key, "softwareInstanceId": engine["id"]}})
                    elif path.startswith("/jobs/") and path.endswith("/reconcile"):
                        key = path.split("/")[2]
                        jobs[key]["status"] = "SUCCEEDED"
                        observation["accounts"][1]["member"] = True
                        observation["fingerprint"] = "c" * 64
                        route.fulfill(json={"data": {"jobId": key}})
                    elif path.startswith("/jobs/") and path.split("/")[-1] in jobs:
                        route.fulfill(json={"data": jobs[path.split("/")[-1]]})
                    elif request.method not in ("GET", "HEAD", "OPTIONS"):
                        blocked.append({"path": path, "method": request.method}); route.fulfill(status=409, json={"code": "TEST_TARGET_MUTATION_BLOCKED"})
                    else: route.continue_()
                page.route("**/ops/api/v1/**", route_api)
                def software_page():
                    page.goto(base)
                    page.get_by_role("button", name="软件管理", exact=True).click()
                def open_detail(item):
                    page.locator(".software-card").filter(has=page.get_by_role("heading", name=item["name"], exact=True)).get_by_role("button", name="详情", exact=True).click()
                    drawer = page.get_by_role("dialog", name=item["name"], exact=True)
                    drawer.wait_for(); return drawer
                def close(drawer):
                    page.mouse.click(120,400); drawer.wait_for(state="hidden")
                def layout(drawer, label):
                    for width in (1440, 390):
                        page.set_viewport_size({"width": width, "height": 1000}); page.wait_for_timeout(150)
                        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
                        assert drawer.evaluate("el => el.scrollWidth <= el.clientWidth + 1")
                        page.screenshot(path=str(output / f"{label}-{width}.png"), animations="disabled")
                        checks.append(f"{label}-{width}")
                    page.set_viewport_size({"width": 1440, "height": 1000})
                software_page()
                for item in (external, container):
                    drawer = open_detail(item)
                    assert drawer.get_by_role("tab", name="账号权限", exact=True).count() == 0
                    close(drawer)
                checks.append("external-and-container-instances-have-no-account-permission-tab")
                drawer = open_detail(engine)
                drawer.get_by_role("tab", name="账号权限", exact=True).click()
                panel = drawer.get_by_label("Docker 账号权限", exact=True)
                select = panel.get_by_label("Docker 服务器账号", exact=True)
                expect(select).to_have_value("ubuntu")
                assert select.locator('option[value="disabled"]').evaluate("el => el.disabled")
                assert select.locator('option[value="root"]').count() == 0
                assert not requests
                select.select_option("operator")
                supported = False
                panel.get_by_role("button", name="检查权限", exact=True).click()
                panel.get_by_role("button", name="更新接入组件", exact=True).click()
                update = page.get_by_role("dialog", name="更新接入组件", exact=True)
                update.get_by_role("button", name="更新并继续", exact=True).click()
                update.wait_for(state="hidden")
                expect(select).to_have_value("operator")
                assert len(updates) == 1 and not requests
                checks.append("component-update-preserves-account-refreshes-versions-and-does-not-grant")
                panel.get_by_role("button", name="授予访问权限", exact=True).click()
                confirmation = panel.get_by_label("Docker 权限确认", exact=True)
                expect(confirmation).to_contain_text("相当于 root")
                assert not requests
                layout(drawer, "docker-grant-confirmation")
                confirmation.get_by_role("button", name="确认授权", exact=True).click()
                confirmation.get_by_role("button", name="重试并核对原请求", exact=True).click()
                expect(panel.get_by_role("button", name="核对原作业", exact=True)).to_be_visible()
                assert len(requests) == 2 and requests[0] == requests[1]
                checks.append("explicit-grant-lost-response-retries-exact-original-key-account-and-hash")
                page.mouse.click(120,400)
                expect(drawer).to_be_visible()
                checks.append("unknown-permission-job-blocks-mask-close")
                software_page()
                drawer = open_detail(engine)
                drawer.get_by_role("tab", name="账号权限", exact=True).click()
                panel = drawer.get_by_label("Docker 账号权限", exact=True)
                expect(panel.get_by_role("button", name="核对原作业", exact=True)).to_be_visible()
                expect(panel.get_by_role("button", name="授予访问权限", exact=True)).to_be_disabled()
                panel.get_by_role("button", name="核对原作业", exact=True).click()
                select = panel.get_by_label("Docker 服务器账号", exact=True)
                select.select_option("operator")
                expect(panel.get_by_text("已加入 docker 组", exact=True)).to_be_visible()
                expect(panel.get_by_text("docker 组权限已更新，请重新登录该账号。", exact=True)).to_be_visible()
                assert panel.get_by_role("button", name="授予访问权限", exact=True).count() == 0
                panel.get_by_text("任务详情", exact=True).click()
                job_details = panel.locator('details.access-diagnostics').filter(has=page.locator('summary', has_text='任务详情'))
                expect(job_details.get_by_text("已完成 · Docker 账号权限已更新",exact=False)).to_be_visible()
                expect(job_details).not_to_contain_text('（SUCCEEDED）')
                checks.append("docker-diagnostics-show-concise-chinese-status")
                layout(drawer, "docker-access-granted")
                checks.append("unknown-restored-after-reopen-and-reconciled-without-regrant-session-warning-visible")
                observation["socket"]["ready"] = False
                observation["socket"]["errorCode"] = "DOCKER_ACCESS_SOCKET_NOT_READY"
                panel.get_by_role("button", name="检查权限", exact=True).click()
                expect(panel.get_by_text("Docker 连接入口未就绪", exact=False)).to_be_visible()
                panel.get_by_role("button", name="收回访问权限", exact=True).click()
                confirmation = panel.get_by_label("Docker 权限确认", exact=True)
                expect(confirmation).to_contain_text("已有会话和其他授权")
                layout(drawer, "docker-revoke-confirmation")
                confirmation.get_by_role("button", name="确认收回", exact=True).click()
                expect(panel.get_by_text("已移出 docker 组", exact=False)).to_be_visible()
                expect(panel.get_by_role("button", name="授予访问权限", exact=True)).to_be_disabled()
                checks.append("revoke-works-with-missing-socket-but-grant-blocked-and-old-session-warning-kept")
                observation["accounts"][1].update(member=True, primaryGroup=True, gid="999")
                panel.get_by_role("button", name="检查权限", exact=True).click()
                expect(panel.get_by_role("button", name="收回访问权限", exact=True)).to_be_disabled()
                expect(panel.get_by_text("docker 是该账号的主组，不能在此收回。", exact=True)).to_be_visible()
                assert len(requests) == 3
                checks.append("docker-primary-group-cannot-be-revoked")
                observation["accounts"][1].update(member=False, primaryGroup=False, gid="1001")
                observation["socket"].update(ready=True, errorCode="")
                panel.get_by_role("button", name="检查权限", exact=True).click()
                panel.get_by_role("button", name="授予访问权限", exact=True).click()
                reject_next = True
                panel.get_by_role("button", name="确认授权", exact=True).click()
                expect(panel.get_by_role("alert")).to_contain_text("账号状态已变化")
                expect(panel.get_by_role("button", name="检查权限", exact=True)).to_be_enabled()
                assert panel.get_by_role("button", name="重试并核对原请求", exact=True).count() == 0
                panel.get_by_role("button", name="检查权限", exact=True).click()
                expect(panel.get_by_label("Docker 服务器账号", exact=True)).to_have_value("operator")
                panel.get_by_role("button", name="授予访问权限", exact=True).click()
                expect(panel.get_by_role("button", name="确认授权", exact=True)).to_be_enabled()
                assert len(requests) == 4
                checks.append("explicit-unqueued-rejection-unlocks-check-preserves-account-and-requires-new-confirmation")
                assert not blocked and not errors, (blocked, errors)
                assert not api("/software-instances") and not api("/jobs")["items"]
                result["status"] = "PASS"
                (output / "requests.json").write_text(json.dumps({"calls": calls, "permissionRequests": requests, "componentUpdates": updates}, ensure_ascii=False, indent=2), encoding="utf8")
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
