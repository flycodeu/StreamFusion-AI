<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  ElButton,
  ElDrawer,
  ElInput,
  ElPagination,
  ElRadioButton,
  ElRadioGroup,
  ElTable,
  ElTableColumn,
  ElTag,
  vLoading,
} from 'element-plus'
import * as api from '../../api/access-query/api'
import type {
  PageOverview,
  PageRole,
  PageUser,
  QueryResult,
  RoleOverview,
  RolePage,
  UserIdentity,
} from '../../api/access-query/types'
import { eligibilityText, reasonText } from '../../api/access-query/types'
import RequestError from '../../components/feedback/RequestError.vue'
import TablePanel from '../../components/table/TablePanel.vue'
import { usePageScope } from '../../composables/usePageScope'
import { ApiRequestError } from '../../lib/http/error'
import { sessionState } from '../../session/state'

defineOptions({ name: 'SystemAccessQuery' })
const captureScope = usePageScope()
const mode = ref<'role' | 'page'>('role')
const keyword = ref('')
const appliedKeyword = ref('')
const current = ref(1)
const size = ref(20)
const total = ref(0)
const loading = ref(false)
const error = ref<unknown>(null)
const pageReady = ref(false)
const exporting = ref(false)
const exportError = ref<unknown>(null)
const exportAdvice = computed(() =>
  exportError.value instanceof ApiRequestError && exportError.value.code === 'PAYLOAD_TOO_LARGE'
    ? '导出范围过大，请缩小页面搜索条件后重试。'
    : undefined,
)
const roles = ref<RoleOverview[]>([])
const pages = ref<PageOverview[]>([])
const drawer = ref(false)
const detailRole = ref<RoleOverview | null>(null)
const detailPage = ref<PageOverview | null>(null)
const detailTab = ref<'users' | 'pages' | 'roles'>('users')
const detailCurrent = ref(1)
const detailTotal = ref(0)
const detailLoading = ref(false)
const detailError = ref<unknown>(null)
const roleUsers = ref<UserIdentity[]>([])
const rolePages = ref<RolePage[]>([])
const pageUsers = ref<PageUser[]>([])
const pageRoles = ref<PageRole[]>([])
let listSequence = 0
let detailSequence = 0
let listAbort: globalThis.AbortController | null = null
let detailAbort: globalThis.AbortController | null = null
let exportAbort: globalThis.AbortController | null = null
let exportSequence = 0
const asRole = (value: unknown) => value as RoleOverview
const asPage = (value: unknown) => value as PageOverview
const asRolePage = (value: unknown) => value as RolePage
const asPageUser = (value: unknown) => value as PageUser
const asPageRole = (value: unknown) => value as PageRole
const asUser = (value: unknown) => value as UserIdentity

