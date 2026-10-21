"""Browser/API checks against an isolated Ops controller, without remote installs.

Exercises real catalog, credentials, material rejection and preflight validation.
It does not certify Linux/Docker installation or replace protocol acceptance.
"""
import argparse
import hashlib
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

                host = api("/hosts", "POST", {"name": "安装界面隔离主机", "kind": "LOCAL"}, 201)
                definitions = api("/software-recipes")
                assert {item["key"] for item in definitions} == {"mysql", "redis", "nginx", "seaweedfs", "mediamtx", "zlm", "java", "maven", "python", "ffmpeg", "codex", "claude"}
                assert all(item["native"]["parts"] and ("DOCKER" not in item["runtimeModes"] or (item["digest"].startswith("sha256:") and item["imageId"].startswith("sha256:"))) for item in definitions)
                checks.append("real-twelve-software-thirteen-definitions-supported-modes")

                access_value, secret_value = secrets.token_hex(10), secrets.token_hex(24)
                access = api("/credentials", "POST", {"name": "S3测试访问键", "kind": "TOKEN", "secret": access_value}, 201)
                secret = api("/credentials", "POST", {"name": "S3测试秘密键", "kind": "PASSWORD", "secret": secret_value}, 201)
                page.get_by_role("button", name="软件管理", exact=True).click()
                page.get_by_role("tab", name="软件目录", exact=True).click()

                def open_install(name):
                    heading = page.get_by_role("heading", name=name, exact=True)
                    heading.wait_for()
                    page.locator("article").filter(has=heading).get_by_role("button", name="安装", exact=True).click()
                    drawer = page.get_by_role("dialog", name="安装 " + name, exact=True)
                    drawer.wait_for()
                    return drawer

                def close_install(drawer):
                    drawer.get_by_role("button", name="取消", exact=True).click()
                    drawer.wait_for(state="hidden")

                for name in ("MySQL", "Redis", "Nginx", "SeaweedFS", "MediaMTX", "ZLMediaKit", "Java", "Maven", "Python", "FFmpeg", "Codex CLI", "Claude Code"):
                    page.get_by_role("heading", name=name, exact=True).wait_for()
                checks.append("twelve-software-cards-visible")
                page.get_by_role("button", name="编程软件", exact=True).click()
                assert page.locator("article.plugin-card").count() == 2
                drawer = open_install("Claude Code")
                assert drawer.get_by_role("radio", name="Docker 容器", exact=False).count() == 0
                drawer.get_by_role("radio", name="离线归档", exact=False).check()
                drawer.get_by_text("上传离线安装包", exact=True).click()
                assert drawer.get_by_label("原生安装归档", exact=True).get_attribute("accept") == ""
                assert "原始二进制" in drawer.inner_text()
                for width in (1440, 390):
                    page.set_viewport_size({"width": width, "height": 900})
                    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
                    assert drawer.evaluate("element => element.scrollWidth <= element.clientWidth + 1")
                    page.screenshot(path=str(output / f"programming-raw-{width}.png"), animations="disabled")
                page.set_viewport_size({"width": 1440, "height": 900})
                close_install(drawer)
                page.get_by_role("button", name="全部软件", exact=True).click()
                # Native configuration layout fixture only: no vendor download
                # or installation is claimed by this intercepted material list.
                codex = next(item for item in definitions if item["key"] == "codex")
                material_route = "**/installation-materials?recipeKey=codex*"
                page.route(material_route, lambda route: route.fulfill(json={"data": [{"id": "native-layout-fixture", "name": "界面测试材料", "recipeVersion": codex["version"], "recipeRevision": codex["revision"], "runtimeMode": "NATIVE", "available": True, "status": "AVAILABLE"}]}))
                drawer = open_install("Codex CLI")
                drawer.get_by_label("已准备的官方材料", exact=True).select_option("native-layout-fixture")
                drawer.get_by_role("button", name="下一步", exact=True).click()
                assert drawer.get_by_role("button", name="新建凭据", exact=True).count() == 0
                assert "自己的 SSH" in drawer.inner_text()
                drawer.get_by_role("button", name="下一步", exact=True).click()
                assert drawer.get_by_label("宿主机访问端口", exact=True).count() == 0
                close_install(drawer)
                page.unroute(material_route)
                page.get_by_role("button", name="运行环境", exact=True).click()
                assert page.locator("article.plugin-card").count() == 2
                page.get_by_role("heading", name="Python", exact=True).wait_for()
                page.get_by_role("button", name="媒体工具", exact=True).click()
                assert page.locator("article.plugin-card").count() == 1
                page.get_by_role("heading", name="FFmpeg", exact=True).wait_for()
                page.get_by_role("button", name="全部软件", exact=True).click()
                assert page.get_by_role("heading", name="MinIO", exact=True).count() == 0
                checks.extend(["programming-category-two-tools", "native-only-tools-no-fake-docker-channel", "claude-raw-offline-material-chooser", "programming-no-auth-secrets-or-listening-ports", "python-runtime-category", "ffmpeg-media-tool-category", "minio-readonly-no-unavailable-install-card"])
                drawer = open_install("Nginx")
                native_option = drawer.locator("label").filter(has_text="原生 / systemd")
                assert native_option.get_by_role("radio").is_enabled()
                assert "官方材料" in native_option.inner_text()
                drawer.get_by_role("button", name="下一步", exact=True).click()
                drawer.get_by_label("实例名称", exact=True).fill("nginx-test-8088")
                assert drawer.get_by_role("button", name="新建凭据", exact=True).count() == 0
                drawer.get_by_role("button", name="下一步", exact=True).click()
                drawer.get_by_label("宿主机监听地址", exact=True).fill("127.0.0.1")
                drawer.get_by_label("宿主机访问端口", exact=True).fill("0")
                before = len(preflights)
                drawer.get_by_role("button", name="检查安装条件", exact=True).click()
                drawer.get_by_role("alert").wait_for()
                assert len(preflights) == before
                drawer.get_by_label("宿主机访问端口", exact=True).fill("18088")
                drawer.get_by_label("存储根目录", exact=True).fill("/srv/ops-installation-check")
                # The catalog refreshes host objects every four seconds. A
                # stable host identity must not reset the open installation.
                page.wait_for_timeout(9000)
                assert drawer.get_by_label("宿主机访问端口", exact=True).input_value() == "18088"
                assert drawer.get_by_label("存储根目录", exact=True).input_value() == "/srv/ops-installation-check"
                drawer.get_by_role("button", name="检查安装条件", exact=True).click()
                page.wait_for_function("() => !!document.querySelector('.install-error code')")
                assert preflights[-1]["config"]["hostPort"] == 18088
                assert preflights[-1]["config"]["bindAddress"] == "127.0.0.1"
                assert preflights[-1]["config"]["name"] == "nginx-test-8088"
                assert "traceId=" in drawer.get_by_role("alert").inner_text()
                checks.extend(["native-mode-selectable", "nginx-no-false-password-fields", "invalid-port-blocked-before-request", "edited-port-and-address-reach-real-preflight", "preflight-failure-includes-code-time-trace"])
                checks.append("installation-draft-survives-two-catalog-refreshes")
                close_install(drawer)

                drawer = open_install("SeaweedFS")
                drawer.get_by_role("button", name="下一步", exact=True).click()
                drawer.get_by_label("S3 Access Key", exact=True).select_option(access["id"] + ":" + access["revision"])
                drawer.get_by_label("S3 Secret Key", exact=True).select_option(secret["id"] + ":" + secret["revision"])
                drawer.get_by_role("button", name="下一步", exact=True).click()
                drawer.get_by_label("宿主机访问端口", exact=True).fill("18334")
                drawer.get_by_role("button", name="检查安装条件", exact=True).click()
                page.wait_for_function("() => !!document.querySelector('.install-error code')")
                last = preflights[-1]
                assert last["config"]["accessKeyCredentialId"] == access["id"]
                assert last["config"]["credentialId"] == secret["id"]
                assert last["config"]["hostPort"] == 18334
                assert all(value not in json.dumps(last) for value in (access_value, secret_value))
                checks.extend(["s3-edited-port-and-two-encrypted-credential-references", "no-credential-content-in-install-plan-request"])
                close_install(drawer)

                drawer = open_install("Nginx")
                drawer.get_by_role("radio", name="离线归档", exact=False).check()
                drawer.get_by_text("上传离线镜像", exact=True).click()
                bad_archive = b"invalid docker archive\n" + bytes(4096)
                drawer.get_by_label("镜像归档", exact=True).set_input_files({"name": "invalid.tar", "mimeType": "application/x-tar", "buffer": bad_archive})
                drawer.get_by_label("归档 SHA-256", exact=True).fill(hashlib.sha256(bad_archive).hexdigest())
                uploads = []
                page.on("request", lambda request: uploads.append(request.url) if request.method == "PUT" and "/installation-materials/" in request.url else None)
                # The upload reaches the real controller, but confirmation loses
                # its connection. Retry must reuse UPLOADED rather than PUT again.
                page.route("**/installation-materials/*/confirm", lambda route: route.abort("connectionfailed"))
                drawer.get_by_role("button", name="上传并校验", exact=True).click()
                drawer.locator(".install-error").wait_for()
                uploaded = api("/installation-materials")
                assert len(uploaded) == 1 and uploaded[0]["status"] == "UPLOADED"
                assert len(uploads) == 1
                page.unroute("**/installation-materials/*/confirm")
                drawer.get_by_role("button", name="上传并校验", exact=True).click()
                page.wait_for_function("() => document.querySelector('.install-error')?.textContent.includes('INSTALL_ARCHIVE_REJECTED')")
                materials = api("/installation-materials")
                assert len(materials) == 1 and materials[0]["status"] == "FAILED" and not materials[0]["available"]
                assert len(uploads) == 1
                assert not api("/software-instances")
                checks.extend(["real-offline-upload-and-invalid-archive-rejection", "confirmation-connection-loss-reuses-uploaded-material-without-second-put", "failed-material-not-available-no-fake-installed-instance"])
                close_install(drawer)

                for width in (1440, 1280, 390):
                    page.set_viewport_size({"width": width, "height": 900})
                    drawer = open_install("SeaweedFS")
                    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), width
                    assert drawer.evaluate("element => element.scrollWidth <= element.clientWidth + 1"), width
                    footer = drawer.locator(".install-footer").bounding_box()
                    assert footer and footer["y"] >= 0 and footer["y"] + footer["height"] <= 901
                    page.screenshot(path=str(output / f"install-{width}.png"), animations="disabled")
                    close_install(drawer)
                    checks.append(f"installation-drawer-layout-{width}")
                page.set_viewport_size({"width": 1440, "height": 900})
                page.get_by_role("tab", name="软件中心", exact=True).click()
                for typ, name in (("MEDIAMTX", "已有媒体服务"), ("MINIO", "已有对象存储")):
                    page.get_by_role("button", name="登记已有", exact=True).first.click()
                    registration = page.get_by_role("dialog", name="登记已有软件", exact=True)
                    registration.get_by_label("软件名称", exact=True).fill(name)
                    registration.get_by_label("软件类型", exact=True).select_option(typ)
                    registration.get_by_label("systemd unit", exact=True).fill(typ.lower() + ".service")
                    registration.get_by_role("button", name="只读登记", exact=True).click()
                    registration.wait_for(state="hidden")
                    detail = page.locator(".el-drawer:visible").last
                    detail.get_by_role("button", name="Close this dialog", exact=True).click()
                    detail.wait_for(state="hidden")
                rows = api("/software-instances")
                assert len(rows) == 2 and all(row["managementMode"] == "OBSERVE" and row["dataOwnership"] == "EXTERNAL" for row in rows)
                assert {row["softwareType"] for row in rows} == {"MEDIAMTX", "MINIO"}
                checks.append("real-ui-registration-mediamtx-minio-readonly-external")
                # Explicit managed-service UI fixture for configuration/firewall
                # separation. No remote service or firewall is modified.
                nginx = next(item for item in definitions if item["key"] == "nginx")
                config = {"name":"network-fixture","runtimeMode":"DOCKER","bindAddress":"127.0.0.1","hostPort":18088,"memoryMiB":256,"ports":{}}
                configured = {**rows[0],"id":"11111111-1111-4111-8111-111111111111","name":"配置联动测试","softwareType":"NGINX","runtimeKind":"DOCKER","locator":"a"*64,"managementMode":"MANAGED","actualState":"RUNNING","desiredState":"RUNNING","installationStatus":"READY","lastJobId":"","installation":{"definition":nginx,"config":config}}
                page.route("**/ops/api/v1/software-instances",lambda route: route.fulfill(json={"data":[*rows,configured]}))
                def configuration_plan(route):
                    draft=route.request.post_data_json["config"]
                    route.fulfill(json={"data":{"id":"config-fixture","previousConfig":config,"config":draft,"expiresAt":"2099-01-01T00:00:00Z","checks":[],"portMappings":[{"address":draft["bindAddress"],"hostPort":draft["hostPort"],"containerPort":80,"protocol":"tcp"}]}})
                page.route("**/software-instances/"+configured["id"]+"/configuration-plans",configuration_plan)
                page.get_by_role("button",name="运行总览",exact=True).click()
                page.get_by_role("button",name="软件管理",exact=True).click()
                card=page.locator(".software-card").filter(has_text="配置联动测试")
                card.get_by_role("button",name="详情",exact=True).click()
                detail=page.get_by_role("dialog",name="配置联动测试",exact=True)
                detail.get_by_role("tab",name="服务配置",exact=True).click()
                detail.get_by_role("button",name="调整配置",exact=True).click()
                detail.get_by_label("宿主机访问端口",exact=True).fill("18089")
                detail.get_by_role("button",name="检查配置",exact=True).click()
                detail.get_by_text("端口或地址变更后，旧防火墙规则保留。请到“端口管理”重新检测并按需调整访问规则。",exact=True).wait_for()
                for width in (1440,390):
                    page.set_viewport_size({"width":width,"height":900})
                    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
                    assert detail.evaluate("e=>e.scrollWidth<=e.clientWidth+1")
                    page.screenshot(path=str(output/f"configuration-firewall-{width}.png"),animations="disabled")
                checks.append("software-port-change-shows-firewall-followup-wide-narrow")
                assert not errors, errors
                browser.close()
            result = {"passed": checks, "pageErrors": errors, "backend": "real isolated controller", "uiFixtures": ["prepared programming material list", "managed-service configuration plan only"], "remoteInstalls": 0, "linuxDockerAcceptance": False}
            (output / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf8")
            print(json.dumps({"passed": len(checks), "pageErrors": errors, "linuxDockerAcceptance": False}))
        finally:
            if process and process.poll() is None:
                subprocess.run([binary, "stop", "--state", str(state)], capture_output=True, timeout=20)
                process.wait(timeout=15)


if __name__ == "__main__":
    main()
