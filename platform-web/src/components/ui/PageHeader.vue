<script setup lang="ts">
import HelpTip from './HelpTip.vue'

withDefaults(
  defineProps<{
    title: string
    count?: string | number
    help?: string
    card?: boolean
    headingTag?: 'h1' | 'h2' | 'h3'
  }>(),
  {
    card: true,
    headingTag: 'h1',
  },
)
</script>

<template>
  <header class="sf-page-header" :class="{ 'sf-card': card, 'is-plain': !card }">
    <div class="sf-page-header__main">
      <slot name="prefix" />
      <component :is="headingTag" class="sf-page-header__title">{{ title }}</component>
      <span v-if="count !== undefined && count !== ''" class="sf-count-pill">{{ count }}</span>
      <HelpTip v-if="help" :content="help" :label="`${title}说明`" />
      <slot name="meta" />
    </div>
    <div v-if="$slots.actions" class="sf-page-header__actions">
      <slot name="actions" />
    </div>
  </header>
</template>

<style scoped>
.sf-page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px 16px;
  padding: 14px var(--sf-card-padding-x);
}

.sf-page-header.is-plain {
  padding: 0;
  background: transparent;
  border: 0;
  box-shadow: none;
}

.sf-page-header__main {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 10px;
  min-width: 0;
}

.sf-page-header__title {
  margin: 0;
  color: var(--sf-text-primary);
  font-size: var(--sf-fs-section);
  font-weight: 600;
  line-height: 1.4;
}

.sf-page-header.is-plain .sf-page-header__title {
  font-size: var(--sf-fs-page);
}

.sf-page-header__actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-left: auto;
}

.sf-page-header__actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
</style>