async function load(): Promise<void> {
  const sequence = ++listSequence
  const valid = captureScope()
  listAbort?.abort()
  listAbort = new globalThis.AbortController()
  loading.value = true
  error.value = null
  pageReady.value = false
  try {
    const query = { page: current.value, size: size.value, keyword: appliedKeyword.value }
    if (mode.value === 'role') {
      const result = await api.getRoleOverview(query, listAbort.signal)
      if (sequence !== listSequence || !valid()) return
      roles.value = result.items
      pages.value = []
      pageReady.value = false
      total.value = result.total
    } else {
      const result = await api.getPageOverview(query, listAbort.signal)
      if (sequence !== listSequence || !valid()) return
      pages.value = result.items
      roles.value = []
      pageReady.value = true
      total.value = result.total
    }
  } catch (cause) {
    if (sequence === listSequence && valid()) error.value = cause
  } finally {
    if (sequence === listSequence && valid()) loading.value = false
  }
}
function search(): void {
  exportAbort?.abort()
  ++exportSequence
  exporting.value = false
  exportError.value = null
  current.value = 1
  appliedKeyword.value = keyword.value.trim()
  roles.value = []
  pages.value = []
  total.value = 0
  void load()
}
function resize(): void {
  current.value = 1
  void load()
}
async function exportPages(): Promise<void> {
  if (mode.value !== 'page' || !pageReady.value || exporting.value) return
  const sequence = ++exportSequence
  const valid = captureScope()
  exportAbort?.abort()
  exportAbort = new globalThis.AbortController()
  exporting.value = true
  exportError.value = null
  try {
    const file = await api.getPageExcel(appliedKeyword.value, exportAbort.signal)
    if (sequence !== exportSequence || !valid() || mode.value !== 'page') return
    const url = globalThis.URL.createObjectURL(file.blob)
    const anchor = globalThis.document.createElement('a')
    anchor.href = url
    anchor.download = file.name
    anchor.click()
    globalThis.setTimeout(() => globalThis.URL.revokeObjectURL(url), 10000)
  } catch (cause) {
    if (sequence === exportSequence && valid()) exportError.value = cause
  } finally {
    if (sequence === exportSequence && valid()) exporting.value = false
  }
}
function openRole(role: RoleOverview, tab: 'users' | 'pages'): void {
  detailRole.value = role
  detailPage.value = null
  detailTab.value = tab
  detailCurrent.value = 1
  drawer.value = true
  void loadDetail()
}
function openPage(page: PageOverview, tab: 'users' | 'roles'): void {
  detailPage.value = page
  detailRole.value = null
  detailTab.value = tab
  detailCurrent.value = 1
  drawer.value = true
  void loadDetail()
}
function changeDetailTab(): void {
  detailCurrent.value = 1
  void loadDetail()
}
function closeDetail(): void {
  detailAbort?.abort()
  ++detailSequence
  detailLoading.value = false
  detailError.value = null
}
async function loadDetail(): Promise<void> {
  const sequence = ++detailSequence
  const valid = captureScope()
  detailAbort?.abort()
  detailAbort = new globalThis.AbortController()
  detailLoading.value = true
  detailError.value = null
  try {
    const query = { page: detailCurrent.value, size: 20 }
    let result: QueryResult<unknown>
    if (detailRole.value) {
      if (detailTab.value === 'users') {
        const data = await api.getRoleUsers(detailRole.value.role.id, query, detailAbort.signal)
        if (sequence !== detailSequence || !valid()) return
        roleUsers.value = data.items
        result = data
      } else {
        const data = await api.getRolePages(detailRole.value.role.id, query, detailAbort.signal)
        if (sequence !== detailSequence || !valid()) return
        rolePages.value = data.items
        result = data
      }
    } else if (detailPage.value) {
      if (detailTab.value === 'users') {
        const data = await api.getPageUsers(
          detailPage.value.menu.id,
          { ...query, includeInactive: true },
          detailAbort.signal,
        )
        if (sequence !== detailSequence || !valid()) return
        pageUsers.value = data.items
        result = data
      } else {
        const data = await api.getPageRoles(
          detailPage.value.menu.id,
          { ...query, includeInactive: true },
          detailAbort.signal,
        )
        if (sequence !== detailSequence || !valid()) return
        pageRoles.value = data.items
        result = data
      }
    } else return
    detailTotal.value = result.total
  } catch (cause) {
    if (sequence === detailSequence && valid()) detailError.value = cause
  } finally {
    if (sequence === detailSequence && valid()) detailLoading.value = false
  }
}
watch(mode, () => {
  exportAbort?.abort()
  ++exportSequence
  exporting.value = false
  exportError.value = null
  listAbort?.abort()
  ++listSequence
  roles.value = []
  pages.value = []
  pageReady.value = false
  total.value = 0
  keyword.value = ''
  appliedKeyword.value = ''
  current.value = 1
  drawer.value = false
  closeDetail()
  void load()
})
watch(
  () => [sessionState.me?.user.id, sessionState.epoch],
  () => {
    exportAbort?.abort()
    ++exportSequence
    exporting.value = false
    exportError.value = null
    listAbort?.abort()
    ++listSequence
    loading.value = false
    error.value = null
    roles.value = []
    pages.value = []
    pageReady.value = false
    keyword.value = ''
    appliedKeyword.value = ''
    total.value = 0
    drawer.value = false
    detailRole.value = null
    detailPage.value = null
    roleUsers.value = []
    rolePages.value = []
    pageUsers.value = []
    pageRoles.value = []
    detailTotal.value = 0
    closeDetail()
  },
  { flush: 'sync' },
)
onMounted(() => void load())
onBeforeUnmount(() => {
  listAbort?.abort()
  detailAbort?.abort()
  exportAbort?.abort()
})
</script>

