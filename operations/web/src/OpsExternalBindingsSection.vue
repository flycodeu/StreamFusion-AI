<script setup lang="ts">
import OpsPagination from "./OpsPagination.vue";
import type { Host } from "./hosts";

export interface WorkspaceBinding {
  id: string;
  kind: string;
  host: string;
  port: number;
  database: string;
  username: string;
  credentialId: string;
  credentialRevision: string;
}

export interface WorkspaceProbeLayer {
  layer: string;
  executed: boolean;
  reachable: boolean;
  authenticated: boolean;
  errorCode?: string;
}

export interface WorkspaceBindingProbeResult {
  controller: WorkspaceProbeLayer;
  targetHost: WorkspaceProbeLayer;
  applicationNetwork: WorkspaceProbeLayer | null;
  authentication: WorkspaceProbeLayer | null;
  errorCode: string;
  observedAt: string;
  releaseId?: string;
  credentialRevision?: string;
}

export interface WorkspaceBindingInstanceOption {
  id: string;
  name: string;
  targetId: string;
  currentReleaseId: string;
  config: { preview?: unknown; zlm?: unknown };
}

defineProps<{
  busy: boolean;
  bindings: WorkspaceBinding[];
  targets: Host[];
  instanceOptions: WorkspaceBindingInstanceOption[];
  bindingProbeTargets: Record<string, string>;
  bindingProbeInstances: Record<string, string>;
  bindingProbeResults: Record<string, WorkspaceBindingProbeResult>;
  bindingPage: number;
  bindingPageSize: number;
  bindingTotal: number;
  bindingsLoading: boolean;
  time: (value: string) => string;
}>();

const emit = defineEmits<{
  toggle: [event: Event];
  openCreateBinding: [];
  probe: [binding: WorkspaceBinding];
  changePage: [page: number];
  changeSize: [size: number];
}>();
</script>

<template>
  <details class="dependency-section" @toggle="emit('toggle', $event)">
    <summary>外部依赖连接</summary>
    <div class="section-head">
      <h2>外部依赖</h2>
      <button
        class="secondary"
        :disabled="busy"
        @click="emit('openCreateBinding')"
      >
        绑定已有服务
      </button>
    </div>
    <div v-if="!bindings.length" class="empty">尚未登记外部依赖</div>
    <div v-else class="table-scroll">
      <table>
        <thead>
          <tr>
            <th>类型</th>
            <th>地址</th>
            <th>数据库</th>
            <th>检查来源</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="b in bindings" :key="b.id">
            <td>{{ b.kind }}</td>
            <td>{{ b.host }}:{{ b.port }}</td>
            <td>{{ b.database || "—" }}</td>
            <td>
              <div class="probe-controls">
                <select
                  v-model="bindingProbeTargets[b.id]"
                  :aria-label="`检查来源：${b.host}`"
                  @change="
                    bindingProbeInstances[b.id] = '';
                    delete bindingProbeResults[b.id];
                  "
                >
                  <option value="">选择主机</option>
                  <option v-for="t in targets" :key="t.id" :value="t.id">
                    {{ t.name }} · {{ t.address }}
                  </option>
                </select>
                <select
                  v-model="bindingProbeInstances[b.id]"
                  :aria-label="`实例网络：${b.host}`"
                  @change="delete bindingProbeResults[b.id]"
                >
                  <option value="">仅检查主机 TCP</option>
                  <option
                    v-for="i in instanceOptions.filter(
                      (item) =>
                        item.targetId === bindingProbeTargets[b.id] &&
                        item.currentReleaseId &&
                        !item.config.preview &&
                        !item.config.zlm,
                    )"
                    :key="i.id"
                    :value="i.id"
                  >
                    {{ i.name }} · 当前已发布版本
                  </option>
                </select>
              </div>
            </td>
            <td>
              <button
                class="quiet"
                :disabled="busy || !bindingProbeTargets[b.id]"
                @click="emit('probe', b)"
              >
                {{
                  bindingProbeInstances[b.id] ? "检查实例连接" : "TCP 检查"
                }}
              </button>
              <details
                v-if="bindingProbeResults[b.id]"
                class="probe-result"
              >
                <summary>
                  {{
                    bindingProbeResults[b.id]?.authentication?.authenticated
                      ? "认证通过"
                      : bindingProbeResults[b.id]?.authentication?.errorCode ||
                        bindingProbeResults[b.id]?.errorCode ||
                        "查看检查结果"
                  }}
                </summary>
                <dl>
                  <dt>控制器 TCP</dt>
                  <dd>
                    {{
                      bindingProbeResults[b.id]?.controller.reachable
                        ? "可达"
                        : "不可达"
                    }}
                  </dd>
                  <dt>目标主机 TCP</dt>
                  <dd>
                    {{
                      !bindingProbeResults[b.id]?.targetHost.executed
                        ? "未执行"
                        : bindingProbeResults[b.id]?.targetHost.reachable
                          ? "可达"
                          : "不可达"
                    }}
                  </dd>
                  <dt>实例网络</dt>
                  <dd>
                    {{
                      !bindingProbeResults[b.id]?.applicationNetwork
                        ? "未验证"
                        : bindingProbeResults[b.id]?.applicationNetwork
                              ?.reachable
                          ? "可达"
                          : "不可达"
                    }}
                  </dd>
                  <dt>最小认证</dt>
                  <dd>
                    {{
                      !bindingProbeResults[b.id]?.authentication?.executed
                        ? "未验证"
                        : bindingProbeResults[b.id]?.authentication
                              ?.authenticated
                          ? "通过"
                          : bindingProbeResults[b.id]?.authentication
                              ?.errorCode || "未通过"
                    }}
                  </dd>
                  <template v-if="bindingProbeResults[b.id]?.releaseId">
                    <dt>冻结版本</dt>
                    <dd>
                      <code>{{ bindingProbeResults[b.id]?.releaseId }}</code>
                    </dd>
                  </template>
                  <dt>检查时间</dt>
                  <dd>{{ time(bindingProbeResults[b.id]?.observedAt || "") }}</dd>
                </dl>
                <small
                  v-if="
                    bindingProbeResults[b.id]?.authentication?.errorCode ===
                    'AUTH_TLS_REQUIRED'
                  "
                  >该 MySQL 账号需要 TLS
                  full-auth，当前探针未支持；没有发送明文密码，结果不代表密码错误或数据库不可用。</small
                >
                <small
                  v-else-if="
                    bindingProbeResults[b.id]?.errorCode ||
                    bindingProbeResults[b.id]?.authentication?.errorCode
                  "
                  >核对目标执行器能力、实例当前版本与原作业/ACK；认证错误请检查所选凭据修订，不要以
                  TCP 可达判断密码正确。</small
                >
              </details>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <OpsPagination
      :page="bindingPage"
      :page-size="bindingPageSize"
      :total="bindingTotal"
      :disabled="bindingsLoading"
      label="外部依赖分页"
      @update:page="emit('changePage', $event)"
      @update:page-size="emit('changeSize', $event)"
    />
  </details>
</template>
