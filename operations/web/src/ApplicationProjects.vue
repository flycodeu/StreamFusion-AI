<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from "vue";
import { ElDialog, ElMessageBox } from "element-plus";
import { ApiError, request } from "./api";
import { requestAllPages } from "./listPages";
import OpsPagination from "./OpsPagination.vue";
import { hostTime, type Host } from "./hosts";
import type { GitProfile } from "./gitManagement";
import { softwareInstallationReady, softwareIsDockerEngine, softwareIsTool, softwareVersionText, type SoftwareInstance } from "./software";
import {
  copyApplicationProject, latestProjectCredentials, newApplicationProject, newProjectDeployment, newProjectEnvironment,
  newProjectModule, newProjectRepository, projectListPath, projectModuleUsage, projectNameError,
  projectRepositoryUsage, projectVariableMode,
  type ApplicationProject, type ApplicationProjectPage, type ProjectCredential, type ProjectDeployment, type ProjectVariable,
} from "./applicationProjects";

const emit = defineEmits<{ error: [unknown] }>();
const records = ref<ApplicationProject[]>([]);
const page = ref(1), pageSize = ref(10), total = ref(0);
const query = ref(""), archived = ref("false");
const loading = ref(false), saving = ref(false), opening = ref(false);
const listError = ref(""), editorError = ref("");
const editorOpen = ref(false), tab = ref("repositories");
const draft = ref(newApplicationProject()), baseline = ref("");
const selectedModuleId = ref(""), selectedEnvironmentId = ref(""), selectedDeploymentId = ref("");
const archiveTarget = ref("");
const hosts = ref<Host[]>([]), accounts = ref<GitProfile[]>([]), software = ref<SoftwareInstance[]>([]);
const credentials = ref<ProjectCredential[]>([]);
const credentialVariables = reactive(new WeakSet<ProjectVariable>());
let listSequence = 0, editorSequence = 0;
let listController: AbortController | undefined;
let editorController: AbortController | undefined;
let searchTimer: ReturnType<typeof setTimeout> | undefined;

const module = computed(() => draft.value.modules.find(item => item.id === selectedModuleId.value));
const environment = computed(() => draft.value.environments.find(item => item.id === selectedEnvironmentId.value));
const deployment = computed(() => environment.value?.deployments.find(item => item.id === selectedDeploymentId.value));
const dirty = computed(() => JSON.stringify(draft.value) !== baseline.value);
const availableModules = computed(() => draft.value.modules.filter(item => !environment.value?.deployments.some(target => target.moduleId === item.id)));
const tabs = [{ id: "repositories", label: "代码仓库" }, { id: "modules", label: "应用模块" }, { id: "environments", label: "部署环境" }];
const buildKinds = { DOCKERFILE: "Dockerfile", COMMAND: "编译与打包", STATIC: "前端静态文件" };
const runtimeKinds = { DOCKER: "Docker 容器", NATIVE: "原生服务", STATIC: "静态文件" };
const healthKinds = { NONE: "暂不配置", HTTP: "HTTP 请求", TCP: "TCP 端口", COMMAND: "验证命令" };
const logKinds = { FILE: "文件日志", DOCKER: "Docker 控制台", JOURNAL: "系统日志" };
const moduleName = (id: string) => draft.value.modules.find(item => item.id === id)?.name || "未命名模块";
const accountLabel = (account: GitProfile) => `${account.name} · ${account.systemAccount || "未配置"}@${account.sshHost}`;
const softwareChoices = (hostId: string) => hostId ? software.value.filter(item =>
  item.hostId === hostId && item.name.trim() && item.managementMode !== "RELEASED" &&
  item.desiredState !== "REMOVED" && item.actualState !== "REMOVED" && softwareInstallationReady(item) &&
  (softwareIsTool(item) || softwareIsDockerEngine(item)),
) : [];

function errorMessage(error: unknown) {
  if (error instanceof ApiError && error.code === "AUTH_REQUIRED") emit("error", error);
  return error instanceof Error && /[\u4e00-\u9fff]/.test(error.message)
    ? error.message : "读取项目配置失败，请刷新后重试";
}

async function load() {
  listController?.abort();
  const sequence = ++listSequence, controller = new AbortController();
  listController = controller;
  loading.value = true;
  listError.value = "";
  try {
    const data = await request<ApplicationProjectPage>(projectListPath(page.value, pageSize.value, query.value, archived.value), "GET", undefined, controller.signal);
    if (sequence !== listSequence) return;
    const last = Math.max(1, Math.ceil(data.total / pageSize.value));
    if (page.value > last) { page.value = last; return; }
    records.value = data.items;
    total.value = data.total;
  } catch (error) {
    if (sequence === listSequence && !controller.signal.aborted) listError.value = errorMessage(error);
  } finally {
    if (sequence === listSequence) loading.value = false;
  }
}