<template>
  <div class="content-page access-query">
    <header class="query-heading">
      <div>
        <h2>权限查询</h2>
      </div>
      <ElTag effect="plain" type="info">只读</ElTag>
    </header>
    <div class="query-toolbar">
      <ElRadioGroup v-model="mode" aria-label="权限查询视角">
        <ElRadioButton value="role">按角色查看</ElRadioButton>
        <ElRadioButton value="page">按页面查看</ElRadioButton>
      </ElRadioGroup>
      <ElInput
        v-model="keyword"
        class="query-search"
        maxlength="64"
        clearable
        :placeholder="mode === 'role' ? '搜索角色名称或编码' : '搜索页面名称或路径'"
        @keyup.enter="search"
        @clear="search"
      />
      <ElButton type="primary" :loading="loading" @click="search">查询</ElButton>
      <ElButton :loading="loading" @click="load">刷新</ElButton>
      <ElButton
        v-if="mode === 'page'"
        :loading="exporting"
        :disabled="!pageReady || loading"
        title="导出当前结果范围的全部页面，不限当前页"
        @click="exportPages"
        >导出当前结果（Excel）</ElButton
      >
    </div>
    <RequestError :error="error" />
    <RequestError :error="exportError" :advice="exportAdvice" />
    <TablePanel>
      <template #actions
        ><span class="table-context">{{
          mode === 'role' ? '角色授权概览' : '页面覆盖概览'
        }}</span></template
      >
      <ElTable
        v-if="mode === 'role'"
        v-loading="loading"
        :data="roles"
        border
        row-key="role.id"
        empty-text="暂无角色"
      >
        <ElTableColumn label="角色" min-width="200"
          ><template #default="{ row }">
            <div class="primary-cell">{{ asRole(row).role.name }}</div>
            <small>{{ asRole(row).role.code }}</small>
            <ElTag
              v-if="asRole(row).role.status !== 'ENABLED'"
              class="status-tag"
              type="warning"
              size="small"
              >已停用</ElTag
            >
          </template></ElTableColumn
        >
        <ElTableColumn label="关联账号" min-width="140"
          ><template #default="{ row }">
            <ElButton link type="primary" @click="openRole(asRole(row), 'users')"
              >{{ asRole(row).assignedUserCount }} 人</ElButton
            >
            <small>其中 {{ asRole(row).usableUserCount }} 人当前可用</small>
          </template></ElTableColumn
        >
        <ElTableColumn label="授权页面" min-width="150"
          ><template #default="{ row }">
            <ElButton link type="primary" @click="openRole(asRole(row), 'pages')"
              >{{ asRole(row).assignedPageCount }} 个</ElButton
            >
            <small>其中 {{ asRole(row).effectivePageCount }} 个当前有效</small>
          </template></ElTableColumn
        >
      </ElTable>
      <ElTable
        v-else
        v-loading="loading"
        :data="pages"
        border
        row-key="menu.id"
        empty-text="暂无页面"
      >
        <ElTableColumn label="页面" min-width="250"
          ><template #default="{ row }">
            <div class="primary-cell">{{ asPage(row).menu.menuPath }}</div>
            <small>{{ asPage(row).menu.path }}</small>
            <ElTag
              v-if="!asPage(row).menu.chainEnabled"
              class="status-tag"
              type="warning"
              size="small"
              >页面不可用</ElTag
            >
          </template></ElTableColumn
        >
        <ElTableColumn label="授权角色" min-width="140"
          ><template #default="{ row }">
            <ElButton link type="primary" @click="openPage(asPage(row), 'roles')"
              >{{ asPage(row).assignedRoleCount }} 个</ElButton
            >
            <small>其中 {{ asPage(row).activeRoleCount }} 个当前有效</small>
          </template></ElTableColumn
        >
        <ElTableColumn label="覆盖账号" min-width="140"
          ><template #default="{ row }">
            <ElButton link type="primary" @click="openPage(asPage(row), 'users')"
              >{{ asPage(row).assignedUserCount }} 人</ElButton
            >
            <small>其中 {{ asPage(row).eligibleUserCount }} 人当前具备资格</small>
          </template></ElTableColumn
        >
      </ElTable>
      <template #footer
        ><ElPagination
          v-model:current-page="current"
          v-model:page-size="size"
          :page-sizes="[20, 50, 100]"
          :total="total"
          layout="total, sizes, prev, pager, next"
          @current-change="load"
          @size-change="resize"
      /></template>
    </TablePanel>
    <p class="query-note">
      统计当前授权配置，不是访问行为记录。<span v-if="mode === 'page'"
        >导出包含当前结果的全部页面及具备资格的账号，不限当前页。</span
      >
    </p>
    <ElDrawer
      v-model="drawer"
      :title="
        detailRole
          ? `${detailRole.role.name} · 角色明细`
          : `${detailPage?.menu.menuPath ?? ''} · 页面明细`
      "
      size="min(760px, 100vw)"
      @closed="closeDetail"
    >
      <div class="detail-heading">
        <ElRadioGroup
          v-model="detailTab"
          size="small"
          aria-label="明细类型"
          @change="changeDetailTab"
        >
          <ElRadioButton value="users">账号</ElRadioButton>
          <ElRadioButton v-if="detailRole" value="pages">页面</ElRadioButton>
          <ElRadioButton v-if="detailPage" value="roles">角色</ElRadioButton>
        </ElRadioGroup>
      </div>
      <RequestError :error="detailError" />
      <ElButton v-if="detailError" :loading="detailLoading" @click="loadDetail">重试</ElButton>
      <ElTable
        v-if="detailRole && detailTab === 'users'"
        v-loading="detailLoading"
        :data="roleUsers"
        border
        empty-text="没有关联账号"
      >
        <ElTableColumn label="账号 / 昵称" min-width="200"
          ><template #default="{ row }"
            ><div>{{ asUser(row).username }}</div>
            <small>{{ asUser(row).nickname || '—' }}</small></template
          ></ElTableColumn
        >
        <ElTableColumn label="账号状态" min-width="140"
          ><template #default="{ row }">{{
            detailRole?.role.status !== 'ENABLED'
              ? '角色已停用'
              : asUser(row).accountUsable
                ? '当前可用'
                : asUser(row).status === 2
                  ? '已停用'
                  : '需先改密'
          }}</template></ElTableColumn
        >
        <ElTableColumn label="部门" min-width="160"
          ><template #default="{ row }">{{
            asUser(row)
              .departments.map((department) => department.name)
              .join('、') || '—'
          }}</template></ElTableColumn
        >
      </ElTable>
      <ElTable
        v-else-if="detailRole && detailTab === 'pages'"
        v-loading="detailLoading"
        :data="rolePages"
        border
        empty-text="没有获授页面"
      >
        <ElTableColumn label="页面" min-width="220"
          ><template #default="{ row }"
            ><div>{{ asRolePage(row).menu.menuPath }}</div>
            <small>{{ asRolePage(row).menu.path }}</small></template
          ></ElTableColumn
        >
        <ElTableColumn label="当前授予" min-width="150"
          ><template #default="{ row }">{{
            asRolePage(row).grantEffective
              ? '有效'
              : asRolePage(row).blockers.map(reasonText).join('；')
          }}</template></ElTableColumn
        >
      </ElTable>
      <ElTable
        v-else-if="detailPage && detailTab === 'users'"
        v-loading="detailLoading"
        :data="pageUsers"
        border
        empty-text="没有获授账号"
      >
        <ElTableColumn label="账号 / 昵称" min-width="170"
          ><template #default="{ row }"
            ><div>{{ asPageUser(row).user.username }}</div>
            <small>{{ asPageUser(row).user.nickname || '—' }}</small></template
          ></ElTableColumn
        >
        <ElTableColumn label="当前资格" min-width="120"
          ><template #default="{ row }">{{
            eligibilityText(
              asPageUser(row).grantEffective,
              asPageUser(row).accessEligible,
              asPageUser(row).sourceCount,
            )
          }}</template></ElTableColumn
        >
        <ElTableColumn label="部门" min-width="160"
          ><template #default="{ row }">{{
            asPageUser(row)
              .user.departments.map((department) => department.name)
              .join('、') || '—'
          }}</template></ElTableColumn
        >
        <ElTableColumn label="授予角色" min-width="180"
          ><template #default="{ row }"
            >{{
              asPageUser(row)
                .sourcePreview.map((source) => source.name)
                .join('、')
            }}<span v-if="asPageUser(row).sourceCount > asPageUser(row).sourcePreview.length">
              等 {{ asPageUser(row).sourceCount }} 个</span
            ></template
          ></ElTableColumn
        >
      </ElTable>
      <ElTable
        v-else-if="detailPage"
        v-loading="detailLoading"
        :data="pageRoles"
        border
        empty-text="没有获授角色"
      >
        <ElTableColumn label="角色" min-width="170"
          ><template #default="{ row }"
            ><div>{{ asPageRole(row).role.name }}</div>
            <small>{{ asPageRole(row).role.code }}</small></template
          ></ElTableColumn
        >
        <ElTableColumn label="关联用户" min-width="110"
          ><template #default="{ row }">{{ asPageRole(row).userCount }} 人</template></ElTableColumn
        >
        <ElTableColumn label="当前授予" min-width="120"
          ><template #default="{ row }">{{
            asPageRole(row).grantEffective
              ? '有效'
              : asPageRole(row).blockers.map(reasonText).join('；')
          }}</template></ElTableColumn
        >
      </ElTable>
      <ElPagination
        v-model:current-page="detailCurrent"
        :page-size="20"
        :total="detailTotal"
        layout="total, prev, next"
        class="detail-pagination"
        @current-change="loadDetail"
      />
    </ElDrawer>
  </div>
</template>

<style scoped>
.query-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 20px;
}
.query-heading h2 {
  margin: 0 0 6px;
  font-size: 21px;
  font-weight: 600;
  letter-spacing: -0.02em;
}
.query-toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 16px;
}
.query-search {
  width: min(320px, 100%);
}
.table-context {
  font-size: 13px;
  font-weight: 600;
}
.primary-cell {
  font-weight: 500;
}
small {
  display: block;
  color: var(--el-text-color-secondary);
  font-size: 12px;
  line-height: 1.6;
}
.status-tag {
  margin-left: 8px;
}
.query-note {
  color: var(--el-text-color-secondary);
  font-size: 12px;
  line-height: 1.7;
  margin-top: 12px;
}
.detail-heading {
  display: grid;
  gap: 12px;
  margin-bottom: 16px;
}
.detail-pagination {
  margin-top: 14px;
}
@media (max-width: 640px) {
  .query-search {
    width: 100%;
  }
  .query-heading {
    align-items: center;
  }
}
</style>
