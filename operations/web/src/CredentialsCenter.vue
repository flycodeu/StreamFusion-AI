<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import {
  ElButton,
  ElEmpty,
  ElInput,
  ElPagination,
  ElRadioButton,
  ElRadioGroup,
  ElTabPane,
  ElTable,
  ElTableColumn,
  ElTabs,
  ElTag,
  ElTooltip,
} from "element-plus";
import { request } from "./api";
import { requestAllPages } from "./listPages";
import { usePagination } from "./pagination";
import { hostTime, type Host } from "./hosts";
import {
  softwareIsHistoryOnly,
  softwareState,
  type SoftwareInstance,
} from "./software";
import {
  jenkinsConsoleURL,
  jenkinsPluginManagerAccess,
  jenkinsSecurityTokenURL,
  softwareWebAccess,
} from "./webAccess";
import JenkinsUnlockPassword from "./JenkinsUnlockPassword.vue";
import { canCopyJenkinsUnlockPassword } from "./jenkinsUnlock";

export interface CredentialItem {
  id: string;
  revision: string;
  name: string;
  kind: string;
  createdAt: string;
  revokedAt: string;
}

const props = defineProps<{
  credentials: CredentialItem[];
  busy: boolean;
}>();

const emit = defineEmits<{
  refresh: [];
  create: [];
  newRevision: [CredentialItem];
  revoke: [CredentialItem];
  navigate: [target: { view: string; softwareId?: string; search?: string }];
  error: [unknown];
}>();

const activeTab = ref<"credentials" | "jenkins">("credentials");
const kindFilter = ref("ALL");
const query = ref("");
const expandedGroups = ref<Record<string, boolean>>({});
const hosts = ref<Host[]>([]);
const softwareInstances = ref<SoftwareInstance[]>([]);
const loadingIntegrations = ref(false);
let disposed = false;

const credentialKindLabels: Record<string, string> = {
  TOKEN: "Token 密钥",
  PASSWORD: "密码凭据",
  SSH_KEY: "SSH 私钥",
};

interface CredentialGroup {
  id: string;
  latest: CredentialItem;
  revisions: CredentialItem[];
  referencedHosts: Host[];
}

const hostsByCredential = computed(() => {
  const map = new Map<string, Host[]>();
  for (const host of hosts.value) {
    if (!host.credentialId) continue;
    const list = map.get(host.credentialId) || [];
    list.push(host);
    map.set(host.credentialId, list);
  }
  return map;
});

const groups = computed<CredentialGroup[]>(() => {
  const byId = new Map<string, CredentialItem[]>();
  for (const item of props.credentials) {
    const list = byId.get(item.id) || [];
    list.push(item);
    byId.set(item.id, list);
  }
  const term = query.value.trim().toLowerCase();
  const result: CredentialGroup[] = [];
  for (const [id, list] of byId.entries()) {
    const sorted = [...list].sort(
      (a, b) => Number(b.revision) - Number(a.revision),
    );
    const latest = sorted[0]!;
    if (kindFilter.value !== "ALL" && latest.kind !== kindFilter.value) {
      continue;
    }
    const referencedHosts = hostsByCredential.value.get(id) || [];
    if (term) {
      const text = [
        latest.name,
        credentialKindLabels[latest.kind] || latest.kind,
        ...referencedHosts.map((h) => `${h.name} ${h.address}`),
      ]
        .join(" ")
        .toLowerCase();
      if (!text.includes(term)) continue;
    }
    result.push({ id, latest, revisions: sorted, referencedHosts });
  }
  return result.sort((a, b) =>
    (b.latest.createdAt || "").localeCompare(a.latest.createdAt || ""),
  );
});

const {
  page: credentialPage,
  pageSize: credentialPageSize,
  total: credentialTotal,
  pageItems: pagedGroups,
} = usePagination(() => groups.value, [kindFilter, query]);

