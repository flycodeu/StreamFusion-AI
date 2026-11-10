"""Browser-only operation responses; never contacts or mutates a target host."""
import copy
from urllib.parse import urlsplit
from playwright.sync_api import expect


def check_management_ui(page, output, published, checks):
    host_id = "11111111-1111-4111-8111-111111111114"
    software_id = "22222222-2222-4222-8222-222222222214"
    stamp = "2026-11-09T12:00:00Z"
    definition = copy.deepcopy(published)
    definition["management"]["parameters"] = [{"key": "ADMIN", "label": "管理凭据", "type": "CREDENTIAL", "required": True}]
    definition["management"]["operations"][0]["parameters"] = [{"key": "FILTER", "label": "名称筛选", "type": "TEXT"}]
    definition["management"]["operations"][1]["parameters"] = [{"key": "NAME", "label": "项目名称", "type": "TEXT", "required": True}]
    host = {"id": host_id, "name": "界面测试主机", "kind": "SSH", "address": "192.0.2.1", "sshUser": "ops", "version": "1", "maintenance": False, "installRoot": "/opt/ops", "capabilities": {"expiresAt": "2099-01-01T00:00:00Z"}}
    config = {"name": "自定义应用", "runtimeMode": "NATIVE", "memoryMiB": 512, "ports": {}, "parameters": {"APP_NAME": "demo"}}
    instance = {"id": software_id, "hostId": host_id, "hostName": host["name"], "name": config["name"], "softwareType": "OTHER", "softwareVersion": "1.0.0", "runtimeKind": "TOOL", "locator": "/opt/ops/bin/demo", "managementMode": "MANAGED", "desiredState": "INSTALLED", "actualState": "INSTALLED", "freshness": "FRESH", "version": "1", "lastJobId": "", "installationStatus": "READY", "installationJobId": "33333333-3333-4333-8333-333333333314", "dataOwnership": "OPS", "createdAt": stamp, "updatedAt": stamp, "installation": {"definition": definition, "config": config}, "observation": {"state": "INSTALLED", "identityHash": "a" * 64, "version": "1.0.0", "observedAt": stamp, "errorCode": "", "message": "READY"}}
    context = {"definition": definition, "templateId": "template-ui", "templateRevision": 1, "stateVersion": "0", "parameters": {}, "credentialRefs": {}, "lastJobId": ""}
    plans, jobs, requests = {}, {}, []

    def route_fixture(route):
        request = route.request
        path = urlsplit(request.url).path.removeprefix("/ops/api/v1")
        data = None
        if request.method == "GET" and path == "/hosts": data = [host]
        elif request.method == "GET" and path == "/software-instances": data = [instance]
        elif request.method == "GET" and path == f"/software-instances/{software_id}": data = instance
        elif request.method == "GET" and path == f"/software-instances/{software_id}/management": data = context
        elif request.method == "GET" and path == "/credentials": data = [{"id": "credential-ui", "revision": "1", "name": "测试管理凭据", "kind": "PASSWORD"}]
        elif request.method == "POST" and path == f"/software-instances/{software_id}/operation-plans":
            payload = request.post_data_json
            requests.append((path, payload))
            operation = next(item for item in definition["management"]["operations"] if item["key"] == payload["operationKey"])
            plan_id = f"plan-{len(plans)+1}"
            data = {"id": plan_id, "operation": operation, "checks": [], "resources": [], "expiresAt": "2099-01-01T00:00:00Z"}
            plans[plan_id] = {"data": data, "payload": payload}
        elif request.method == "POST" and path.startswith("/software-operation-plans/") and path.endswith("/submit"):
            requests.append((path, request.post_data_json))
            plan = plans[path.split("/")[2]]
            job_id = f"44444444-4444-4444-8444-{len(jobs)+1:012d}"
            readonly = plan["data"]["operation"]["readOnly"] if "readOnly" in plan["data"]["operation"] else False
            result = {"table": {"columns": ["名称", "状态"], "rows": [{"名称": "demo", "状态": "正常"}], "total": 3}} if readonly else {"output": "测试操作已完成"}
            jobs[job_id] = {"id": job_id, "kind": "SOFTWARE_OPERATION", "status": "SUCCEEDED" if readonly else "RUNNING", "operationKey": plan["payload"]["operationKey"], "phase": "SOFTWARE_OPERATION_COMPLETED" if readonly else "MANAGEMENT_EXECUTE", "targetAckPending": False, "errorCode": "", "updatedAt": stamp, "result": {"management": result}}
            context.update({"credentialRefs": plan["payload"]["credentialRefs"], "stateVersion": str(len(jobs)), "lastJobId": job_id})
            instance["lastJobId"] = job_id
            data = {"jobId": job_id, "softwareInstanceId": software_id}
        elif request.method == "GET" and path.startswith("/jobs/") and path.split("/")[2] in jobs: data = jobs[path.split("/")[2]]
        else:
            route.fallback()
            return
        route.fulfill(json={"code": "OK", "data": data})

    page.route("**/ops/api/v1/**", route_fixture)
    page.set_viewport_size({"width": 1440, "height": 900})
    page.mouse.click(4, 4)
    page.get_by_role("button", name="软件管理", exact=True).click()
    card = page.locator("article.software-card").filter(has=page.get_by_role("heading", name="自定义应用", exact=True))
    card.get_by_role("button", name="详情", exact=True).click()
    dialog = page.get_by_role("dialog", name="自定义应用", exact=True)
    dialog.get_by_role("tab", name="管理操作", exact=True).click()
    panel = dialog.get_by_label("软件管理操作", exact=True)
    panel.get_by_label("管理凭据", exact=True).select_option("credential-ui:1")
    panel.get_by_label("名称筛选", exact=True).fill("demo")
    panel.get_by_role("button", name="查询", exact=True).click()
    expect(panel.locator(".operation-table")).to_contain_text("共 3 项，显示前 1 项")
    assert requests[0][1]["credentialRefs"] == {"ADMIN": {"id": "credential-ui", "revision": "1"}}
    assert requests[0][1]["operationParameters"] == {"FILTER": "demo"}
    expect(panel.get_by_text("SOFTWARE_OPERATION_COMPLETED", exact=True)).to_have_count(0)
    checks.append("custom-query-form-uses-credential-references-and-renders-chinese-table-total")
    panel.get_by_role("button", name="清理测试文件", exact=True).click()
    # The query filter is unsaved operation input and switching asks once.
    if panel.get_by_role("button", name="切换", exact=True).count(): panel.get_by_role("button", name="切换", exact=True).click()
    panel.get_by_label("项目名称", exact=True).fill("项目A")
    panel.get_by_role("button", name="检查并预览", exact=True).click()
    expect(panel.get_by_role("button", name="确认执行", exact=True)).to_be_visible()
    assert len(jobs) == 1, "preview must not submit a mutation"
    panel.locator(".operation-plan summary").click()
    expect(panel.locator(".operation-plan pre").first).to_contain_text("rm -f")
    for width, height in ((1440, 900), (390, 650)):
        page.set_viewport_size({"width": width, "height": height})
        panel.get_by_role("button", name="确认执行", exact=True).scroll_into_view_if_needed()
        assert dialog.evaluate("el => el.scrollWidth <= el.clientWidth+1")
        page.screenshot(path=str(output / f"custom-management-{width}.png"), animations="disabled")
    panel.get_by_role("button", name="确认执行", exact=True).click()
    expect(panel.get_by_text("执行中", exact=True)).to_be_visible()
    checks.append("custom-mutation-previews-real-scripts-and-requires-one-confirmation-on-wide-and-narrow-layouts")
    page.set_viewport_size({"width": 1440, "height": 900})
    page.mouse.click(3, 3)
    expect(dialog).not_to_be_visible()
    card.get_by_role("button", name="详情", exact=True).click()
    dialog.get_by_role("tab", name="管理操作", exact=True).click()
    expect(panel.get_by_role("heading", name="清理测试文件", exact=True)).to_be_visible()
    expect(panel.get_by_text("执行中", exact=True)).to_be_visible()
    assert len(jobs) == 2, "reopening must only read the original job"
    original = context["lastJobId"]
    jobs[original].update({"status": "SUCCEEDED", "phase": "SOFTWARE_OPERATION_COMPLETED"})
    expect(panel.get_by_text("已完成", exact=True)).to_be_visible(timeout=10000)
    expect(panel.locator(".operation-result pre")).to_have_text("测试操作已完成")
    checks.append("closing-and-reopening-resumes-the-original-operation-without-replay")