async function loadOptions(signal: AbortSignal) {
  const data = await Promise.all([
    request<Host[]>("/hosts", "GET", undefined, signal), request<GitProfile[]>("/git-accounts", "GET", undefined, signal),
    requestAllPages<SoftwareInstance>("/software-instances", signal),
    request<ProjectCredential[]>("/credentials", "GET", undefined, signal),
  ]);
  return { hosts: data[0], accounts: data[1], software: data[2], credentials: latestProjectCredentials(data[3]) };
}

async function editProject(project?: ApplicationProject) {
  if (saving.value) return;
  editorController?.abort();
  const sequence = ++editorSequence, controller = new AbortController();
  editorController = controller;
  opening.value = true;
  listError.value = "";
  try {
    const [saved, options] = await Promise.all([
      project ? request<ApplicationProject>(`/application-projects/${project.id}`, "GET", undefined, controller.signal) : Promise.resolve(newApplicationProject()),
      loadOptions(controller.signal),
    ]);
    if (sequence !== editorSequence) return;
    hosts.value = options.hosts; accounts.value = options.accounts;
    software.value = options.software; credentials.value = options.credentials;
    draft.value = copyApplicationProject(saved);
    baseline.value = JSON.stringify(draft.value);
    selectedModuleId.value = saved.modules[0]?.id || "";
    selectedEnvironmentId.value = saved.environments[0]?.id || "";
    selectedDeploymentId.value = saved.environments[0]?.deployments[0]?.id || "";
    editorError.value = "";
    tab.value = "repositories";
    editorOpen.value = true;
  } catch (error) {
    if (sequence === editorSequence && !controller.signal.aborted) listError.value = errorMessage(error);
  } finally { if (sequence === editorSequence) opening.value = false; }
}

async function closeEditor(done: () => void) {
  if (saving.value) { done(); return; }
  if (dirty.value) {
    try { await ElMessageBox.confirm("放弃尚未保存的项目配置？", "关闭配置", { confirmButtonText: "放弃修改", cancelButtonText: "继续编辑", type: "warning" }); }
    catch { return; }
  }
  done();
}

async function saveProject() {
  editorError.value = projectNameError(draft.value);
  if (!editorError.value && draft.value.environments.some(environment => environment.deployments.some(target => target.variables.some(variable => usesCredential(variable) && !variable.credentialId)))) editorError.value = "请为凭据变量选择连接凭据";
  if (editorError.value || saving.value) return;
  const submitted = copyApplicationProject(draft.value);
  saving.value = true;
  try {
    const saved = await request<ApplicationProject>(submitted.id ? `/application-projects/${submitted.id}` : "/application-projects", submitted.id ? "PATCH" : "POST", submitted);
    draft.value = copyApplicationProject(saved);
    baseline.value = JSON.stringify(draft.value);
    editorOpen.value = false;
    await load();
  } catch (error) { editorError.value = errorMessage(error); }
  finally { saving.value = false; }
}

async function setArchived(project: ApplicationProject, value: boolean) {
  if (loading.value || saving.value) return;
  saving.value = true;
  listError.value = "";
  try {
    if (value) await request(`/application-projects/${project.id}/archive`, "POST", { version: project.version });
    else {
      const saved = await request<ApplicationProject>(`/application-projects/${project.id}`);
      await request(`/application-projects/${project.id}`, "PATCH", { ...saved, archived: false });
    }
    archiveTarget.value = "";
    await load();
  } catch (error) { listError.value = errorMessage(error); }
  finally { saving.value = false; }
}

function addRepository() { draft.value.repositories.push(newProjectRepository()); }
function removeRepository(id: string) {
  const usage = projectRepositoryUsage(draft.value, id);
  if (usage.length) { editorError.value = `请先调整使用此仓库的模块：${usage.join("、")}`; return; }
  draft.value.repositories = draft.value.repositories.filter(item => item.id !== id);
  editorError.value = "";
}
function addModule() {
  const item = newProjectModule(draft.value.repositories.length === 1 ? draft.value.repositories[0].id : "");
  draft.value.modules.push(item); selectedModuleId.value = item.id;
}
function removeModule() {
  if (!module.value) return;
  const usage = projectModuleUsage(draft.value, module.value.id);
  if (usage.length) { editorError.value = `请先移除此模块的环境配置：${usage.join("、")}`; return; }
  draft.value.modules = draft.value.modules.filter(item => item.id !== module.value!.id);
  selectedModuleId.value = draft.value.modules[0]?.id || "";
  editorError.value = "";
}
function addEnvironment() {
  const item = newProjectEnvironment(); draft.value.environments.push(item);
  selectedEnvironmentId.value = item.id; selectedDeploymentId.value = "";
}
function selectEnvironment(id: string) {
  selectedEnvironmentId.value = id;
  selectedDeploymentId.value = environment.value?.deployments[0]?.id || "";
}
function removeEnvironment() {
  draft.value.environments = draft.value.environments.filter(item => item.id !== selectedEnvironmentId.value);
  selectEnvironment(draft.value.environments[0]?.id || "");
}
function addDeployment(moduleId: string) {
  if (!environment.value || !moduleId) return;
  const item = newProjectDeployment(moduleId, draft.value.modules.find(item => item.id === moduleId)?.runtime.kind);
  environment.value.deployments.push(item); selectedDeploymentId.value = item.id;
}
function removeDeployment() {
  if (!environment.value) return;
  environment.value.deployments = environment.value.deployments.filter(item => item.id !== selectedDeploymentId.value);
  selectedDeploymentId.value = environment.value.deployments[0]?.id || "";
}
function setVariableCredential(variable: ProjectVariable, id: string) {
  variable.value = ""; variable.credentialId = id;
  variable.credentialRevision = credentials.value.find(item => item.id === id)?.revision || "";
}
function usesCredential(variable: ProjectVariable) { return !!variable.credentialId || credentialVariables.has(variable); }
function changeVariableMode(variable: ProjectVariable, mode: "VALUE" | "CREDENTIAL") {
  projectVariableMode(variable, mode);
  if (mode === "CREDENTIAL") credentialVariables.add(variable);
  else credentialVariables.delete(variable);
}
function addVariable(target: ProjectDeployment) { target.variables.push({ name: "", value: "", credentialId: "", credentialRevision: "" }); }