const jenkinsInstances = computed(() => {
  const hostMap = new Map(hosts.value.map((h) => [h.id, h]));
  return softwareInstances.value
    .filter(
      (item) =>
        (item.softwareType === "JENKINS" ||
          item.installation?.definition?.catalogKey === "jenkins" ||
          item.installation?.definition?.key === "jenkins") &&
        item.managementMode !== "RELEASED" &&
        !softwareIsHistoryOnly(item) &&
        item.installationStatus !== "UNINSTALLED",
    )
    .map((item) => {
      const host = hostMap.get(item.hostId);
      const access = softwareWebAccess(item, host);
      const plugins = jenkinsPluginManagerAccess(item, host);
      return {
        item,
        host,
        access,
        consoleUrl: access?.url ? jenkinsConsoleURL(access.url) : "",
        tokenUrl: access?.url ? jenkinsSecurityTokenURL(access.url) : "",
        pluginsUrl: plugins?.url || "",
      };
    });
});

function stateTagType(tone: string): "success" | "danger" | "warning" | "info" {
  if (tone === "state-success") return "success";
  if (tone === "state-error") return "danger";
  if (tone === "state-warning") return "warning";
  return "info";
}

function toggleHistory(id: string) {
  expandedGroups.value[id] = !expandedGroups.value[id];
}

async function loadIntegrations() {
  if (loadingIntegrations.value || disposed) return;
  loadingIntegrations.value = true;
  try {
    const [hostList, softwareList] = await Promise.all([
      request<Host[]>("/hosts"),
      requestAllPages<SoftwareInstance>("/software-instances"),
    ]);
    if (disposed) return;
    hosts.value = hostList;
    softwareInstances.value = softwareList;
  } catch (error) {
    if (!disposed) emit("error", error);
  } finally {
    if (!disposed) loadingIntegrations.value = false;
  }
}

onMounted(() => {
  void loadIntegrations();
});
onUnmounted(() => {
  disposed = true;
});
</script>

