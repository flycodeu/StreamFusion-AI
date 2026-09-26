<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import {
  ElButton,
  ElCard,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessage,
  ElSelect,
  ElOption,
  ElTag,
  ElTabs,
  ElTabPane,
} from 'element-plus'
import { saveProfile } from '../../api/auth/api'
import { sessionState, setIdentity } from '../../session/state'
import type { UserProfile } from '../../api/auth/types'
import RequestError from '../../components/feedback/RequestError.vue'
import UserAvatar from '../../components/avatar/UserAvatar.vue'
import AvatarPicker from '../../features/account/AvatarPicker.vue'
import PasswordForm from '../../features/account/PasswordForm.vue'
import LoginRecordTable from '../../features/login-records/LoginRecordTable.vue'

const route = useRoute()
function selectedTab(tab: unknown): string {
  return tab === 'password' || tab === 'logins' ? tab : 'profile'
}
const activeTab = ref(selectedTab(route.query.tab))
watch(
  () => route.query.tab,
  (tab) => {
    activeTab.value = selectedTab(tab)
  },
)
const form = reactive<UserProfile>({ ...(sessionState.me?.user as UserProfile) })
const busy = ref(false)
const error = ref<unknown>(null)
const submitted = ref(false)
const displayName = computed(() => sessionState.me?.user.nickname || form.username)
const phoneError = computed(() =>
  form.phone?.trim() && !/^[+]?[0-9]{7,20}$/.test(form.phone.trim())
    ? '请输入 7～20 位号码，可在开头使用 +'
    : '',
)
const emailError = computed(() =>
  form.email?.trim() &&
  (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(form.email.trim()) || form.email.trim().length > 254)
    ? '请输入有效的邮箱地址'
    : '',
)
watch(
  () => sessionState.me?.user,
  (user) => {
    if (user) Object.assign(form, user)
  },
)
function reset() {
  if (sessionState.me) Object.assign(form, sessionState.me.user)
  submitted.value = false
  error.value = null
}
async function save(): Promise<void> {
  submitted.value = true
  if (busy.value || phoneError.value || emailError.value) return
  busy.value = true
  error.value = null
  const epoch = sessionState.epoch
  try {
    const user = await saveProfile(form)
    if (sessionState.epoch !== epoch || sessionState.me?.user.id !== user.id) return
    setIdentity({ ...sessionState.me, user })
    Object.assign(form, user)
    ElMessage.success('个人信息已保存')
  } catch (cause) {
    error.value = cause
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="content-page profile-layout">
    <ElCard shadow="never" class="identity-card">
      <div class="identity-heading">
        <UserAvatar :avatar-key="sessionState.me?.user.avatarKey" :name="displayName" :size="88" />
        <h2>{{ displayName }}</h2>
        <span class="account-name">{{ form.username }}</span>
      </div>
      <dl class="identity-facts">
        <div>
          <dt>账号状态</dt>
          <dd>
            <ElTag :type="form.status === 1 ? 'success' : 'info'" size="small">{{
              form.status === 1 ? '正常' : form.status === 0 ? '待改密' : '停用'
            }}</ElTag>
          </dd>
        </div>
        <div>
          <dt>所属角色</dt>
          <dd class="role-tags">
            <ElTag v-for="role in sessionState.me?.roles" :key="role.id" size="small" type="info">{{
              role.name
            }}</ElTag
            ><span v-if="!sessionState.me?.roles.length">未分配</span>
          </dd>
        </div>
      </dl>
    </ElCard>
    <ElCard shadow="never" class="profile-card">
      <ElTabs v-model="activeTab">
        <ElTabPane label="基本资料" name="profile">
          <RequestError :error="error" />
          <ElForm label-width="80px" :disabled="busy" @submit.prevent="save">
            <ElFormItem label="头像" class="profile-avatar"
              ><AvatarPicker v-model="form.avatarKey" :name="displayName"
            /></ElFormItem>
            <div class="profile-fields">
              <ElFormItem label="昵称"
                ><ElInput v-model="form.nickname" maxlength="64" placeholder="请输入昵称"
              /></ElFormItem>
              <ElFormItem label="性别"
                ><ElSelect v-model="form.gender"
                  ><ElOption label="未设置" :value="0" /><ElOption label="男" :value="1" /><ElOption
                    label="女"
                    :value="2" /></ElSelect
              ></ElFormItem>
              <ElFormItem label="电话" :error="submitted ? phoneError : ''"
                ><ElInput
                  v-model="form.phone"
                  autocomplete="tel"
                  placeholder="请输入联系电话"
                  maxlength="21"
              /></ElFormItem>
              <ElFormItem label="邮箱" :error="submitted ? emailError : ''"
                ><ElInput
                  v-model="form.email"
                  autocomplete="email"
                  placeholder="请输入邮箱地址"
                  maxlength="254"
              /></ElFormItem>
            </div>
            <ElFormItem class="profile-actions"
              ><ElButton type="primary" native-type="submit" :loading="busy">保存修改</ElButton
              ><ElButton :disabled="busy" @click="reset">重置</ElButton></ElFormItem
            >
          </ElForm>
        </ElTabPane>
        <ElTabPane label="修改密码" name="password"
          ><PasswordForm v-if="activeTab === 'password'"
        /></ElTabPane>
        <ElTabPane label="登录记录" name="logins"
          ><LoginRecordTable v-if="activeTab === 'logins'"
        /></ElTabPane>
      </ElTabs>
    </ElCard>
  </div>
</template>

<style scoped>
.profile-layout {
  display: grid;
  grid-template-columns: 280px minmax(0, 1fr);
  gap: 20px;
  align-items: start;
}
.identity-card {
  position: sticky;
  top: 20px;
}
.profile-card {
  min-height: 470px;
}
.profile-card :deep(.el-tabs__header) {
  margin-bottom: 28px;
}
.profile-card :deep(.el-tabs__item) {
  font-size: 15px;
}
.profile-avatar {
  margin-bottom: 30px;
}
.identity-heading {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  padding: 10px 0 24px;
}
.identity-heading h2 {
  margin: 6px 0 0;
  font-size: 19px;
  font-weight: 600;
  overflow-wrap: anywhere;
}
.account-name {
  color: var(--el-text-color-secondary);
  font-size: 14px;
}
.identity-facts {
  margin: 0 0 22px;
  font-size: 14px;
}
.identity-facts > div {
  display: grid;
  grid-template-columns: 72px 1fr;
  gap: 12px;
  padding: 14px 0;
  border-top: 1px solid var(--el-border-color-lighter);
}
.identity-facts dt {
  color: var(--el-text-color-secondary);
}
.identity-facts dd {
  margin: 0;
}
.role-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.profile-card :deep(.el-card__body) {
  padding: 28px 24px;
}
.profile-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px 24px;
}
.profile-fields :deep(.el-form-item) {
  margin-bottom: 24px;
}
.profile-actions {
  margin-top: 14px;
  margin-bottom: 0;
}
@media (max-width: 1100px) {
  .profile-fields {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 760px) {
  .profile-layout {
    grid-template-columns: 1fr;
    gap: 16px;
  }
  .identity-card {
    position: static;
  }
  .profile-card :deep(.el-card__body) {
    padding: 20px 14px;
  }
}
</style>