watch([page, pageSize, archived], () => { void load(); });
watch(pageSize, () => { page.value = 1; }, { flush: "sync" });
watch(archived, () => { page.value = 1; }, { flush: "sync" });
watch(query, () => {
  if (searchTimer) clearTimeout(searchTimer);
  listController?.abort(); ++listSequence;
  searchTimer = setTimeout(() => { if (page.value === 1) void load(); else page.value = 1; }, 250);
});
onMounted(load);
onUnmounted(() => {
  ++listSequence; ++editorSequence;
  listController?.abort(); editorController?.abort();
  if (searchTimer) clearTimeout(searchTimer);
});
</script>

<template>
  <section class="application-projects" aria-label="项目管理">
    <div class="project-toolbar">
      <div class="project-filters">
        <input v-model="query" type="search" placeholder="搜索项目" aria-label="搜索项目" />
        <select v-model="archived" aria-label="项目状态">
          <option value="false">使用中的项目</option>
          <option value="true">已归档</option>
          <option value="all">全部项目</option>
        </select>
      </div>
      <div class="project-actions">
        <button class="secondary" :disabled="loading || opening" @click="load">刷新</button>
        <button class="primary" :disabled="opening || saving" @click="editProject()">{{ opening ? "正在读取…" : "新建项目" }}</button>
      </div>
    </div>
    <p v-if="listError" class="project-error" role="alert">{{ listError }}</p>
    <div class="project-list" :aria-busy="loading">
      <article v-for="project in records" :key="project.id" class="project-card">
        <div class="project-card-title">
          <span class="project-symbol" aria-hidden="true">{{ project.name.slice(0, 1) }}</span>
          <div>
            <h2>{{ project.name }}</h2>
            <p v-if="project.description">{{ project.description }}</p>
          </div>
          <span class="project-state" :class="{ archived: project.archived }">{{ project.archived ? "已归档" : "使用中" }}</span>
        </div>
        <div class="project-card-summary">
          <span>{{ project.repositories.length }} 个仓库</span>
          <span>{{ project.modules.length }} 个模块</span>
          <span>{{ project.environments.length }} 个环境</span>
        </div>
        <footer>
          <span>{{ hostTime(project.updatedAt) }}</span>
          <div v-if="archiveTarget === project.id" class="project-actions">
            <span>归档此项目？</span>
            <button class="quiet" :disabled="saving" @click="archiveTarget = ''">取消</button>
            <button class="quiet danger" :disabled="saving" @click="setArchived(project, true)">确认归档</button>
          </div>
          <div v-else class="project-actions">
            <button class="quiet" :disabled="opening || saving" @click="editProject(project)">配置</button>
            <button v-if="!project.archived" class="quiet" :disabled="saving" @click="archiveTarget = project.id">归档</button>
            <button v-else class="quiet" :disabled="saving" @click="setArchived(project, false)">恢复使用</button>
          </div>
        </footer>
      </article>
      <div v-if="!records.length" class="project-empty">
        <h2>{{ loading ? "正在读取项目…" : "尚无项目" }}</h2>
        <p v-if="!loading">创建项目，配置代码仓库、应用模块与部署环境。</p>
      </div>
    </div>
    <OpsPagination v-model:page="page" v-model:page-size="pageSize" :total="total" :disabled="loading" label="项目分页" />
    <ElDialog
      v-model="editorOpen"
      class="application-project-dialog"
      :title="draft.id ? `配置项目 · ${draft.name}` : '新建项目'"
      width="min(1180px, 96vw)"
      align-center
      append-to-body
      destroy-on-close
      :before-close="closeEditor"
      :close-on-click-modal="true"
      :close-on-press-escape="true"
    >
      <div class="project-editor">
        <fieldset :disabled="saving">
          <div class="project-fields project-basics">
            <label>
              项目名称
              <input v-model="draft.name" maxlength="60" placeholder="例如：智能化平台" />
            </label>
            <label>
              项目说明
              <input v-model="draft.description" maxlength="500" placeholder="选填" />
            </label>
          </div>
          <nav class="project-tabs" aria-label="项目配置">
            <button
              v-for="item in tabs"
              :key="item.id"
              type="button"
              :class="{ active: tab === item.id }"
              :aria-current="tab === item.id ? 'page' : undefined"
              @click="tab = item.id"
            >
              {{ item.label }}
            </button>
          </nav>
          <div v-if="tab === 'repositories'" class="project-tab-content">
            <div class="project-section-heading">
              <h3>代码仓库</h3>
              <button type="button" class="secondary" @click="addRepository">添加仓库</button>
            </div>
            <div v-if="!draft.repositories.length" class="project-blank">可添加一个或多个仓库，模块分别选择。</div>
            <section v-for="(repository, index) in draft.repositories" :key="repository.id" class="project-config-block">
              <div class="project-section-heading">
                <h4>仓库 {{ index + 1 }}</h4>
                <button type="button" class="quiet danger" @click="removeRepository(repository.id)">移除</button>
              </div>
              <div class="project-fields">
                <label>
                  仓库名称
                  <input v-model="repository.name" maxlength="60" placeholder="例如：业务源码" />
                </label>
                <label>
                  仓库地址
                  <input v-model="repository.url" placeholder="https://git.example.com/team/project.git" />
                </label>
                <label>
                  Git 账号
                  <select v-model="repository.gitAccountId">
                    <option value="">公共仓库，不使用账号</option>
                    <option v-for="account in accounts" :key="account.id" :value="account.id">{{ accountLabel(account) }}</option>
                  </select>
                </label>
                <label>
                  默认分支
                  <input v-model="repository.branch" placeholder="留空采用仓库默认分支" />
                </label>
              </div>
            </section>
          </div>
          <div v-else-if="tab === 'modules'" class="project-tab-content">
            <div class="project-section-heading">
              <h3>应用模块</h3>
              <button type="button" class="secondary" @click="addModule">添加模块</button>
            </div>
            <div v-if="!draft.modules.length" class="project-blank">前端、后端和算法服务可分别配置。</div>
            <div v-else class="project-split">
              <nav class="project-item-nav" aria-label="模块列表">
                <button
                  v-for="item in draft.modules"
                  :key="item.id"
                  type="button"
                  :class="{ active: selectedModuleId === item.id }"
                  @click="selectedModuleId = item.id"
                >
                  {{ item.name || '未命名模块' }}
                </button>
              </nav>
              <div v-if="module" class="project-module-form">
                <div class="project-fields">
                  <label>
                    模块名称
                    <input v-model="module.name" maxlength="60" placeholder="例如：后端服务" />
                  </label>
                  <label>
                    代码仓库
                    <select v-model="module.repositoryId">
                      <option value="">稍后选择</option>
                      <option v-for="repository in draft.repositories" :key="repository.id" :value="repository.id">
                        {{ repository.name || '未命名仓库' }}
                      </option>
                    </select>
                  </label>
                  <label>
                    源码目录
                    <input v-model="module.directory" placeholder="backend 或 ." />
                  </label>
                  <label>
                    构建方式
                    <select v-model="module.build.kind">
                      <option v-for="(label, kind) in buildKinds" :key="kind" :value="kind">{{ label }}</option>
                    </select>
                  </label>
                  <label>
                    构建主机
                    <select v-model="module.build.hostId" @change="module.build.softwareId = ''">
                      <option value="">稍后选择</option>
                      <option v-for="host in hosts" :key="host.id" :value="host.id">{{ host.name }}</option>
                    </select>
                  </label>
                  <label>
                    执行账号
                    <input v-model="module.build.account" placeholder="例如：ubuntu" />
                  </label>
                  <label>
                    构建环境
                    <select v-model="module.build.softwareId" :disabled="!module.build.hostId">
                      <option value="">自定义或稍后选择</option>
                      <option v-for="item in softwareChoices(module.build.hostId)" :key="item.id" :value="item.id">
                        {{ item.name }}
                        ·
                        {{ softwareVersionText(item.softwareVersion) }}
                      </option>
                    </select>
                  </label>
                  <label>
                    工具链说明
                    <input v-model="module.build.toolchain" placeholder="例如：JDK 8 + Maven" />
                  </label>
                  <label class="project-full">
                    产物路径
                    <input v-model="module.build.outputPath" placeholder="源码目录内的固定路径，例如：target/app.jar 或 dist；Docker 可留空" />
                  </label>
                </div>
                <div v-if="module.build.kind === 'DOCKERFILE'" class="project-fields project-form-section">
                  <label>
                    构建上下文
                    <input v-model="module.build.context" placeholder="仓库内目录，例如 ." />
                  </label>
                  <label>
                    Dockerfile 路径
                    <input v-model="module.build.dockerfile" placeholder="例如：deploy/Dockerfile" />
                  </label>
                </div>
                <div class="project-section-heading project-form-section">
                  <h4>构建步骤</h4>
                  <button
                    type="button"
                    class="quiet"
                    @click="module.build.steps.push({ name: '', command: '', workdir: '.', timeoutSeconds: 600, privilege: 'ACCOUNT' })"
                  >
                    添加步骤
                  </button>
                </div>
                <section v-for="(step, index) in module.build.steps" :key="index" class="project-step">
                  <div class="project-step-title">
                    <span>{{ index + 1 }}</span>
                    <input v-model="step.name" :aria-label="`步骤${index + 1}名称`" placeholder="例如：编译并检查产物" />
                    <button
                      type="button"
                      class="quiet"
                      :disabled="index === 0"
                      aria-label="上移步骤"
                      @click="module.build.steps.splice(index - 1, 0, module.build.steps.splice(index, 1)[0])"
                    >
                      ↑
                    </button>
                    <button
                      type="button"
                      class="quiet"
                      :disabled="index === module.build.steps.length - 1"
                      aria-label="下移步骤"
                      @click="module.build.steps.splice(index + 1, 0, module.build.steps.splice(index, 1)[0])"
                    >
                      ↓
                    </button>
                    <button type="button" class="quiet danger" @click="module.build.steps.splice(index, 1)">移除</button>
                  </div>
                  <label class="project-command">
                    执行命令
                    <textarea v-model="step.command" rows="4" spellcheck="false" placeholder="mvn clean package" />
                  </label>
                  <div class="project-fields project-three">
                    <label>
                      工作目录
                      <input v-model="step.workdir" placeholder="." />
                    </label>
                    <label>
                      超时（秒）
                      <input v-model.number="step.timeoutSeconds" type="number" min="1" max="7200" />
                    </label>
                    <label>
                      执行权限
                      <select v-model="step.privilege">
                        <option value="ACCOUNT">执行账号</option>
                        <option value="ROOT">管理员</option>
                      </select>
                    </label>
                  </div>
                </section>
                <div class="project-section-heading project-form-section">
                  <h4>运行方式</h4>
                </div>
                <div class="project-fields">
                  <label>
                    运行方式
                    <select v-model="module.runtime.kind">
                      <option v-for="(label, kind) in runtimeKinds" :key="kind" :value="kind">{{ label }}</option>
                    </select>
                  </label>
                  <label class="project-full">
                    启动命令或参数
                    <input v-model="module.runtime.command" placeholder="Docker 留空沿用镜像默认；原生服务填写启动命令" />
                  </label>
                  <label>
                    就绪检查
                    <select v-model="module.runtime.health.kind">
                      <option v-for="(label, kind) in healthKinds" :key="kind" :value="kind">{{ label }}</option>
                    </select>
                  </label>
                  <label v-if="module.runtime.health.kind !== 'NONE'">
                    检查超时（秒）
                    <input v-model.number="module.runtime.health.timeoutSeconds" type="number" min="1" max="600" />
                  </label>
                  <label v-if="module.runtime.health.kind === 'HTTP'">
                    检查路径
                    <input v-model="module.runtime.health.path" placeholder="/health" />
                  </label>
                  <label v-if="['HTTP', 'TCP'].includes(module.runtime.health.kind)">
                    服务端口
                    <input v-model.number="module.runtime.health.port" type="number" min="0" max="65535" />
                  </label>
                  <label v-if="module.runtime.health.kind === 'COMMAND'" class="project-full">
                    验证命令
                    <input v-model="module.runtime.health.command" placeholder="填写验证命令" />
                  </label>
                </div>
                <div class="project-remove">
                  <button type="button" class="quiet danger" @click="removeModule">移除此模块</button>
                </div>
              </div>
            </div>
          </div>
          <div v-else class="project-tab-content">
            <div class="project-section-heading">
              <h3>部署环境</h3>
              <button type="button" class="secondary" @click="addEnvironment">添加环境</button>
            </div>
            <div v-if="!draft.environments.length" class="project-blank">添加 test、QA 或 prod，分别配置主机和运行参数。</div>
            <template v-else>
              <nav class="project-environment-nav" aria-label="环境列表">
                <button
                  v-for="item in draft.environments"
                  :key="item.id"
                  type="button"
                  :class="{ active: selectedEnvironmentId === item.id }"
                  @click="selectEnvironment(item.id)"
                >
                  {{ item.name || '未命名环境' }}
                </button>
              </nav>
              <template v-if="environment">
                <div class="project-section-heading">
                  <label class="environment-name">
                    环境名称
                    <input v-model="environment.name" maxlength="60" placeholder="例如：test" />
                  </label>
                  <button type="button" class="quiet danger" @click="removeEnvironment">移除环境</button>
                </div>
                <div class="project-section-heading project-form-section">
                  <h4>模块部署配置</h4>
                  <select
                    class="add-deployment"
                    aria-label="添加模块部署配置"
                    :value="''"
                    @change="addDeployment(($event.target as HTMLSelectElement).value); ($event.target as HTMLSelectElement).value = ''"
                  >
                    <option value="">添加模块…</option>
                    <option v-for="item in availableModules" :key="item.id" :value="item.id">{{ item.name || '未命名模块' }}</option>
                  </select>
                </div>
                <div v-if="!environment.deployments.length" class="project-blank">选择要部署到此环境的模块。</div>
                <div v-else class="project-split">
                  <nav class="project-item-nav" aria-label="环境模块列表">
                    <button
                      v-for="item in environment.deployments"
                      :key="item.id"
                      type="button"
                      :class="{ active: selectedDeploymentId === item.id }"
                      @click="selectedDeploymentId = item.id"
                    >
                      {{ moduleName(item.moduleId) }}
                    </button>
                  </nav>
                  <div v-if="deployment" class="project-module-form">
                    <div class="project-fields">
                      <label>
                        部署主机
                        <select v-model="deployment.hostId" @change="deployment.runtimeSoftwareId = ''">
                          <option value="">稍后选择</option>
                          <option v-for="host in hosts" :key="host.id" :value="host.id">{{ host.name }}</option>
                        </select>
                      </label>
                      <label>
                        运行环境
                        <select v-model="deployment.runtimeSoftwareId" :disabled="!deployment.hostId">
                          <option value="">自定义或稍后选择</option>
                          <option v-for="item in softwareChoices(deployment.hostId)" :key="item.id" :value="item.id">
                            {{ item.name }}
                            ·
                            {{ softwareVersionText(item.softwareVersion) }}
                          </option>
                        </select>
                      </label>
                      <label>
                        监听地址
                        <input v-model="deployment.bindAddress" placeholder="例如：0.0.0.0" />
                      </label>
                      <label>
                        内存上限（MiB）
                        <input v-model.number="deployment.memoryMiB" type="number" min="0" max="1048576" placeholder="0 表示未设置" />
                      </label>
                    </div>
                    <div class="project-section-heading project-form-section">
                      <h4>端口</h4>
                      <button type="button" class="quiet" @click="deployment.ports.push({ hostPort: 0, containerPort: 0, protocol: 'TCP' })">
                        添加端口
                      </button>
                    </div>
                    <div v-for="(port, index) in deployment.ports" :key="index" class="project-fields project-port">
                      <label>
                        主机端口
                        <input v-model.number="port.hostPort" type="number" min="1" max="65535" />
                      </label>
                      <label>
                        服务端口
                        <input v-model.number="port.containerPort" type="number" min="1" max="65535" />
                      </label>
                      <label>
                        协议
                        <select v-model="port.protocol">
                          <option>TCP</option>
                          <option>UDP</option>
                        </select>
                      </label>
                      <button type="button" class="quiet danger row-remove" @click="deployment.ports.splice(index, 1)">移除</button>
                    </div>
                    <div class="project-section-heading project-form-section">
                      <h4>目录挂载</h4>
                      <button type="button" class="quiet" @click="deployment.mounts.push({ source: '', target: '', readOnly: false })">添加目录</button>
                    </div>
                    <div v-for="(mount, index) in deployment.mounts" :key="index" class="project-fields project-mount">
                      <label>
                        主机目录
                        <input v-model="mount.source" placeholder="/srv/project/data" />
                      </label>
                      <label>
                        运行目录
                        <input v-model="mount.target" placeholder="/app/data" />
                      </label>
                      <label>
                        权限
                        <select :value="String(mount.readOnly)" @change="mount.readOnly = ($event.target as HTMLSelectElement).value === 'true'">
                          <option value="false">读写</option>
                          <option value="true">只读</option>
                        </select>
                      </label>
                      <button type="button" class="quiet danger row-remove" @click="deployment.mounts.splice(index, 1)">移除</button>
                    </div>
                    <div class="project-section-heading project-form-section">
                      <h4>环境变量</h4>
                      <button type="button" class="quiet" @click="addVariable(deployment)">添加变量</button>
                    </div>
                    <div v-for="(variable, index) in deployment.variables" :key="index" class="project-variable">
                      <input v-model="variable.name" aria-label="变量名称" placeholder="变量名称" />
                      <select
                        :value="usesCredential(variable) ? 'CREDENTIAL' : 'VALUE'"
                        aria-label="变量来源"
                        @change="changeVariableMode(variable, ($event.target as HTMLSelectElement).value as 'VALUE' | 'CREDENTIAL')"
                      >
                        <option value="VALUE">普通配置</option>
                        <option value="CREDENTIAL">连接凭据</option>
                      </select>
                      <select
                        v-if="usesCredential(variable)"
                        :value="variable.credentialId"
                        aria-label="选择凭据"
                        @change="setVariableCredential(variable, ($event.target as HTMLSelectElement).value)"
                      >
                        <option value="">选择凭据</option>
                        <option v-for="credential in credentials" :key="credential.id" :value="credential.id">{{ credential.name }}</option>
                      </select>
                      <input v-else v-model="variable.value" aria-label="变量值" placeholder="普通值；密码和 Token 请使用连接凭据" />
                      <button type="button" class="quiet danger" @click="deployment.variables.splice(index, 1)">移除</button>
                    </div>
                    <details class="project-form-section">
                      <summary>日志来源与保留配置</summary>
                      <div class="project-fields">
                        <label>
                          日志来源
                          <select v-model="deployment.logs.kind">
                            <option v-for="(label, kind) in logKinds" :key="kind" :value="kind">{{ label }}</option>
                          </select>
                        </label>
                        <template v-if="deployment.logs.kind === 'FILE'">
                          <label>
                            日志目录
                            <input v-model="deployment.logs.directory" placeholder="/srv/project/logs" />
                          </label>
                          <label>
                            文件规则
                            <input v-model="deployment.logs.pattern" placeholder="*.log" />
                          </label>
                          <label>
                            保留天数
                            <input v-model.number="deployment.logs.retentionDays" type="number" min="0" max="3650" placeholder="0 表示未设置" />
                          </label>
                          <label>
                            容量上限（MiB）
                            <input v-model.number="deployment.logs.maxMiB" type="number" min="0" max="1048576" placeholder="0 表示未设置" />
                          </label>
                        </template>
                      </div>
                      <p class="project-note">此处保存配置，暂不启动日志采集或自动清理。</p>
                    </details>
                    <div class="project-remove">
                      <button type="button" class="quiet danger" @click="removeDeployment">移除此部署配置</button>
                    </div>
                  </div>
                </div>
              </template>
            </template>
          </div>
        </fieldset>
      </div>
      <template #footer>
        <div class="project-dialog-footer">
          <p v-if="editorError" class="project-error" role="alert">{{ editorError }}</p>
          <div class="project-actions">
            <button class="secondary" @click="closeEditor(() => editorOpen = false)">关闭</button>
            <button class="primary" :disabled="saving" @click="saveProject">{{ saving ? "保存中…" : "保存配置" }}</button>
          </div>
        </div>
      </template>
    </ElDialog>
  </section>
