"""Isolated command-editor acceptance. Never submits target operations.

Uses the real controller, seeded built-in recipes, authentication and persistence.
Only one draft is edited and published in this script's new temporary state.
"""
import argparse
import json
from pathlib import Path
import secrets
import socket
import subprocess
import time
import traceback
from urllib.parse import urlsplit
import urllib.request

from playwright.sync_api import expect, sync_playwright
from software_management_ui_fixture import check_management_ui


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--browser-executable", required=True)
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve())
    output = Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=False)
    state, library = output / "state", output / "empty-library"
    library.mkdir()
    (library / "library.json").write_text('{"schemaVersion":1,"materials":[]}', encoding="utf8")
    flags = getattr(subprocess, "CREATE_NO_WINDOW", 0)
    username, password = "editor_" + secrets.token_hex(5), "Editor-" + secrets.token_hex(16)
    password_file = output / ".password.secret"
    process, browser, page = None, None, None
    checks, errors, blocked = [], [], []
    result = {"status": "RUNNING", "checks": checks, "pageErrors": errors, "blockedMutations": blocked,
              "backend": "real isolated controller, real seeded templates, real draft and publication",
              "remoteInstalls": 0, "linuxAcceptance": False}
    try:
        password_file.write_text(password, encoding="utf8")
        try:
            initialized = subprocess.run([binary, "init-admin", "--state", str(state), "--username", username,
                                          "--password-file", str(password_file)], capture_output=True,
                                         timeout=30, creationflags=flags)
            if initialized.returncode:
                raise RuntimeError(initialized.stderr.decode("utf8", errors="replace").strip())
        finally:
            password_file.unlink(missing_ok=True)
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        with (output / "runtime.log").open("w", encoding="utf8") as runtime_log:
            process = subprocess.Popen([binary, "serve", "--state", str(state), "--listen", f"127.0.0.1:{port}",
                                        "--material-library", str(library)], stdout=runtime_log,
                                       stderr=runtime_log, creationflags=flags)
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
                page.set_default_timeout(15000)
                page.on("pageerror", lambda error: errors.append(str(error)))

                def prevent_target_operations(route):
                    path = urlsplit(route.request.url).path.removeprefix("/ops/api/v1")
                    if route.request.method != "GET" and not path.startswith(("/auth/", "/command-templates")):
                        blocked.append(path)
                        route.fulfill(status=409, json={"code": "TEST_MUTATION_BLOCKED", "msg": "Isolated editor test"})
                    else:
                        route.continue_()
                page.route("**/ops/api/v1/**", prevent_target_operations)
                page.goto(base)
                page.get_by_label("账号", exact=True).fill(username)
                page.get_by_label("密码", exact=True).fill(password)
                page.get_by_role("button", name="登录", exact=True).click()
                page.get_by_role("heading", name="运行总览", exact=True).wait_for()

                def get(path):
                    response = page.request.get(base + "/ops/api/v1" + path)
                    assert response.status == 200, (path, response.status)
                    return response.json()["data"]

                records = get("/command-templates")
                builtins = [item for item in records if item["draft"].get("commands", {}).get("builtin")]
                assert len(builtins) == 33, len(builtins)
                assert all(item.get("published") and item["draft"]["commands"]["steps"] for item in builtins)
                checks.append("all-33-builtins-seeded-as-published-with-real-steps")
                page.get_by_role("button", name="软件管理", exact=True).click()
                assert page.get_by_role("tab", name="软件配置", exact=True).count() == 0
                page.get_by_role("button", name="软件配置", exact=True).click()
                workspace = page.locator(".recipe-workspace")
                workspace.locator(".software-option").first.wait_for()
                editor_dialog = page.locator(".command-editor.el-dialog")
                expect(editor_dialog).to_have_count(0)
                expect(page.locator(".content.settings")).to_have_count(0)
                page.screenshot(path=str(output / "editor-library-1440.png"), animations="disabled")
                checks.append("standalone-menu-and-no-software-management-configuration-tab")

                for name in ("Java", "MySQL", "Nginx"):
                    workspace.get_by_label("搜索软件配置", exact=True).fill(name)
                    option = workspace.locator(".software-option").filter(has=page.get_by_text(name, exact=True))
                    expect(option).to_have_count(1)
                    option.click()
                    expect(editor_dialog.get_by_role("heading", name=f"软件配置 · {name}", exact=True)).to_be_visible()
                    expect(editor_dialog.locator(".phase-tabs button")).to_have_count(2)
                    expect(editor_dialog.get_by_label("步骤命令", exact=True)).to_have_count(0)
                    expect(editor_dialog.get_by_role("button", name="添加步骤", exact=True)).to_be_visible()
                    system_steps = editor_dialog.locator(".step-option").filter(has_text="平台处理")
                    assert system_steps.count() > 0
                    system_steps.first.click()
                    expect(editor_dialog.locator(".system-step-details")).to_be_visible()
                    assert editor_dialog.locator(".system-step-details dd").count() > 0
                    assert editor_dialog.get_by_role("button", name="删除步骤", exact=True).count() == 0
                    commands = editor_dialog.locator(".step-option").filter(has_not_text="平台处理")
                    if commands.count():
                        commands.first.click()
                        expect(editor_dialog.get_by_label("步骤命令", exact=True)).not_to_have_value("")
                        expect(editor_dialog.get_by_role("button", name="删除步骤", exact=True)).to_be_visible()
                    # Choosing a supported view never edits the frozen metadata.
                    runtime = editor_dialog.get_by_label("运行方式", exact=True)
                    runtimes = runtime.locator("option").evaluate_all("items => items.map(item => item.value)")
                    assert runtimes, "runtime selector must expose supported modes"
                    for value in runtimes:
                        runtime.select_option(value)
                        source = editor_dialog.get_by_label("安装来源", exact=True).first
                        sources = source.locator("option").evaluate_all("items => items.map(item => item.value)")
                        assert sources, "source selector must expose supported sources"
                        for selected_source in sources:
                            source.select_option(selected_source)
                            assert editor_dialog.locator(".step-option").count() > 0, (name, value, selected_source)
                    editor_dialog.get_by_role("button", name="仅看命令", exact=True).click()
                    expect(editor_dialog.locator(".step-option.automatic")).to_have_count(0)
                    editor_dialog.get_by_role("button", name="查看完整流程", exact=True).click()
                    page.screenshot(path=str(output / f"editor-{name.lower()}-1440.png"), animations="disabled")
                    page.mouse.click(4, 4)
                    expect(editor_dialog).not_to_be_visible()
                    checks.append(name.lower() + "-grouped-and-filtered-runtime-source-steps")

                # Save a harmless comment through the UI. No target exists here.
                workspace.get_by_label("搜索软件配置", exact=True).fill("Java")
                workspace.locator(".software-option").filter(has=page.get_by_text("Java", exact=True)).click()
                expect(editor_dialog.get_by_role("heading", name="软件配置 · Java", exact=True)).to_be_visible()
                page.screenshot(path=str(output / "editor-java-before-edit.png"), animations="disabled")
                runtime = editor_dialog.get_by_label("运行方式", exact=True)
                runtime.select_option("NATIVE")
                source = editor_dialog.get_by_label("安装来源", exact=True).first
                sources = source.locator("option").evaluate_all("items => items.map(item => item.value)")
                source.select_option("ONLINE" if "ONLINE" in sources else sources[0])
                system_steps = editor_dialog.locator(".step-option").filter(has_text="平台处理")
                system_steps.first.click()
                assert editor_dialog.locator(".system-step-details").inner_text().strip()
                if source.input_value() == "ONLINE":
                    assert editor_dialog.locator(".system-step-details a[href^='https://']").count() > 0
                page.screenshot(path=str(output / "editor-material-details-1440.png"), animations="disabled")
                checks.append("system-steps-show-real-material-sources-and-protect-actions")

                command_steps = editor_dialog.locator(".step-option").filter(has_not_text="平台处理")
                command_steps.first.click()
                original_names = editor_dialog.locator(".step-option strong").all_text_contents()
                editor_dialog.get_by_role("button", name="添加步骤", exact=True).click()
                expect(editor_dialog.get_by_label("步骤名称", exact=True)).to_have_value("新步骤")
                editor_dialog.get_by_label("步骤名称", exact=True).fill("独立检查")
                editor_dialog.get_by_label("步骤命令", exact=True).fill("printf checked")
                editor_dialog.locator(".step-advanced summary").click()
                expect(editor_dialog.get_by_label("执行账号", exact=True)).to_have_value("ROOT")
                editor_dialog.locator(".step-advanced summary").click()
                before = editor_dialog.locator(".step-option.active .step-number").inner_text()
                editor_dialog.get_by_role("button", name="上移步骤", exact=True).click()
                after = editor_dialog.locator(".step-option.active .step-number").inner_text()
                assert int(after) == int(before) - 1
                editor_dialog.get_by_role("button", name="删除步骤", exact=True).click()
                assert editor_dialog.locator(".step-option strong").all_text_contents() == original_names
                checks.append("built-in-command-add-move-delete-with-default-sudo")

                command_steps.first.click()
                editor_dialog.get_by_role("button", name="添加步骤", exact=True).click()
                editor_dialog.get_by_label("步骤名称", exact=True).fill("确认安装环境")
                editor = editor_dialog.get_by_label("步骤命令", exact=True)
                initial = "printf 'editor flow check\\n'"
                marker = "# isolated command editor regression"
                edited = initial + "\n" + marker
                editor.fill(edited)
                page.mouse.click(4, 4)
                page.get_by_role("button", name="继续编辑", exact=True).click()
                expect(editor).to_have_value(edited)
                checks.append("mask-click-closes-clean-editor-and-confirms-unsaved-commands")
                with page.expect_response(lambda response: response.request.method == "PUT" and "/command-templates/" in response.url):
                    editor_dialog.get_by_role("button", name="保存草稿", exact=True).click()
                current = next(item for item in get("/command-templates") if any(marker in step["script"] for step in item["draft"]["commands"]["steps"]))
                assert not any(marker in step["script"] for step in current["published"]["commands"]["steps"])
                editor_dialog.get_by_role("button", name="预览发布", exact=True).click()
                expect(editor_dialog.locator(".preview-step[open]")).to_have_count(0)
                editor_dialog.locator(".preview-step > summary").filter(has_text="确认安装环境").click()
                expect(editor_dialog.locator(".preview-step[open] pre")).to_have_text(edited)
                with page.expect_response(lambda response: response.request.method == "POST" and response.url.endswith("/publish")):
                    editor_dialog.get_by_role("button", name="确认发布", exact=True).click()
                published = next(item for item in get("/command-templates") if item["id"] == current["id"])
                created_step = next(step for step in published["published"]["commands"]["steps"] if marker in step["script"])
                assert created_step["id"].startswith("command-")
                assert created_step["runAs"] == "ROOT"
                assert created_step["runtimeMode"] == "NATIVE"
                assert created_step["sourceModes"] == [source.input_value()]
                assert published["published"]["commands"]["flowVersion"] == 1
                assert published["published"]["revision"] == current["published"]["revision"]
                assert published["published"]["commands"]["revision"] > current["published"]["commands"]["revision"]
                assert published["published"].get("native") == current["published"].get("native")
                checks.append("draft-edit-and-publication-persist-without-changing-material-revision")
                editor_dialog.locator(".step-option").filter(has_not_text="平台处理").first.click()
                for width, height in ((1440, 1000), (1120, 768), (390, 600)):
                    page.set_viewport_size({"width": width, "height": height})
                    body = editor_dialog.locator(".el-dialog__body")
                    footer = editor_dialog.locator(".el-dialog__footer")
                    assert editor_dialog.evaluate("el => el.scrollWidth <= el.clientWidth + 1")
                    assert body.evaluate("el => el.scrollWidth <= el.clientWidth + 1")
                    expect(editor_dialog.get_by_role("button", name="保存草稿", exact=True)).to_be_in_viewport()
                    editor_dialog.locator(".step-advanced summary").click()
                    expect(editor_dialog.get_by_label("执行账号", exact=True)).to_be_visible()
                    footer_y = footer.bounding_box()["y"]
                    body.evaluate("el => el.scrollTop = el.scrollHeight")
                    assert footer.bounding_box()["y"] == footer_y
                    editor_dialog.get_by_label("步骤命令", exact=True).scroll_into_view_if_needed()
                    assert editor_dialog.get_by_label("步骤命令", exact=True).bounding_box()["height"] >= 120
                    page.screenshot(path=str(output / f"editor-final-{width}-{height}.png"), animations="disabled")
                    editor_dialog.locator(".step-advanced summary").click()
                checks.append("modal-content-scrolls-and-footer-stays-visible-on-short-and-narrow-screens")
                page.set_viewport_size({"width": 1440, "height": 1000})
                page.mouse.click(4, 4)
                expect(editor_dialog).not_to_be_visible()
                workspace.get_by_role("button", name="新增软件", exact=True).click()
                editor_dialog.get_by_label("更多配置操作", exact=True).click()
                page.get_by_role("menuitem", name="创建演示工具", exact=True).click()
                extensions = editor_dialog.locator(".definition-extensions")
                install_parameters = extensions.locator(".extension-section").filter(has=page.get_by_role("heading", name="安装参数", exact=True))
                install_parameters.get_by_role("button", name="添加参数", exact=True).click()
                install_parameters.get_by_label("参数名称", exact=True).fill("应用名称")
                install_parameters.get_by_label("参数键", exact=True).fill("APP_NAME")
                install_parameters.get_by_label("默认值", exact=True).fill("demo")
                install_parameters.get_by_label("必填", exact=True).check()
                extensions.get_by_role("tab", name="管理操作", exact=True).click()
                extensions.get_by_role("button", name="添加操作", exact=True).click()
                extensions.get_by_label("操作名称", exact=True).fill("查询应用")
                extensions.get_by_label("操作键", exact=True).fill("query-app")
                extensions.get_by_label("结果形式", exact=True).select_option("TABLE")
                extensions.get_by_label("查询操作", exact=True).check()
                extensions.get_by_label("适用运行方式", exact=True).select_option("NATIVE")
                execute = extensions.locator(".extension-section").filter(has=page.get_by_role("heading", name="执行步骤", exact=True))
                execute.get_by_label("步骤名称", exact=True).fill("读取应用信息")
                execute.get_by_label("执行步骤脚本", exact=True).fill("printf '%s\\n' '{\"columns\":[\"名称\"],\"rows\":[{\"名称\":\"demo\"}],\"total\":1}'")
                resources = extensions.locator(".resource-fields")
                resources.get_by_role("button", name="添加资源", exact=True).click()
                resources.get_by_label("资源名称", exact=True).fill("config-read")
                resources.get_by_label("类型", exact=True).select_option("PATH_READ")
                resources.get_by_label("目标值", exact=True).fill("${OPS_CONFIG_ROOT}")
                # A separate mutation explicitly releases a previously declared
                # path. This definition is published, never executed here.
                extensions.get_by_role("button", name="添加操作", exact=True).click()
                extensions.get_by_label("操作名称", exact=True).fill("清理测试文件")
                extensions.get_by_label("操作键", exact=True).fill("remove-file")
                execute.get_by_label("步骤名称", exact=True).fill("移除受管文件")
                execute.get_by_label("执行步骤脚本", exact=True).fill('rm -f -- "$OPS_CONFIG_ROOT/message.txt"')
                verify = extensions.locator(".extension-section").filter(has=page.get_by_role("heading", name="结果核验", exact=True))
                verify.get_by_role("button", name="添加步骤", exact=True).click()
                verify.get_by_label("步骤名称", exact=True).fill("确认文件已移除")
                verify.get_by_label("结果核验脚本", exact=True).fill('test ! -e "$OPS_CONFIG_ROOT/message.txt"')
                resources.get_by_role("button", name="添加资源", exact=True).click()
                resources.get_by_label("资源名称", exact=True).fill("message")
                resources.get_by_label("目标值", exact=True).fill("${OPS_CONFIG_ROOT}/message.txt")
                resources.get_by_label("核验后释放该资源", exact=True).check()
                extensions.get_by_role("tab", name="资源占用", exact=True).click()
                resources.get_by_role("button", name="添加资源", exact=True).click()
                resources.get_by_label("资源名称", exact=True).fill("http")
                resources.get_by_label("类型", exact=True).select_option("PORT")
                resources.get_by_label("目标值", exact=True).fill("18080")
                resources.get_by_label("协议", exact=True).select_option("TCP")
                resources.get_by_label("监听地址", exact=True).fill("127.0.0.1")
                for width, height in ((1440, 900), (390, 600)):
                    page.set_viewport_size({"width": width, "height": height})
                    resources.scroll_into_view_if_needed()
                    assert editor_dialog.evaluate("el => el.scrollWidth <= el.clientWidth+1")
                    expect(editor_dialog.get_by_role("button", name="保存草稿", exact=True)).to_be_in_viewport()
                    page.screenshot(path=str(output / f"management-template-{width}.png"), animations="disabled")
                with page.expect_response(lambda response: response.request.method == "POST" and response.url.endswith("/command-templates")):
                    editor_dialog.get_by_role("button", name="保存草稿", exact=True).click()
                created = next(item for item in get("/command-templates") if item["draft"]["key"] == "custom-command-demo")
                assert created["draft"]["parameters"][0]["key"] == "APP_NAME"
                assert created["draft"]["management"]["resources"][0]["protocol"] == "TCP"
                assert created["draft"]["management"]["operations"][0]["runtimeModes"] == ["NATIVE"]
                assert created["draft"]["management"]["operations"][1]["resources"][0]["release"] is True
                editor_dialog.get_by_role("button", name="预览发布", exact=True).click()
                with page.expect_response(lambda response: response.request.method == "POST" and response.url.endswith("/publish")):
                    editor_dialog.get_by_role("button", name="确认发布", exact=True).click()
                published_custom = next(item for item in get("/command-templates") if item["id"] == created["id"])
                assert published_custom["published"]["management"] == created["draft"]["management"]
                assert published_custom["published"]["parameters"] == created["draft"]["parameters"]
                checks.append("custom-parameters-runtime-operations-and-resource-release-edit-publish-readback")
                assert not get("/hosts"), "editor must not create hosts"
                assert not get("/jobs")["items"], "editor must not submit jobs"
                result["browserUIFixtures"] = ["custom management form, operation plan and job responses; no target execution"]
                check_management_ui(page, output, published_custom["published"], checks)
                assert not errors and not blocked, (errors, blocked)
                result["status"] = "PASS"
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
        if process and process.poll() is None:
            subprocess.run([binary, "stop", "--state", str(state)], capture_output=True, timeout=20, creationflags=flags)
            process.wait(timeout=15)
        (output / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf8")
        print(json.dumps({"status": result["status"], "checks": len(checks), "remoteInstalls": 0}))


if __name__ == "__main__":
    main()
