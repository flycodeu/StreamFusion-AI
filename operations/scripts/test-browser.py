"""Browser acceptance for a built I01 controller. Creates isolated test state only."""
import argparse
import json
import pathlib
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
        page.goto(url)
        page.get_by_label("账号", exact=True).fill("admin")
        page.get_by_label("密码", exact=True).fill(password)
        page.get_by_role("button", name="登录", exact=True).click()
        page.get_by_role("heading", name="总览", exact=True).wait_for()
        page.get_by_text("控制器运行中", exact=True).wait_for()
        page.screenshot(path=str(output / "overview-wide.png"), full_page=True)
        page.reload()
        page.get_by_role("heading", name="总览", exact=True).wait_for()
        checks.extend(["login", "session-after-reload", "real-status"])
        page.get_by_role("button", name="凭据", exact=True).click()
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
        page.get_by_role("button", name="控制器设置", exact=True).click()
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
        page.get_by_role("button", name="主机与依赖", exact=True).click()
        page.get_by_role("button", name="登记目标", exact=True).click()
        dialog = page.get_by_role("dialog", name="登记目标", exact=True)
        dialog.get_by_label("名称", exact=True).fill("浏览器测试目标")
        dialog.get_by_label("受管目录", exact=True).fill("/opt/browser-target")
        dialog.get_by_role("button", name="保存目标", exact=True).click()
        dialog.wait_for(state="hidden")
        page.get_by_role("cell", name="浏览器测试目标", exact=True).wait_for()
        page.get_by_role("button", name="插件中心", exact=True).click()
        page.get_by_role("heading", name="环境插件", exact=True).wait_for()
        page.wait_for_function("() => document.querySelectorAll('.plugin-card').length === 3")
        assert page.get_by_label("目标主机", exact=True).input_value()
        assert page.get_by_role("heading", name="Maven 构建环境", exact=True).count() == 1
        assert page.get_by_role("button", name="安装", exact=True).count() == 3
        for button in page.get_by_role("button", name="安装", exact=True).all():
            assert button.is_disabled()
        assert page.get_by_label("宿主端口", exact=True).count() == 0
        assert page.get_by_label("控制器名称", exact=True).count() == 0
        page.wait_for_function("() => document.querySelectorAll('.el-message').length === 0")
        page.screenshot(path=str(output / "plugins-wide.png"), full_page=True)
        checks.extend(["I02-P1-catalog-three-versions", "I02-P1-no-docker-install-disabled", "I02-P1-no-service-port-or-settings-form"])
        page.get_by_role("button", name="主机与依赖", exact=True).click()
        page.get_by_role("button", name="绑定已有服务", exact=True).click()
        dialog = page.get_by_role("dialog", name="绑定已有依赖", exact=True)
        dialog.get_by_label("类型", exact=True).select_option("REDIS")
        dialog.get_by_label("地址", exact=True).fill("127.0.0.1")
        dialog.get_by_role("button", name="登记绑定", exact=True).click()
        dialog.wait_for(state="hidden")
        page.get_by_role("cell", name="REDIS", exact=True).wait_for()
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
        page.get_by_role("button", name="控制器设置", exact=True).click()
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
        page.get_by_role("button", name="总览", exact=True).click()
        page.get_by_role("heading", name="测试控制器", exact=True).wait_for()
        checks.append("password-change-relogin")
        page.set_viewport_size({"width": 390, "height": 844})
        for name in ["总览", "主机与依赖", "插件中心", "应用实例", "制品", "发布作业", "凭据", "操作记录", "控制器设置"]:
            page.get_by_role("button", name=name, exact=True).click()
            if name == "插件中心":
                page.get_by_role("heading", name="环境插件", exact=True).wait_for()
                page.wait_for_function("() => document.querySelectorAll('.plugin-card').length === 3")
                page.wait_for_function("() => document.querySelectorAll('.el-message').length === 0")
                page.screenshot(path=str(output / "plugins-narrow.png"), full_page=True)
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), name
        page.get_by_role("button", name="总览", exact=True).click()
        page.wait_for_function("() => document.querySelectorAll('.el-message').length === 0")
        page.screenshot(path=str(output / "overview-narrow.png"), full_page=True)
        checks.append("nine-pages-narrow-no-overflow")
        page.get_by_role("button", name="退出", exact=True).click()
        page.get_by_role("heading", name="控制器登录", exact=True).wait_for()
        checks.append("logout")
        browser.close()
    assert not errors, errors
    result = {"passed": checks, "pageErrors": errors, "backend": "real binary / isolated state", "productionAcceptance": False}
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
