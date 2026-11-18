<script setup lang="ts">
import ErrorHelpLink from "./ErrorHelpLink.vue";
import NodeRegistryPanel from "./NodeRegistryPanel.vue";
import OpsPagination from "./OpsPagination.vue";
import { hostTime } from "./hosts";
import {
  nodeVersionRow,
  type NodeAction,
  type NodeEnvironment,
  type NodeRegistry,
  type NodeVersionObservation,
} from "./nodeEnvironment";
import {
  softwareJobDiagnostic,
  softwareJobErrorMessages,
  softwareJobStatusLabel,
  type SoftwareInstance,
  type SoftwareJob,
} from "./software";

defineProps<{
  environment: NodeEnvironment;
  desiredDefault: string;
  busy: boolean;
  writable: boolean;
  pending: boolean;
  tab: string;
  versions: NodeVersionObservation[];
  pagedVersions: NodeVersionObservation[];
  versionPage: number;
  versionPageSize: number;
  versionTotal: number;
  pagedSources: SoftwareInstance[];
  sourcePage: number;
  sourcePageSize: number;
  sourceTotal: number;
  jobs: SoftwareJob[];
  historyPage: number;
  historyPageSize: number;
  historyTotal: number;
  logsJobId: string;
  logs: string;
  job?: SoftwareJob;
}>();

const emit = defineEmits<{
  "update:tab": [value: string];
  "update:versionPage": [value: number];
  "update:versionPageSize": [value: number];
  "update:sourcePage": [value: number];
  "update:sourcePageSize": [value: number];
  openAction: [action: NodeAction, nodeVersion?: string];
  openTerminal: [];
  selectRegistry: [entry: NodeRegistry];
  registryCloseState: [state: { busy: boolean; dirty: boolean }];
  showJob: [item: SoftwareJob];
  changeHistoryPage: [page: number];
  changeHistoryPageSize: [pageSize: number];
  jobAction: [action: "reconcile" | "cancel"];
}>();
</script>