<template>
  <section class="content credentials-center" aria-label="凭据与集成">
    <ElTabs v-model="activeTab" class="cred-tabs">
      <ElTabPane :label="`凭据库（${props.credentials.length ? groups.length : 0}）`" name="credentials">
        <div class="panel-card">
          <div class="ops-el-toolbar">
            <div class="ops-el-toolbar-group">
              <ElInput
                v-model="query"
                clearable
                placeholder="搜索凭据名称或引用服务器"
                style="width: 240px"
              />
              <ElRadioGroup v-model="kindFilter" size="small">
                <ElRadioButton value="ALL">全部</ElRadioButton>
                <ElRadioButton value="SSH_KEY">SSH 私钥</ElRadioButton>
                <ElRadioButton value="PASSWORD">密码</ElRadioButton>
                <ElRadioButton value="TOKEN">Token</ElRadioButton>
              </ElRadioGroup>
            </div>
            <div class="ops-el-toolbar-group">
              <ElButton
                :disabled="busy || loadingIntegrations"
                @click="
                  emit('refresh');
                  void loadIntegrations();
                "
              >
                刷新
              </ElButton>
              <ElButton type="primary" :disabled="busy" @click="emit('create')">
                新增凭据
              </ElButton>
            </div>
          </div>

          <ElTable
            :data="pagedGroups"
            row-key="id"
            border
            stripe
            style="width: 100%; margin-top: 14px"
          >
            <ElTableColumn type="expand" width="44">
              <template #default="{ row }">
                <div class="cred-expand-wrap">
                  <div class="cred-expand-title">修订版本记录（共 {{ row.revisions.length }} 个版本）</div>
                  <ElTable :data="row.revisions" border size="small" style="width: 100%">
                    <ElTableColumn label="版本" width="90">
                      <template #default="{ row: rev }">
                        <code>r{{ rev.revision }}</code>
                      </template>
                    </ElTableColumn>
                    <ElTableColumn label="名称" prop="name" min-width="160" />
                    <ElTableColumn label="状态" width="95">
                      <template #default="{ row: rev }">
                        <ElTag size="small" :type="rev.revokedAt ? 'info' : 'success'">
                          {{ rev.revokedAt ? "已撤销" : "可用" }}
                        </ElTag>
                      </template>
                    </ElTableColumn>
                    <ElTableColumn label="创建时间" width="175">
                      <template #default="{ row: rev }">
                        {{ hostTime(rev.createdAt) }}
                      </template>
                    </ElTableColumn>
                    <ElTableColumn label="操作" width="120" fixed="right">
                      <template #default="{ row: rev }">
                        <ElButton
                          v-if="!rev.revokedAt"
                          link
                          type="danger"
                          size="small"
                          :disabled="busy"
                          @click="emit('revoke', rev as CredentialItem)"
                        >
                          撤销修订
                        </ElButton>
                        <span v-else class="muted">—</span>
                      </template>
                    </ElTableColumn>
                  </ElTable>
                </div>
              </template>
            </ElTableColumn>
            <ElTableColumn label="凭据名称" min-width="180">
              <template #default="{ row }">
                <span>{{ row.latest.name }}</span>
              </template>
            </ElTableColumn>
            <ElTableColumn label="类型" width="115">
              <template #default="{ row }">
                <span>{{ credentialKindLabels[row.latest.kind] || row.latest.kind }}</span>
              </template>
            </ElTableColumn>
            <ElTableColumn label="当前版本" width="110">
              <template #default="{ row }">
                <code>r{{ row.latest.revision }}</code>
                <span v-if="row.revisions.length > 1" class="muted" style="margin-left: 6px; font-size: 12px">
                  ({{ row.revisions.length }})
                </span>
              </template>
            </ElTableColumn>
            <ElTableColumn label="引用服务器" min-width="190">
              <template #default="{ row }">
                <div v-if="row.referencedHosts.length" class="host-ref-list">
                  <ElTag
                    v-for="host in row.referencedHosts"
                    :key="host.id"
                    size="small"
                    effect="light"
                  >
                    {{ host.name }}
                  </ElTag>
                </div>
                <span v-else class="muted">暂无主机直接绑定</span>
              </template>
            </ElTableColumn>
            <ElTableColumn label="状态" width="90" align="center">
              <template #default="{ row }">
                <ElTag size="small" :type="row.latest.revokedAt ? 'info' : 'success'">
                  {{ row.latest.revokedAt ? "已撤销" : "可用" }}
                </ElTag>
              </template>
            </ElTableColumn>
            <ElTableColumn label="更新时间" width="175">
              <template #default="{ row }">
                {{ hostTime(row.latest.createdAt) }}
              </template>
            </ElTableColumn>
            <ElTableColumn label="操作" width="150" fixed="right">
              <template #default="{ row }">
                <div class="ops-el-toolbar-group" style="gap: 8px">
                  <ElButton
                    v-if="!row.latest.revokedAt"
                    link
                    type="primary"
                    size="small"
                    :disabled="busy"
                    @click="emit('newRevision', row.latest)"
                  >
                    轮换修订
                  </ElButton>
                  <ElButton
                    v-if="!row.latest.revokedAt"
                    link
                    type="danger"
                    size="small"
                    :disabled="busy"
                    @click="emit('revoke', row.latest)"
                  >
                    撤销
                  </ElButton>
                </div>
              </template>
            </ElTableColumn>
            <template #empty>
              <ElEmpty
                :description="
                  props.credentials.length
                    ? '没有匹配的凭据记录'
                    : '尚未保存任何凭据，点击右上角「新增凭据」添加 SSH 私钥或密码'
                "
              />
            </template>
          </ElTable>

          <div v-if="credentialTotal > 0" class="ops-el-pager">
            <ElPagination
              v-model:current-page="credentialPage"
              v-model:page-size="credentialPageSize"
              :page-sizes="[10, 20, 50]"
              :total="credentialTotal"
              background
              layout="total, sizes, prev, pager, next"
            />
          </div>
        </div>
      </ElTabPane>

      <ElTabPane :label="`CI/CD 引擎（Jenkins ${jenkinsInstances.length}）`" name="jenkins">
        <div class="panel-card">
          <div class="ops-el-toolbar" style="margin-bottom: 14px">
            <h2>CI/CD 引擎（Jenkins）</h2>
            <div class="ops-el-toolbar-group">
              <ElButton
                :loading="loadingIntegrations"
                @click="loadIntegrations"
              >
                刷新
              </ElButton>
              <ElButton
                type="primary"
                @click="emit('navigate', { view: 'software-install', search: 'Jenkins' })"
              >
                安装 Jenkins
              </ElButton>
            </div>
          </div>

          <ElEmpty
            v-if="!jenkinsInstances.length"
            description="尚未在任何服务器上安装 Jenkins 实例"
          >
            <ElButton
              type="primary"
              @click="emit('navigate', { view: 'software-install', search: 'Jenkins' })"
            >
              前往软件安装部署 Jenkins
            </ElButton>
          </ElEmpty>
          <ElTable
            v-else
            :data="jenkinsInstances"
            border
            stripe
            style="width: 100%"
          >
            <ElTableColumn type="index" label="#" width="52" align="center" />
            <ElTableColumn label="实例名称" min-width="150">
              <template #default="{ row }">
                <span>{{ row.item.name }}</span>
              </template>
            </ElTableColumn>
            <ElTableColumn label="所属服务器" min-width="170">
              <template #default="{ row }">
                <span>{{ row.host?.name || row.item.hostName || row.item.hostId }}</span>
                <span v-if="row.host?.address" class="muted" style="margin-left: 6px; font-size: 12px">
                  {{ row.host.address }}
                </span>
              </template>
            </ElTableColumn>
            <ElTableColumn label="运行版本" width="130">
              <template #default="{ row }">
                <span>{{ row.item.observation?.version || row.item.softwareVersion || "未确认" }}</span>
              </template>
            </ElTableColumn>
            <ElTableColumn label="状态" width="100" align="center">
              <template #default="{ row }">
                <ElTag size="small" :type="stateTagType(softwareState(row.item).tone)">
                  {{ softwareState(row.item).label }}
                </ElTag>
              </template>
            </ElTableColumn>
            <ElTableColumn label="Web 入口" min-width="220" show-overflow-tooltip>
              <template #default="{ row }">
                <code v-if="row.consoleUrl">{{ row.consoleUrl }}</code>
                <span v-else class="muted">{{ row.access?.reason || "当前不可达或服务未运行" }}</span>
              </template>
            </ElTableColumn>
            <ElTableColumn label="操作" width="320" fixed="right">
              <template #default="{ row }">
                <div class="jenkins-row-actions">
                  <ElButton
                    v-if="row.consoleUrl"
                    tag="a"
                    :href="row.consoleUrl"
                    target="_blank"
                    rel="noopener noreferrer"
                    link
                    type="primary"
                    size="small"
                  >
                    控制台 ↗
                  </ElButton>
                  <ElTooltip
                    v-if="row.tokenUrl"
                    content="请在 Jenkins 右上角用户名 → Security → API Token → Add new Token 生成个人令牌（不要用全局 Credentials）"
                    placement="top"
                  >
                    <ElButton
                      tag="a"
                      :href="row.tokenUrl"
                      target="_blank"
                      rel="noopener noreferrer"
                      link
                      type="primary"
                      size="small"
                    >
                      生成 Token ↗
                    </ElButton>
                  </ElTooltip>
                  <ElButton
                    v-if="row.pluginsUrl"
                    tag="a"
                    :href="row.pluginsUrl"
                    target="_blank"
                    rel="noopener noreferrer"
                    link
                    size="small"
                  >
                    插件管理 ↗
                  </ElButton>
                  <JenkinsUnlockPassword
                    v-if="canCopyJenkinsUnlockPassword(row.item)"
                    :instance-id="row.item.id"
                    :instance="row.item"
                  />
                  <ElButton
                    link
                    size="small"
                    @click="emit('navigate', { view: 'software', softwareId: row.item.id })"
                  >
                    详情
                  </ElButton>
                </div>
              </template>
            </ElTableColumn>
          </ElTable>
        </div>
      </ElTabPane>
    </ElTabs>
  </section>
</template>

<style scoped>
.credentials-center {
  display: grid;
  gap: 0;
}
.cred-expand-wrap {
  padding: 10px 18px;
  display: grid;
  gap: 8px;
  background: #f8fafc;
}
.cred-expand-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--text-secondary);
}
.host-ref-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.jenkins-row-actions {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
.jenkins-row-actions a {
  text-decoration: none;
}
</style>
