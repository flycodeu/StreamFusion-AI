"""Configuration UI responses for an isolated controller; no target execution."""
import copy
import json
import time
from urllib.parse import urlsplit
from playwright.sync_api import Error as PlaywrightError, expect


def check_configuration_ui(page, output, published, checks):
    host_id = "11111111-1111-4111-8111-111111111120"
    software_id = "22222222-2222-4222-8222-222222222220"
    stamp = "2026-11-10T00:00:00Z"
    definition = copy.deepcopy(published)
    fields = [
        {"key": "BIND_ADDRESS", "label": "监听地址", "type": "TEXT", "required": True, "default": "127.0.0.1"},
        {"key": "WEB_PORT", "label": "访问端口", "type": "PORT", "required": True, "default": "8080"},
        {"key": "MAX_UPLOAD_MIB", "label": "上传上限（MiB）", "type": "INTEGER", "required": True, "min": 1, "default": "100"},
        {"key": "SOURCE_DIRECTORY", "label": "素材目录", "type": "PATH"},
        {"key": "MEMORY_MIB", "label": "内存上限（MiB）", "type": "INTEGER", "required": True, "default": "512"},
    ]
    step = {"name": "应用配置", "script": "printf '%s\\n' '仅浏览器夹具，不执行目标命令'", "runAs": "ROOT", "timeoutSeconds": 60}
    operations = []
    for key, name, readonly in [("read-config", "读取配置", True), ("configure", "保存配置", False), ("check-data-migration", "检查数据迁移", True), ("migrate-data", "迁移数据", False)]:
        operations.append({"key": key, "name": name, "readOnly": readonly, "rebind": not readonly, "runtimeModes": ["NATIVE"], "steps": [step], "resultFormat": "TABLE" if readonly else "TEXT", "parameters": [{"key": "DATA_DIRECTORY", "label": "目标数据目录", "type": "PATH", "required": True}] if "migration" in key or key == "migrate-data" else []})
    definition["management"] = {"parameters": fields, "operations": operations}
    host = {"id": host_id, "name": "配置测试服务器", "kind": "SSH", "address": "192.0.2.20", "sshUser": "ops", "version": "1", "maintenance": False, "capabilities": {"expiresAt": "2099-01-01T00:00:00Z"}}
    instance = {"id": software_id, "hostId": host_id, "hostName": host["name"], "name": "配置测试标注平台", "softwareType": "OTHER", "softwareVersion": "4.0-python312", "runtimeKind": "SYSTEMD", "locator": "ops-config-fixture.service", "managementMode": "MANAGED", "configurationAvailable": True, "desiredState": "RUNNING", "actualState": "RUNNING", "freshness": "FRESH", "version": "1", "lastJobId": "", "installationStatus": "READY", "dataOwnership": "OPS", "createdAt": stamp, "updatedAt": stamp, "installation": {"definition": definition, "config": {"runtimeMode": "NATIVE", "hostPort": 9924, "bindAddress": "0.0.0.0", "memoryMiB": 1024, "parameters": {}}}, "observation": {"state": "RUNNING", "identityHash": "a" * 64, "version": "4.0-python312", "observedAt": stamp}}
    values = {"BIND_ADDRESS": "0.0.0.0", "WEB_PORT": "9924", "MAX_UPLOAD_MIB": "1024", "SOURCE_DIRECTORY": "/data/images", "MEMORY_MIB": "1024"}
    data_directory = "/srv/ops/annotations/data"
    context = {"definition": definition, "softwareVersion": "1", "templateId": "configuration-fixture", "templateRevision": 1, "stateVersion": "0", "parameters": {}, "credentialRefs": {}, "lastJobId": ""}
    plans, jobs, submitted, violations = {}, {}, [], []
    hold_initial, held_reads = [False], []

    def api(route):
        nonlocal data_directory
        request = route.request
        path, method = urlsplit(request.url).path.removeprefix("/ops/api/v1"), request.method
        if method == "GET" and path == "/hosts": result = [host]
        elif method == "GET" and path == "/software-instances": result = [instance]
        elif method == "GET" and path == f"/software-instances/{software_id}":
            if hold_initial[0]:
                hold_initial[0] = False
                held_reads.append(route)
                return
            result = instance
        elif method == "GET" and path == f"/software-instances/{software_id}/management": result = context
        elif method == "POST" and path == f"/software-instances/{software_id}/operation-plans":
            payload = request.post_data_json
            if payload["expectedVersion"] != context["softwareVersion"]:
                violations.append({"expected": context["softwareVersion"], "received": payload["expectedVersion"]})
                route.fulfill(status=409, json={"code": "MANAGEMENT_STATE_CHANGED", "msg": "软件状态已变化，请重新读取"})
                return
            operation = next(item for item in operations if item["key"] == payload["operationKey"])
            plan_id = f"configuration-plan-{len(plans) + 1}"
            result = {"id": plan_id, "operation": operation, "checks": [{"key": "IMPACT", "status": "PASS", "message": "保存后重启服务，访问会短暂中断。"}] if not operation["readOnly"] else [], "resources": [], "expiresAt": "2099-01-01T00:00:00Z"}
            plans[plan_id] = {"payload": payload, "result": result}
        elif method == "POST" and path.startswith("/software-operation-plans/") and path.endswith("/submit"):
            plan = plans[path.split("/")[2]]
            payload, operation = plan["payload"], plan["result"]["operation"]
            submitted.append(operation["key"])
            if operation["key"] == "configure": values.update(payload["parameters"])
            if operation["key"] == "migrate-data": data_directory = payload["operationParameters"]["DATA_DIRECTORY"]
            table = {"columns": ["key", "value"], "rows": [{"key": key, "value": value} for key, value in {**values, "DATA_DIRECTORY": data_directory}.items()]}
            if operation["key"] == "check-data-migration":
                table["rows"] = [{"key": key, "value": value} for key, value in {"CURRENT_DATA_DIRECTORY": data_directory, "DATA_DIRECTORY": payload["operationParameters"]["DATA_DIRECTORY"], "FILE_COUNT": "3", "DATA_BYTES": "1024", "AVAILABLE_BYTES": "1000000000", "CHECK": "可迁移，原目录保留"}.items()]
            job_id = f"33333333-3333-4333-8333-{len(jobs) + 1:012d}"
            jobs[job_id] = {"id": job_id, "kind": "SOFTWARE_OPERATION", "status": "SUCCEEDED", "operationKey": operation["key"], "phase": "OPERATION_COMPLETED", "targetAckPending": False, "updatedAt": stamp, "result": {"management": {"table": table} if operation["readOnly"] else {"output": "已保存配置"}}, "ackUntil": time.monotonic() + .8}
            context.update(softwareVersion=str(int(context["softwareVersion"]) + 2), stateVersion=str(len(jobs)), parameters=copy.deepcopy(values), lastJobId=job_id)
            instance.update(version=context["softwareVersion"], lastJobId=job_id)
            result = {"jobId": job_id, "softwareInstanceId": software_id}
        elif method == "GET" and path.startswith("/jobs/") and path.split("/")[2] in jobs:
            job = jobs[path.split("/")[2]]
            result = {**job, "targetAckPending": job["operationKey"] == "configure" and time.monotonic() < job["ackUntil"]}
        else:
            route.fallback()
            return
        route.fulfill(json={"code": "OK", "data": result})

    page.route("**/ops/api/v1/**", api)
    page.set_viewport_size({"width": 1440, "height": 900})
    page.get_by_role("button", name="软件管理", exact=True).click()
    workspace = page.get_by_label("已安装软件", exact=True)
    workspace.get_by_label("搜索软件或服务标识", exact=True).fill("")
    workspace.get_by_label("筛选软件主机", exact=True).select_option("")
    workspace.get_by_label("筛选管理模式", exact=True).select_option("ACTIVE")
    workspace.get_by_role("button", name="刷新", exact=True).click()
    expect(page.locator(".el-message").filter(has_text="已登记为只读软件")).to_have_count(0)
    card = page.locator("article.software-card").filter(has=page.get_by_role("heading", name=instance["name"], exact=True))
    card.get_by_role("button", name="编辑配置", exact=True).click()
    dialog = page.get_by_role("dialog", name="编辑配置 · " + instance["name"], exact=True)
    port = dialog.get_by_label("访问端口", exact=True)
    expect(port).to_have_value("9924")
    expect(dialog.get_by_label("上传上限（MiB）", exact=True)).to_have_value("1024")
    expect(dialog.get_by_text(data_directory, exact=True)).to_be_visible()
    assert submitted == ["read-config"]
    port.fill("19924")
    dialog.get_by_label("上传上限（MiB）", exact=True).fill("2048")
    dialog.get_by_role("button", name="检查变更", exact=True).click()
    expect(dialog.get_by_label("配置变更预览", exact=True)).to_contain_text("19924")
    expect(dialog.get_by_label("配置变更预览", exact=True)).to_contain_text("2048")
    expect(dialog.get_by_text("保存后重启服务，访问会短暂中断。", exact=True)).to_be_visible()
    assert submitted == ["read-config"], "a preview must not submit configuration"
    for width, height in ((1440, 900), (390, 700)):
        page.set_viewport_size({"width": width, "height": height})
        dialog.get_by_role("button", name="保存配置", exact=True).scroll_into_view_if_needed()
        assert dialog.evaluate("el => el.scrollWidth <= el.clientWidth + 1")
        assert dialog.locator(".configuration-diff").evaluate("el => el.scrollWidth <= el.clientWidth + 1")
        page.screenshot(path=str(output / f"software-configuration-{width}.png"), animations="disabled")
    page.set_viewport_size({"width": 1440, "height": 900})
    dialog.get_by_role("button", name="保存配置", exact=True).click()
    expect(dialog.get_by_text("执行已完成，正在确认结果…", exact=True)).to_be_visible()
    expect(dialog.get_by_role("button", name="继续编辑", exact=True)).to_be_enabled(timeout=10000)
    assert submitted.count("configure") == 1
    dialog.get_by_role("button", name="继续编辑", exact=True).click()
    expect(port).to_have_value("19924")
    expect(dialog.get_by_label("上传上限（MiB）", exact=True)).to_have_value("2048")
    checks.append("configuration-reads-actual-values-previews-diff-and-restart-impact-on-wide-and-narrow-dialogs")
    checks.append("configuration-acknowledgement-unlocks-and-continue-editing-rereads-with-current-software-version")

    dialog.get_by_role("button", name="迁移数据", exact=True).click()
    migration = page.get_by_role("dialog", name="迁移数据 · " + instance["name"], exact=True)
    destination = migration.get_by_label("目标数据目录", exact=True)
    expect(destination).to_be_enabled()
    destination.fill("/srv/ops/annotations/moved")
    migration.get_by_role("button", name="检查迁移", exact=True).click()
    expect(migration.get_by_role("button", name="预览迁移", exact=True)).to_be_visible()
    assert "migrate-data" not in submitted
    destination.fill("/srv/ops/annotations/moved-again")
    expect(migration.get_by_role("button", name="检查迁移", exact=True)).to_be_visible()
    expect(migration.get_by_role("button", name="预览迁移", exact=True)).to_have_count(0)
    migration.get_by_role("button", name="检查迁移", exact=True).click()
    migration.get_by_role("button", name="预览迁移", exact=True).click()
    expect(migration.get_by_role("button", name="确认迁移", exact=True)).to_be_visible()
    assert "migrate-data" not in submitted
    page.screenshot(path=str(output / "software-data-migration-confirm.png"), animations="disabled")
    migration.get_by_role("button", name="确认迁移", exact=True).click()
    expect(migration.get_by_text("已完成", exact=True)).to_be_visible()
    assert submitted.count("migrate-data") == 1 and data_directory == "/srv/ops/annotations/moved-again"
    checks.append("migration-requires-fresh-directory-check-preview-and-explicit-confirmation")
    page.mouse.click(3, 3)
    expect(migration).not_to_be_visible()
    card.get_by_role("button", name="编辑配置", exact=True).click()
    expect(dialog.get_by_label("访问端口", exact=True)).to_have_value("19924")
    page.mouse.click(3, 3)
    expect(dialog).not_to_be_visible()
    assert not violations, violations
    checks.append("configuration-mode-switch-and-reopen-use-current-version-and-blank-area-closes-one-dialog")
    hold_initial[0] = True
    card.get_by_role("button", name="编辑配置", exact=True).click()
    expect(dialog.get_by_text("正在读取软件配置…", exact=True)).to_be_visible()
    expect(dialog.get_by_role("button", name="Close this dialog", exact=True)).to_be_visible()
    page.mouse.click(3, 3)
    expect(dialog).not_to_be_visible()
    for route in held_reads:
        try:
            route.fulfill(json={"code": "OK", "data": instance})
        except PlaywrightError:
            pass  # Closing aborts this read in the browser.
    expect(dialog).not_to_be_visible()
    checks.append("slow-initial-configuration-read-can-close-and-late-response-cannot-reopen-or-fill-the-dialog")
    (output / "software-configuration-requests.json").write_text(json.dumps({"submittedOperations": submitted, "versionViolations": violations}, ensure_ascii=False, indent=2), encoding="utf8")
    page.unroute("**/ops/api/v1/**", api)


