<script setup lang="ts">
import { ref } from 'vue'

defineProps<{ displayName?: string }>()
const collapsed = ref(false)
</script>

<template>
  <div class="admin-layout" :class="{ 'is-collapsed': collapsed }">
    <a class="skip-link" href="#main-content">跳转到内容</a>
    <aside id="primary-sidebar" class="sidebar" aria-label="主导航">
      <a class="brand" href="#main-content" aria-label="StreamFusion AI 首页">
        <span class="brand-mark" aria-hidden="true">SF</span>
        <span class="brand-name">StreamFusion <small>AI PLATFORM</small></span>
      </a>
      <nav class="navigation" aria-label="功能菜单">
        <a class="nav-item is-active" href="#main-content" aria-current="page" title="系统概览">
          <svg
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.6"
            aria-hidden="true"
          >
            <rect x="3" y="3" width="7" height="7" rx="1.5" />
            <rect x="14" y="3" width="7" height="7" rx="1.5" />
            <rect x="3" y="14" width="7" height="7" rx="1.5" />
            <rect x="14" y="14" width="7" height="7" rx="1.5" />
          </svg>
          <span class="nav-label">系统概览</span>
        </a>
      </nav>
      <span class="sidebar-caption">STREAMFUSION AI</span>
    </aside>
    <div class="workspace">
      <header class="topbar">
        <button
          class="icon-button"
          type="button"
          :aria-label="collapsed ? '展开菜单' : '收起菜单'"
          :aria-expanded="!collapsed"
          aria-controls="primary-sidebar"
          @click="collapsed = !collapsed"
        >
          <svg
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.6"
            aria-hidden="true"
          >
            <rect x="3" y="4" width="18" height="16" rx="2" />
            <path d="M9 4v16M13 9h4M13 13h4" />
          </svg>
        </button>
        <span class="breadcrumb"
          >工作台 <span aria-hidden="true">/</span> <strong>系统概览</strong></span
        >
        <div class="account" aria-label="当前用户">
          <span class="avatar" aria-hidden="true">
            <template v-if="displayName">{{ Array.from(displayName)[0] }}</template>
            <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6">
              <circle cx="12" cy="8" r="3" />
              <path d="M5 20v-2a7 7 0 0 1 14 0v2" />
            </svg>
          </span>
          <span>{{ displayName || '未登录' }}</span>
        </div>
      </header>
      <main id="main-content" class="main-content" tabindex="-1">
        <slot />
      </main>
    </div>
  </div>
</template>
