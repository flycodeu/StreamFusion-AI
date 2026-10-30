"""Port/firewall UI checks with an isolated controller and explicit rule fixtures.

Exercises navigation, permission gating and response-loss recovery in the UI.
It does not certify Linux/UFW packet filtering or change a target firewall.
"""
import argparse
from datetime import datetime, timedelta, timezone
import json
import pathlib
import secrets
import socket
import subprocess
import time
import urllib.request

from playwright.sync_api import sync_playwright


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--browser-executable", required=True)
    args = parser.parse_args()
    binary = str(pathlib.Path(args.binary).resolve())
    output = pathlib.Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=False)
    state = output / "state"
    password = "Install-" + secrets.token_hex(16)
    password_file = output / ".password.secret"
    password_file.write_text(password, encoding="utf8")
    try:
        subprocess.run([binary, "init-admin", "--state", str(state), "--password-file", str(password_file)], check=True, capture_output=True)
    finally:
        password_file.unlink(missing_ok=True)
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    base = f"http://127.0.0.1:{port}"
    checks, errors, preflights = [], [], []
    process = None
    with (output / "runtime.log").open("w", encoding="utf8") as runtime_log:
        try:
            process = subprocess.Popen([binary, "serve", "--state", str(state), "--listen", f"127.0.0.1:{port}"], stdout=runtime_log, stderr=runtime_log)
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
                page = browser.new_page(viewport={"width": 1440, "height": 900})
                page.on("pageerror", lambda error: errors.append(str(error)))
                page.on("request", lambda request: preflights.append(request.post_data_json) if request.method == "POST" and request.url.endswith("/software-install-plans") else None)
                page.goto(base)
                page.get_by_label("账号", exact=True).fill("admin")
                page.get_by_label("密码", exact=True).fill(password)
                page.get_by_role("button", name="登录", exact=True).click()
                page.get_by_role("heading", name="运行总览", exact=True).wait_for()

                def api(path, method="GET", data=None, expected=200):
                    csrf = page.request.get(base + "/ops/api/v1/auth/csrf").json()["data"]["csrfToken"]
                    response = page.request.fetch(base + "/ops/api/v1" + path, method=method, data=data,
                        headers={"Origin": base, "X-CSRF-Token": csrf, "Content-Type": "application/json"})
                    result = response.json()
                    assert response.status == expected, (path, response.status, result.get("code"))
                    return result["data"]

                host = api("/hosts", "POST", {"name": "端口测试主机", "kind": "LOCAL"}, 201)
                hid = host["id"]
                page.get_by_role("button", name="端口管理", exact=True).click()
                page.get_by_label("端口管理主机", exact=True).wait_for()
                assert not page.get_by_role("button", name="开放 / 禁止", exact=True).is_enabled()
                page.get_by_role("button", name="检测端口", exact=True).click()
                page.get_by_role("status").filter(has_text="防火墙读取需要").wait_for()
                assert not page.get_by_role("button", name="开放 / 禁止", exact=True).is_enabled()
                checks.extend(["real-empty-host-port-snapshot", "windows-no-fake-firewall-control", "firewall-independent-four-character-menu"])
                fixture = {"status": "INACTIVE", "code": "", "incomingPolicy":"DROP", "outgoingPolicy":"ACCEPT", "hash": "a"*64, "observedAt": "2026-10-21T08:00:00Z", "rawRules": "Status: inactive", "addedRules": "ufw allow 22/tcp", "ports": [{"protocol":"tcp","address":"0.0.0.0","port":22,"process":"sshd"},{"protocol":"udp","address":"::","port":8554,"process":"mediamtx"}], "dockerPorts": "zlm 0.0.0.0:8088->80/tcp", "managedRules": [{"id":"11111111-1111-4111-8111-111111111111","action":"allow","protocol":"tcp","source":"192.0.2.0/24","start":6379,"end":6379}], "protectedPorts":[22], "managementSource":"192.0.2.5/32", "managementPort":22}
                entries, changes, submits = [], [], []
                page.route("**/hosts/"+hid+"/ports", lambda route: route.fulfill(json={"data":{"snapshot":fixture,"code":"","attemptAt":fixture["observedAt"],"traceId":"fixture","freshness":"FRESH"}}))
                page.route("**/ops/api/v1/jobs", lambda route: route.fulfill(json={"data":entries}))
                def preflight(route):
                    data=route.request.post_data_json
                    changes.append(data)
                    if data["operation"]=="ADD" and data["rule"]["start"]==22 and data["rule"]["action"]=="deny":
                        route.fulfill(status=409,json={"code":"MANAGEMENT_PORT_PROTECTED","msg":"不能禁止管理端口","timestamp":"2026-10-21T08:00:00Z","traceId":"fixture-block","data":None})
                    else:
                        expiry=(datetime.now(timezone.utc)+timedelta(seconds=3)).isoformat() if data["operation"]=="ENABLE" else "2099-01-01T00:00:00Z"
                        route.fulfill(json={"data":{"id":"fixture-plan","change":{"operation":data["operation"],"rule":data["rule"]},"snapshot":fixture,"expiresAt":expiry}})
                page.route("**/hosts/"+hid+"/firewall-plans",preflight)
                def submit(route):
                    submits.append(route.request.post_data_json)
                    if len(submits)==1:
                        entries.append({"id":"fixture-job","kind":"FIREWALL_CHANGE","targetId":hid,"status":"UNKNOWN","phase":"TARGET_UNCONFIRMED","errorCode":"FIREWALL_RESULT_UNKNOWN","traceId":"fixture","updatedAt":fixture["observedAt"],"targetAckPending":False,"result":{}})
                        route.abort("failed")
                        return
                    route.fulfill(status=202,json={"data":{"jobId":"fixture-job"}})
                page.route("**/firewall-plans/fixture-plan/submit",submit)
                def confirm(route):
                    entries[0]["status"]="SUCCEEDED";entries[0]["phase"]="FIREWALL_APPLIED";entries[0]["errorCode"]=""
                    route.fulfill(json={"data":{"jobId":"fixture-job"}})
                page.route("**/jobs/fixture-job/firewall-confirm",confirm)
                def reconcile(route):
                    entries[0]["phase"]="FIREWALL_AWAITING_CONFIRM"
                    entries[0]["result"]={"firewallConfirmBy":"2099-01-01T00:00:00Z"}
                    route.fulfill(json={"data":{"jobId":"fixture-job"}})
                page.route("**/jobs/fixture-job/reconcile",reconcile)
                page.get_by_role("button",name="软件管理",exact=True).click()
                page.get_by_role("button",name="端口管理",exact=True).click()
                page.get_by_role("button",name="开放 / 禁止",exact=True).wait_for(state="visible")
                page.wait_for_function("() => ![...document.querySelectorAll('button')].find(b=>b.textContent.trim()==='开放 / 禁止')?.disabled")
                assert page.get_by_role("cell",name="8554",exact=True).count()==1
                page.get_by_text("Docker 发布端口",exact=True).click()
                page.get_by_text("zlm 0.0.0.0:8088->80/tcp",exact=True).wait_for()
                page.screenshot(path=str(output / "ports-wide.png"),animations="disabled")
                page.get_by_role("button",name="开放 / 禁止",exact=True).click()
                drawer=page.get_by_role("dialog",name="新增访问规则",exact=True)
                drawer.get_by_label("访问策略",exact=True).select_option("deny")
                drawer.get_by_label("端口",exact=True).fill("22")
                drawer.get_by_role("button",name="预检规则",exact=True).click()
                drawer.get_by_role("alert").filter(has_text="MANAGEMENT_PORT_PROTECTED").wait_for()
                checks.append("management-deny-error-code-displayed")
                drawer.get_by_label("端口",exact=True).fill("8555")
                drawer.get_by_label("端口协议",exact=True).select_option("udp")
                drawer.get_by_label("访问来源",exact=True).select_option("custom")
                drawer.get_by_label("来源 IP 或网段",exact=True).fill("10.0.0.0/24")
                page.wait_for_timeout(5000)
                assert drawer.get_by_label("端口",exact=True).input_value()=="8555"
                for width in (1440,390):
                    page.set_viewport_size({"width":width,"height":900})
                    if not page.evaluate("document.documentElement.scrollWidth <= innerWidth"):
                        page.screenshot(path=str(output / f"overflow-{width}.png"),animations="disabled")
                        print(page.evaluate("() => [...document.querySelectorAll('body *')].filter(e=>e.getBoundingClientRect().right > innerWidth+2).slice(0,12).map(e=>({tag:e.tagName,cls:e.className,w:e.getBoundingClientRect().width}))"))
                    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
                    assert drawer.evaluate("e => e.scrollWidth <= e.clientWidth + 1")
                    page.screenshot(path=str(output / f"port-form-{width}.png"),animations="disabled")
                page.set_viewport_size({"width":1440,"height":900})
                drawer.get_by_role("button",name="预检规则",exact=True).click()
                drawer.get_by_role("button",name="确认执行",exact=True).wait_for()
                assert changes[-1]["rule"]["source"]=="10.0.0.0/24" and changes[-1]["rule"]["protocol"]=="udp"
                drawer.get_by_role("button",name="取消",exact=True).click()
                drawer.wait_for(state="hidden")
                page.get_by_role("tab",name="访问规则",exact=True).click()
                page.get_by_text("防火墙已停用，保存的访问规则在启用后生效。",exact=True).wait_for()
                page.get_by_text("系统规则与默认策略",exact=True).click()
                page.get_by_text("默认入站：DROP · 默认出站：ACCEPT",exact=True).wait_for()
                page.get_by_role("button",name="启用防火墙",exact=True).click()
                drawer=page.get_by_role("dialog",name="启用防火墙",exact=True)
                assert "120 秒" in drawer.inner_text()
                drawer.get_by_role("button",name="预检规则",exact=True).click()
                drawer.get_by_role("button",name="确认执行",exact=True).click()
                retry=drawer.get_by_role("button",name="重试并找回作业",exact=True)
                retry.wait_for()
                page.wait_for_timeout(3500)
                assert retry.is_enabled()
                assert not drawer.get_by_role("button",name="返回修改",exact=True).is_enabled()
                retry.click()
                drawer.wait_for(state="hidden")
                assert len(submits)==2 and submits[0]["idempotencyKey"]==submits[1]["idempotencyKey"]
                assert page.get_by_role("button",name="确认保持",exact=True).count()==0
                page.get_by_role("button",name="核对原作业",exact=True).click()
                page.get_by_role("button",name="确认保持",exact=True).click()
                page.get_by_role("cell",name="成功",exact=True).wait_for()
                entries.append({**entries[0],"id":"expired-job","status":"UNKNOWN","phase":"FIREWALL_AWAITING_CONFIRM","result":{"firewallConfirmBy":"2000-01-01T00:00:00Z"}})
                page.get_by_role("row").filter(has_text="expired-").wait_for()
                assert not page.get_by_role("row").filter(has_text="expired-").get_by_role("button",name="确认保持",exact=True).is_enabled()
                checks.extend(["lost-target-reply-reconcile-restores-confirmation", "expired-global-confirmation-disabled"])
                checks.extend(["tcp-udp-ipv6-listening-fixture", "docker-publication-separate-view", "source-cidr-and-protocol-reach-preflight", "poll-does-not-reset-open-rule-form", "wide-narrow-rule-dialog-no-overflow", "global-firewall-explicit-120-second-confirm", "lost-response-job-confirmation-ui", "expired-plan-retry-keeps-original-idempotency-key", "inactive-rules-and-default-policies-visible"])
                for width in (1440,390):
                    page.set_viewport_size({"width":width,"height":900})
                    if not page.evaluate("document.documentElement.scrollWidth <= innerWidth"):
                        page.screenshot(path=str(output / f"overflow-{width}.png"),animations="disabled")
                        print(page.evaluate("() => [...document.querySelectorAll('body *')].filter(e=>e.getBoundingClientRect().right > innerWidth+2).slice(0,12).map(e=>({tag:e.tagName,cls:e.className,w:e.getBoundingClientRect().width}))"))
                    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
                    page.screenshot(path=str(output / f"port-jobs-{width}.png"),animations="disabled")
                assert not errors,errors
                browser.close()
            result = {"passed": checks, "pageErrors": errors, "backend": "real isolated controller", "firewallChanges": 0, "uiFixtures": "port/rule/plans/jobs only; no Linux firewall modified", "linuxDockerAcceptance": False}
            (output / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf8")
            print(json.dumps({"passed": len(checks), "pageErrors": errors, "linuxDockerAcceptance": False}))
        finally:
            if process and process.poll() is None:
                subprocess.run([binary, "stop", "--state", str(state)], capture_output=True, timeout=20)
                process.wait(timeout=15)


if __name__ == "__main__":
    main()