def check_stock_service_configuration_ui(page, output, definition, checks):
    """Exercise the shared editor with the actual published service fields."""
    host_id = "11111111-1111-4111-8111-111111111121"
    software_id = "22222222-2222-4222-8222-222222222221"
    fields = definition["management"]["parameters"]
    values = {field["key"]: field.get("default", "") for field in fields if field["type"] != "CREDENTIAL"}
    port = next((field for field in fields if field["key"] in ("CONFIG_PORT", "WEB_PORT")), None)
    if port:
        values[port["key"]] = "18080"
    if "GRPC_PORT" in values:
        values.update(GRPC_PORT="19080", CLUSTER_GRPC_PORT="19081")
    if "EXTERNAL_URL" in values:
        values["EXTERNAL_URL"] = "http://git.company.invalid:18080"
    if "MEMORY_MIB" in values:
        values["MEMORY_MIB"] = "0"
    name = definition["name"] + " 配置测试"
    instance = dict(id=software_id, hostId=host_id, name=name, softwareType="OTHER", runtimeKind="SYSTEMD", managementMode="MANAGED", configurationAvailable=True, installationStatus="READY", version="1", desiredState="RUNNING", actualState="RUNNING", freshness="FRESH", lastJobId="", installation={"definition": definition, "config": {"runtimeMode":"NATIVE", "parameters":{}}})
    host = dict(id=host_id, name="配置测试服务器", address="192.0.2.21", version="1", maintenance=False)
    context = dict(definition=definition, softwareVersion="1", templateId="stock-configuration", templateRevision=1, stateVersion="0", parameters={}, credentialRefs={})
    plans, jobs, writes = {}, {}, []

    def api(route):
        path = urlsplit(route.request.url).path.removeprefix("/ops/api/v1")
        method = route.request.method
        if method == "GET" and path == "/hosts": result = [host]
        elif method == "GET" and path == "/software-instances": result = [instance]
        elif method == "GET" and path == f"/software-instances/{software_id}": result = instance
        elif method == "GET" and path == f"/software-instances/{software_id}/management": result = context
        elif method == "POST" and path == f"/software-instances/{software_id}/operation-plans":
            payload = route.request.post_data_json
            assert payload["expectedVersion"] == context["softwareVersion"]
            parameters = payload["parameters"]
            if "GRPC_PORT" in parameters:
                assert int(parameters["GRPC_PORT"]) == int(parameters["WEB_PORT"])+1000
                assert int(parameters["CLUSTER_GRPC_PORT"]) == int(parameters["WEB_PORT"])+1001
            if "EXTERNAL_URL" in parameters:
                assert (urlsplit(parameters["EXTERNAL_URL"]).port or 80) == int(parameters["WEB_PORT"])
            operation = next(item for item in definition["management"]["operations"] if item["key"] == payload["operationKey"])
            identifier = str(len(plans) + 1)
            result = dict(id=identifier, operation=operation, checks=[], resources=[], expiresAt="2099-01-01T00:00:00Z")
            plans[identifier] = (payload, result)
        elif method == "POST" and path.startswith("/software-operation-plans/") and path.endswith("/submit"):
            payload, plan = plans[path.split("/")[2]]
            operation = plan["operation"]
            if not operation.get("readOnly", False):
                writes.append(payload)
                values.update(payload["parameters"])
            identifier = "33333333-3333-4333-8333-" + f"{len(jobs)+1:012d}"
            table = dict(columns=["key", "value"], rows=[dict(key=key, value=value) for key,value in values.items()])
            jobs[identifier] = dict(id=identifier, kind="SOFTWARE_OPERATION", operationKey=operation["key"], status="SUCCEEDED", targetAckPending=False, phase="OPERATION_COMPLETED", result={"management":{"table":table} if operation.get("readOnly", False) else {"output":"配置已生效"}})
            context.update(softwareVersion=str(int(context["softwareVersion"])+2), stateVersion=str(len(jobs)), parameters=copy.deepcopy(values), lastJobId=identifier)
            instance["version"] = context["softwareVersion"]
            result = dict(jobId=identifier, softwareInstanceId=software_id)
        elif method == "GET" and path.startswith("/jobs/") and path.split("/")[2] in jobs: result = jobs[path.split("/")[2]]
        else:
            route.fallback()
            return
        route.fulfill(json={"code":"OK", "data":result})

    page.route("**/ops/api/v1/**", api)
    page.set_viewport_size({"width":1440, "height":900})
    workspace = page.get_by_label("已安装软件", exact=True)
    workspace.get_by_role("button", name="刷新", exact=True).click()
    card = page.locator("article.software-card").filter(has=page.get_by_role("heading", name=name, exact=True))
    card.get_by_role("button", name="编辑配置", exact=True).click()
    dialog = page.get_by_role("dialog", name="编辑配置 · " + name, exact=True)
    field = port or next(field for field in fields if field["key"] == "MEMORY_MIB")
    control = dialog.get_by_label(field["label"], exact=True)
    expect(control).to_have_value(values[field["key"]])
    control.fill("18081" if port else "512")
    for key, value in {"GRPC_PORT":"19081", "CLUSTER_GRPC_PORT":"19082", "EXTERNAL_URL":"http://git.company.invalid:18081"}.items():
        related = next((item for item in fields if item["key"] == key), None)
        if related:
            dialog.get_by_label(related["label"], exact=True).fill(value)
    dialog.get_by_role("button", name="检查变更", exact=True).click()
    expect(dialog.get_by_label("配置变更预览", exact=True)).to_contain_text(field["label"])
    assert not writes, "preview must not apply configuration"
    page.set_viewport_size({"width":390,"height":700})
    assert dialog.evaluate("el => el.scrollWidth <= el.clientWidth + 1")
    assert dialog.locator(".configuration-diff").evaluate("el => el.scrollWidth <= el.clientWidth + 1")
    dialog.get_by_role("button", name="保存配置", exact=True).scroll_into_view_if_needed()
    page.screenshot(path=str(output / (definition["key"] + "-configuration.png")), animations="disabled")
    dialog.get_by_role("button", name="保存配置", exact=True).click()
    expect(dialog.get_by_role("button", name="继续编辑", exact=True)).to_be_enabled(timeout=10000)
    assert len(writes) == 1
    dialog.get_by_role("button", name="继续编辑", exact=True).click()
    expect(control).to_have_value("18081" if port else "512")
    page.mouse.click(2,2)
    expect(dialog).not_to_be_visible()
    checks.append("stock-config-editor-" + definition["key"])
    page.unroute("**/ops/api/v1/**", api)