<template>
  <div class="environment-summary">
    <div>
      <span>执行账号</span><strong>{{ environment.account }}</strong>
    </div>
    <div>
      <span>实际默认</span
      ><strong>{{ environment.observedDefaultVersion || "未设置" }}</strong>
      <span>期望：{{ desiredDefault }}</span>
    </div>
    <div>
      <span>管理方式</span
      ><strong
        >{{
          environment.managementMode === "MANAGED" ? "Ops 管理" : "只读登记"
        }}
        ·
        {{ environment.managerType === "NVM" ? "nvm" : "系统安装" }}</strong
      >
    </div>
    <div>
      <span>上次检查</span
      ><strong>{{ hostTime(environment.observedAt) }}</strong>
    </div>
  </div>
  <div v-if="environment.managerType === 'NVM'" class="terminal-access">
    <span>{{
      environment.observation?.terminal?.status === "CONNECTED"
        ? "Bash 已接入"
        : environment.observation?.terminal?.status === "CONFLICT"
          ? "Bash 配置冲突"
          : environment.observation?.terminal?.status === "UNSUPPORTED"
            ? "当前终端不支持自动接入"
            : environment.observation?.terminal?.status === "NOT_CONNECTED"
              ? "Bash 未接入"
              : "终端接入待检查"
    }}</span>
    <button
      v-if="
        environment.managementMode === 'MANAGED' &&
        environment.observation?.terminal?.status !== 'CONNECTED'
      "
      class="secondary"
      :disabled="busy || !writable"
      @click="emit('openAction', 'CONNECT_TERMINAL')"
    >
      接入终端
    </button>
    <span
      v-if="
        !environment.observedDefaultVersion &&
        environment.observation?.terminal?.status === 'CONNECTED'
      "
      class="hint"
      >选择默认版本后，新终端可使用 node/npm</span
    >
    <span
      v-else-if="environment.observation?.terminal?.status === 'CONNECTED'"
      class="hint"
      >重新登录即可使用 node/npm</span
    >
  </div>
  <details v-if="environment.errorCode" class="hint">
    <summary>检测未完成，显示上次结果</summary>
    <ErrorHelpLink :code="environment.errorCode" /><code>{{
      environment.errorCode
    }}</code>
    <p>{{ hostTime(environment.observedAt) }} · {{ environment.id }}</p>
  </details>
  <div class="node-tabs" role="tablist" aria-label="Node 环境详情">
    <button
      v-for="entry in [
        { key: 'versions', name: '版本列表' },
        { key: 'sources', name: '来源与目录' },
        { key: 'registry', name: 'npm 镜像' },
        { key: 'history', name: '操作记录' },
      ]"
      :key="entry.key"
      role="tab"
      :aria-selected="tab === entry.key"
      :class="{ active: tab === entry.key }"
      @click="emit('update:tab', entry.key)"
    >
      {{ entry.name }}
    </button>
  </div>
  <section v-if="tab === 'versions'" class="version-list">
    <div class="list-actions">
      <button
        class="primary"
        :disabled="busy || !writable"
        @click="emit('openAction', 'INSTALL_NODE')"
      >
        安装 Node 版本
      </button>
    </div>
    <article
      v-for="item in pagedVersions"
      :key="item.version"
      class="node-version"
    >
      <div class="version-title">
        <strong>{{ item.version }}</strong
        ><span
          v-if="item.version === environment.observedDefaultVersion"
          class="default-badge"
          >默认</span
        >
      </div>
      <div class="package-versions">
        <span
          >npm <b>{{ item.npmVersion || "未确认" }}</b></span
        ><span
          >pnpm <b>{{ item.pnpmVersion || "未安装" }}</b></span
        >
      </div>
      <code>{{ item.binaryPath }}</code>
      <div class="actions">
        <button
          class="quiet"
          :disabled="
            busy ||
            !writable ||
            item.version === environment.observedDefaultVersion
          "
          @click="emit('openAction', 'SET_DEFAULT', item.version)"
        >
          设为默认</button
        ><button
          class="quiet"
          :disabled="busy || !writable"
          @click="emit('openAction', 'UPDATE_NPM', item.version)"
        >
          更新 npm</button
        ><button
          class="quiet"
          :disabled="busy || !writable"
          @click="
            emit(
              'openAction',
              item.pnpmVersion ? 'UPDATE_PNPM' : 'INSTALL_PNPM',
              item.version,
            )
          "
        >
          {{ item.pnpmVersion ? "更新 pnpm" : "安装 pnpm" }}</button
        ><button
          v-if="item.pnpmVersion"
          class="quiet"
          :disabled="busy || !writable"
          @click="emit('openAction', 'REMOVE_PNPM', item.version)"
        >
          移除 pnpm</button
        ><button
          class="quiet danger"
          :disabled="
            busy || !writable || !nodeVersionRow(environment, item.version)
          "
          @click="emit('openAction', 'UNINSTALL_NODE', item.version)"
        >
          卸载版本
        </button>
      </div>
      <details v-if="item.globalPackages?.length">
        <summary>全局工具（{{ item.globalPackages.length }}）</summary>
        <p v-for="pkg in item.globalPackages" :key="pkg.name">
          <code>{{ pkg.name }}</code> {{ pkg.version }}
        </p>
      </details>
    </article>
    <p v-if="!versions.length" class="empty">
      {{
        environment.managementMode === "MANAGED"
          ? "尚未安装 Node 版本。"
          : "未检测到可用版本。"
      }}
    </p>
    <OpsPagination
      :page="versionPage"
      :page-size="versionPageSize"
      :total="versionTotal"
      label="Node 版本分页"
      @update:page="emit('update:versionPage', $event)"
      @update:page-size="emit('update:versionPageSize', $event)"
    />
  </section>
  <section v-else-if="tab === 'sources'" class="environment-details">
    <details v-for="item in pagedSources" :key="item.id">
      <summary>
        {{ item.name }} ·
        {{
          item.installation?.source?.type === "OFFICIAL"
            ? "官方在线安装"
            : item.installation?.source?.type === "ARCHIVE"
              ? "Ops 离线安装"
              : "自行安装，来源未确认"
        }}
      </summary>
      <p v-if="item.installation?.source?.sha256">
        <code>归档 SHA-256 {{ item.installation.source.sha256 }}</code>
      </p>
      <div
        v-for="part in item.installation?.definition?.native?.parts || []"
        :key="part.name"
        class="source-file"
      >
        <strong>{{ part.name }}</strong
        ><a :href="part.url" target="_blank" rel="noopener noreferrer">{{
          part.url
        }}</a
        ><code>SHA-256 {{ part.sha256 }}</code>
      </div>
    </details>
    <OpsPagination
      :page="sourcePage"
      :page-size="sourcePageSize"
      :total="sourceTotal"
      label="Node 安装来源分页"
      @update:page="emit('update:sourcePage', $event)"
      @update:page-size="emit('update:sourcePageSize', $event)"
    />
    <dl>
      <dt>环境目录</dt>
      <dd>
        <code>{{ environment.rootPath }}</code>
      </dd>
      <dt>nvm 版本</dt>
      <dd>{{ environment.nvmVersion || "不适用" }}</dd>
      <dt>所属账号</dt>
      <dd>{{ environment.account }} · UID {{ environment.accountUid }}</dd>
    </dl>
    <div class="actions">
      <button class="secondary" @click="emit('openTerminal')">
        终端使用方式</button
      ><button
        v-if="environment.managerType === 'NVM'"
        class="secondary"
        :disabled="busy || !writable"
        @click="emit('openAction', 'UPDATE_NVM')"
      >
        检查 nvm 更新</button
      ><button
        class="quiet"
        :disabled="busy || pending"
        @click="emit('openAction', 'RELEASE')"
      >
        解除管理</button
      ><button
        class="quiet danger"
        :disabled="busy || !writable || !!versions.length"
        @click="emit('openAction', 'REMOVE_ENVIRONMENT')"
      >
        移除空环境
      </button>
    </div>
  </section>
  <NodeRegistryPanel
    v-else-if="tab === 'registry'"
    :environment="environment"
    :disabled="busy || !writable"
    @select="emit('selectRegistry', $event)"
    @close-state="emit('registryCloseState', $event)"
  />
  <section v-else class="node-history">
    <button
      v-for="item in jobs"
      :key="item.id"
      class="history-row"
      @click="emit('showJob', item)"
    >
      <span>{{ softwareJobStatusLabel(item.status) }}</span
      ><time>{{ hostTime(item.updatedAt) }}</time
      ><span>查看日志</span>
    </button>
    <p v-if="!jobs.length" class="empty">暂无操作记录</p>
    <OpsPagination
      :page="historyPage"
      :page-size="historyPageSize"
      :total="historyTotal"
      :disabled="busy"
      label="Node 操作记录分页"
      @update:page="emit('changeHistoryPage', $event)"
      @update:page-size="emit('changeHistoryPageSize', $event)"
    />
    <p v-if="logsJobId" class="hint">作业 {{ logsJobId }}</p>
    <pre v-if="logs">{{ logs }}</pre>
  </section>
  <div v-if="job" class="job-result" role="status">
    <strong>{{ softwareJobStatusLabel(job.status) }}</strong
    ><span>{{
      job.targetAckPending
        ? "正在等待目标确认收尾"
        : job.status === "UNKNOWN"
          ? "请核对原操作结果，避免重复执行"
          : ""
    }}</span
    ><p v-if="softwareJobErrorMessages[job.errorCode]">
      {{ softwareJobErrorMessages[job.errorCode] }}
    </p>
    <button
      v-if="job.status === 'UNKNOWN' || job.targetAckPending"
      class="secondary"
      :disabled="busy"
      @click="emit('jobAction', 'reconcile')"
    >
      检查操作结果</button
    ><button
      v-if="job.status === 'QUEUED'"
      class="secondary"
      :disabled="busy"
      @click="emit('jobAction', 'cancel')"
    >
      取消排队
    </button>
    <details>
      <summary>诊断信息</summary>
      <ErrorHelpLink v-if="job.errorCode" :code="job.errorCode" />
      <p>
        {{ softwareJobDiagnostic(job.status, job.phase) }}
        <span v-if="job.errorCode"> · {{ job.errorCode }}</span>
        · {{ hostTime(job.updatedAt) }}
      </p>
      <code>{{ job.id }} · {{ job.traceId }}</code>
    </details>
  </div>
</template>
