<script setup lang="ts">
const props = defineProps<{
  username: string;
  password: string;
  busy: boolean;
  loginError: string;
  showLoginPassword: boolean;
  loginCapsLock: boolean;
}>();

const emit = defineEmits<{
  "update:username": [value: string];
  "update:password": [value: string];
  "update:showLoginPassword": [value: boolean];
  "update:loginCapsLock": [value: boolean];
  clearError: [];
  submit: [];
}>();

const opsHighlights = [
  "主机与环境纳管",
  "软件与服务编排",
  "端口与安全基线",
] as const;

function checkLoginCapsLock(event: Event) {
  if (
    typeof KeyboardEvent !== "undefined" &&
    event instanceof KeyboardEvent &&
    typeof event.getModifierState === "function"
  ) {
    emit("update:loginCapsLock", event.getModifierState("CapsLock"));
  }
}

function onUsernameInput(event: Event) {
  emit("update:username", (event.target as HTMLInputElement).value);
  emit("clearError");
}

function onPasswordInput(event: Event) {
  emit("update:password", (event.target as HTMLInputElement).value);
  emit("clearError");
}
</script>

<template>
  <div class="ops-login-layout">
    <section class="ops-login-visual" aria-label="运维控制台概览">
      <div class="ops-visual-brand">
        <img
          class="brand-logo"
          src="/streamfusion.svg"
          alt=""
          width="38"
          height="38"
        />
        <span>StreamFusion Ops</span>
      </div>
      <div class="ops-visual-body">
        <div class="ops-illustration" aria-hidden="true">
          <svg viewBox="0 0 560 330" fill="none">
            <path
              d="M142 92H232Q260 92 260 124V162M418 92H328Q300 92 300 124V162M142 248H232Q260 248 260 216V182M418 248H328Q300 248 300 216V182"
              stroke="#93C5FD"
              stroke-width="3"
              stroke-dasharray="6 5"
            />
            <rect
              x="32"
              y="36"
              width="150"
              height="102"
              rx="12"
              fill="#FFFFFF"
              stroke="#BFDBFE"
              stroke-width="2"
            />
            <rect
              x="48"
              y="52"
              width="118"
              height="26"
              rx="6"
              fill="#EFF6FF"
            />
            <circle cx="62" cy="65" r="5" fill="#2563EB" />
            <path
              d="M76 65h66M50 96h88M50 114h62"
              stroke="#93C5FD"
              stroke-width="4"
              stroke-linecap="round"
            />
            <rect
              x="378"
              y="36"
              width="150"
              height="102"
              rx="12"
              fill="#FFFFFF"
              stroke="#BFDBFE"
              stroke-width="2"
            />
            <rect
              x="396"
              y="54"
              width="32"
              height="32"
              rx="7"
              fill="#DBEAFE"
            />
            <rect
              x="438"
              y="54"
              width="32"
              height="32"
              rx="7"
              fill="#DBEAFE"
            />
            <rect
              x="480"
              y="54"
              width="32"
              height="32"
              rx="7"
              fill="#EFF6FF"
            />
            <path
              d="M398 108h104"
              stroke="#93C5FD"
              stroke-width="4"
              stroke-linecap="round"
            />
            <rect
              x="46"
              y="206"
              width="150"
              height="92"
              rx="12"
              fill="#FFFFFF"
              stroke="#BFDBFE"
              stroke-width="2"
            />
            <path
              d="M72 272v-24m28 24v-40m28 40v-18m28 18v-32"
              stroke="#3B82F6"
              stroke-width="11"
              stroke-linecap="round"
            />
            <rect
              x="364"
              y="206"
              width="150"
              height="92"
              rx="12"
              fill="#FFFFFF"
              stroke="#BFDBFE"
              stroke-width="2"
            />
            <path
              d="M388 232h20m12 0h68M388 252h20m12 0h52M388 272h20m12 0h68"
              stroke="#93C5FD"
              stroke-width="5"
              stroke-linecap="round"
            />
            <rect
              x="225"
              y="117"
              width="110"
              height="110"
              rx="26"
              fill="#FFFFFF"
              stroke="#93C5FD"
              stroke-width="2.5"
            />
            <image
              href="/streamfusion.svg"
              x="246"
              y="138"
              width="68"
              height="68"
            />
          </svg>
        </div>
        <h2>连接基础设施与服务编排</h2>
        <p class="ops-visual-subtitle">
          StreamFusion Ops · 运维与部署控制台
        </p>
        <div class="ops-visual-pills">
          <span v-for="item in opsHighlights" :key="item" class="ops-pill">{{
            item
          }}</span>
        </div>
      </div>
    </section>
    <section class="ops-login-region">
      <div class="login-card">
        <div class="login-intro">
          <div class="brand mobile-login-brand">
            <img
              class="brand-logo"
              src="/streamfusion.svg"
              alt=""
              width="34"
              height="34"
            /><span>StreamFusion Ops</span>
          </div>
          <h1>控制器登录</h1>
        </div>
        <form @submit.prevent="emit('submit')">
          <div class="login-field">
            <label for="ops-login-username">账号</label>
            <input
              id="ops-login-username"
              :value="props.username"
              autocomplete="username"
              placeholder="请输入账号"
              required
              maxlength="32"
              :disabled="busy"
              @input="onUsernameInput"
            />
          </div>
          <div class="login-field">
            <label for="ops-login-password">密码</label>
            <div class="password-input-wrap">
              <input
                id="ops-login-password"
                :value="props.password"
                :type="showLoginPassword ? 'text' : 'password'"
                autocomplete="current-password"
                placeholder="请输入密码"
                required
                maxlength="64"
                :disabled="busy"
                @input="onPasswordInput"
                @keydown="checkLoginCapsLock"
                @keyup="checkLoginCapsLock"
                @blur="emit('update:loginCapsLock', false)"
              />
              <button
                type="button"
                class="password-eye-btn"
                :aria-label="showLoginPassword ? '隐藏密码' : '显示密码'"
                :aria-pressed="showLoginPassword"
                @click="emit('update:showLoginPassword', !showLoginPassword)"
              >
                <svg
                  v-if="showLoginPassword"
                  viewBox="0 0 24 24"
                  aria-hidden="true"
                >
                  <path
                    d="M3 3l18 18M10.6 10.6a2 2 0 0 0 2.8 2.8M9.9 5.1A10.4 10.4 0 0 1 12 5c5 0 9.3 3.1 11 7-0.7 1.6-1.8 3.1-3.2 4.2M6.2 6.2C4.3 7.5 2.8 9.5 2 12c1.7 3.9 6 7 10 7 1.6 0 3.1-.4 4.5-1"
                  />
                </svg>
                <svg v-else viewBox="0 0 24 24" aria-hidden="true">
                  <path
                    d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7-10-7-10-7Z"
                  />
                  <circle cx="12" cy="12" r="3" />
                </svg>
              </button>
            </div>
            <div
              v-if="loginCapsLock"
              class="caps-lock-notice"
              role="status"
            >
              已开启大写锁定 (Caps Lock)
            </div>
          </div>
          <p v-if="loginError" class="error login-alert" role="alert">
            {{ loginError }}
          </p>
          <button class="primary login-submit-btn" :disabled="busy">
            {{ busy ? "请稍候" : "登录" }}
          </button>
        </form>
      </div>
    </section>
  </div>
</template>