</template>

<style scoped>
.application-projects { min-width: 0; }
:global(.application-project-dialog.el-dialog) {
  display: flex;
  flex-direction: column;
  max-height: calc(100dvh - var(--ops-page-gutter, 24px) - var(--ops-page-gutter, 24px));
  overflow: hidden;
}
:global(.application-project-dialog > .el-dialog__header),
:global(.application-project-dialog > .el-dialog__footer) { flex-shrink: 0; }
:global(.application-project-dialog > .el-dialog__header) { margin-bottom: 16px; }
:global(.application-project-dialog > .el-dialog__body) { display: flex; flex: 1 1 auto; min-height: 0; overflow: hidden; }
:global(.application-project-dialog > .el-dialog__footer) { padding-top: 16px; }
.project-toolbar, .project-filters, .project-actions, .project-section-heading, .project-card-title, .project-card footer, .project-step-title { display: flex; align-items: center; gap: 12px; }
.project-toolbar, .project-section-heading, .project-card footer { justify-content: space-between; }
.project-toolbar { margin-bottom: var(--ops-page-gap, 16px); flex-wrap: wrap; }
.project-filters { flex: 1; flex-wrap: wrap; }
.project-filters input, .project-filters select { margin: 0; width: auto; }
.project-filters input { width: 260px; }
.project-filters select { width: 160px; }
.project-list { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 420px), 1fr)); gap: var(--ops-page-gap, 16px); }
.project-card { min-width: 0; border: 1px solid var(--border); border-radius: var(--card-radius); background: var(--surface); }
.project-card-title { align-items: flex-start; padding: 22px 22px 14px; }
.project-card-title > div { min-width: 0; flex: 1; }
.project-symbol { display: grid; place-items: center; flex: 0 0 42px; height: 42px; border-radius: 10px; color: var(--accent); background: var(--accent-soft); font-size: 20px; font-weight: 600; }
.project-card h2 { margin: 1px 0 5px; font-size: 17px; overflow-wrap: anywhere; }
.project-card p { color: var(--muted); margin: 0; font-size: 12px; line-height: 1.7; overflow-wrap: anywhere; }
.project-state { white-space: nowrap; padding: 3px 7px; color: #15803d; background: #f0fdf4; border-radius: 4px; font-size: 11px; }
.project-state.archived { color: var(--muted); background: #f1f5f9; }
.project-card-summary { display: flex; gap: 20px; padding: 6px 22px 20px; font-size: 12px; color: var(--muted); }
.project-card footer { border-top: 1px solid var(--border-subtle); padding: 12px 18px; gap: 8px; flex-wrap: wrap; font-size: 11px; color: var(--muted); background: #fbfcfe; }
.project-card footer .project-actions { gap: 6px; margin-left: auto; }
.project-empty { grid-column: 1 / -1; text-align: center; padding: 48px 20px; border: 1px dashed var(--border); background: var(--surface); border-radius: var(--card-radius); color: var(--muted); }
.project-empty h2 { color: var(--text); font-size: 18px; font-weight: 500; }
.project-empty p, .project-note { font-size: 12px; color: var(--muted); line-height: 1.6; }
.project-error { margin: 0; padding: 10px 12px; background: #fef2f2; border-radius: 5px; color: #b91c1c; line-height: 1.6; font-size: 13px; overflow-wrap: anywhere; }
.application-projects > .project-error { margin-bottom: 16px; }
.project-editor { flex: 1; min-width: 0; min-height: 0; overflow-y: auto; overscroll-behavior: contain; scrollbar-gutter: stable; padding-right: 8px; }
fieldset { border: 0; padding: 0; margin: 0; min-width: 0; }
.project-fields { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
.project-fields label { min-width: 0; }
.project-basics { padding: 0 2px 16px; }
.project-tabs, .project-environment-nav { display: flex; gap: 6px; overflow-x: auto; }
.project-tabs { position: sticky; top: 0; z-index: 1; border-bottom: 1px solid var(--border); margin-bottom: 16px; background: var(--surface); }
.project-tabs button { padding: 12px 18px; border: 0; border-bottom: 2px solid transparent; background: none; font-size: 13px; color: var(--muted); white-space: nowrap; }
.project-tabs button.active { border-bottom-color: var(--accent); color: var(--accent); font-weight: 600; }
.project-tab-content { padding: 0 2px 8px; }
.project-section-heading { margin-bottom: 16px; }
.project-section-heading button { flex-shrink: 0; white-space: nowrap; }
.project-section-heading h3, .project-section-heading h4 { margin: 0; font-size: 14px; }
.project-config-block, .project-step { border: 1px solid var(--border); border-radius: 7px; padding: 16px; margin-bottom: 16px; }
.project-config-block .project-section-heading { margin-bottom: 12px; }
.project-blank { padding: 32px 20px; border: 1px dashed var(--border); border-radius: 6px; text-align: center; color: var(--muted); font-size: 13px; }
.project-split { display: grid; grid-template-columns: 170px minmax(0, 1fr); gap: 24px; }
.project-item-nav { display: flex; flex-direction: column; gap: 5px; align-self: start; }
.project-item-nav button, .project-environment-nav button { border: 1px solid transparent; background: #f8fafc; color: #475569; padding: 10px 12px; border-radius: 5px; font-size: 12px; text-align: left; overflow-wrap: anywhere; }
.project-item-nav button.active, .project-environment-nav button.active { border-color: #bfdbfe; background: var(--accent-soft); color: var(--accent); }
.project-module-form { min-width: 0; }
.project-form-section { margin-top: 20px; }
.project-full { grid-column: 1 / -1; }
.project-three { grid-template-columns: repeat(3, minmax(0, 1fr)); }
.project-step { padding: 14px; background: #fbfcfe; }
.project-step-title { gap: 6px; margin-bottom: 14px; }
.project-step-title > span { display: grid; place-items: center; width: 24px; height: 24px; flex-shrink: 0; border-radius: 50%; background: #dbeafe; color: var(--accent); font-size: 12px; }
.project-step-title input { margin: 0; min-width: 0; }
.project-step-title .quiet { flex-shrink: 0; padding: 4px; }
.project-command { display: block; margin-bottom: 14px; }
.project-command textarea { font-family: var(--mono); font-size: 12px; line-height: 1.6; min-height: 100px; resize: vertical; }
.project-remove { margin-top: 28px; padding-top: 12px; border-top: 1px solid var(--border-subtle); }
.danger { color: var(--error); }
.project-environment-nav { margin-bottom: 18px; flex-wrap: wrap; }
.environment-name { width: min(100%, 360px); }
.add-deployment { margin: 0; width: 200px; }
.project-port, .project-mount { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) 100px auto; align-items: end; gap: 12px; margin-bottom: 14px; }
.row-remove { min-height: 38px; }
.project-variable { display: grid; grid-template-columns: minmax(0, 1fr) 110px minmax(0, 1.3fr) auto; align-items: center; gap: 10px; margin-bottom: 12px; }
.project-variable input, .project-variable select { margin: 0; }
details summary { cursor: pointer; font-size: 13px; color: #475569; margin-bottom: 16px; }
.project-dialog-footer { display: flex; justify-content: flex-end; align-items: center; gap: 14px; }
.project-dialog-footer .project-error { flex: 1; text-align: left; max-height: min(96px, 20dvh); overflow-y: auto; }
.project-dialog-footer .project-actions { flex-shrink: 0; }
@media (max-width: 760px) {
  .project-split { grid-template-columns: minmax(0, 1fr); gap: 18px; }
  .project-item-nav { flex-direction: row; flex-wrap: wrap; }
  .project-fields, .project-three { grid-template-columns: minmax(0, 1fr); gap: 14px; }
  .project-full { grid-column: auto; }
  .project-port, .project-mount { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .project-variable { grid-template-columns: minmax(0, 1fr) 100px; }
  .project-variable > :nth-child(3) { grid-column: 1; }
  .project-filters { width: 100%; flex-basis: 100%; }
  .project-filters input { width: 100%; }
  .project-card-summary { gap: 14px; }
  .project-dialog-footer { align-items: stretch; flex-direction: column; }
  .project-dialog-footer .project-actions { justify-content: flex-end; }
}
@media (max-height: 560px) {
  :global(.application-project-dialog.el-dialog) { --el-dialog-padding-primary: 16px; }
  :global(.application-project-dialog > .el-dialog__header) { margin-bottom: 12px; padding-bottom: 10px; }
  :global(.application-project-dialog > .el-dialog__footer) { padding-top: 12px; }
}
</style>
