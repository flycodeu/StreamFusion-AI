<script setup lang="ts">
import { computed } from 'vue'
import { Connection, Lock, DocumentRemove } from '@element-plus/icons-vue'

const props = withDefaults(
  defineProps<{
    title: string
    description: string
    kind?: 'unavailable' | 'forbidden' | 'missing'
    fullscreen?: boolean
  }>(),
  { kind: 'unavailable', fullscreen: false },
)
const icon = computed(
  () => ({ unavailable: Connection, forbidden: Lock, missing: DocumentRemove })[props.kind],
)
</script>

<template>
  <section class="page-state" :class="{ 'page-state--fullscreen': fullscreen }">
    <div class="state-card">
      <div class="state-icon" :class="`state-icon--${kind}`" aria-hidden="true">
        <component :is="icon" />
      </div>
      <div class="state-copy" role="status">
        <h1>{{ title }}</h1>
        <p>{{ description }}</p>
      </div>
      <div v-if="$slots.actions" class="state-actions"><slot name="actions" /></div>
      <div v-if="$slots.details" class="state-details"><slot name="details" /></div>
    </div>
  </section>
</template>

<style scoped>
.page-state {
  display: grid;
  place-items: center;
  min-height: min(660px, calc(100dvh - 140px));
  padding: clamp(24px, 5vw, 64px) 20px;
}
.page-state--fullscreen {
  min-height: 100dvh;
  background: var(--surface-muted);
}
.state-card {
  width: min(100%, 560px);
  padding: 40px 36px 32px;
  background: #fff;
  border: 1px solid var(--border-subtle);
  border-radius: 14px;
  text-align: center;
  box-shadow: 0 8px 32px #182b4210;
}
.state-icon {
  display: grid;
  place-items: center;
  width: 76px;
  height: 76px;
  margin: 0 auto 24px;
  color: #477c97;
  background: #edf4f7;
  border: 1px solid #dce9ef;
  border-radius: 24px;
}
.state-icon svg {
  width: 34px;
  height: 34px;
}
.state-icon--forbidden,
.state-icon--missing {
  color: #9b753a;
  background: #faf5eb;
  border-color: #efe4ce;
}
.state-copy h1 {
  margin: 0;
  color: var(--text-primary);
  font-size: 23px;
  font-weight: 600;
  letter-spacing: 0.02em;
}
.state-copy p {
  max-width: 390px;
  margin: 14px auto 0;
  color: var(--text-secondary);
  font-size: 14px;
  line-height: 1.8;
  overflow-wrap: anywhere;
}
.state-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: 10px;
  margin-top: 28px;
}
.state-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
.state-actions :deep(.el-button) {
  min-width: 104px;
  height: 36px;
}
.state-details {
  margin-top: 28px;
  padding-top: 18px;
  border-top: 1px solid var(--border-subtle);
  text-align: left;
}
.state-details :deep(.request-error) {
  margin: 0;
}
@media (max-width: 480px) {
  .page-state {
    padding: 24px 14px;
  }
  .state-card {
    padding: 32px 20px 24px;
  }
  .state-copy h1 {
    font-size: 21px;
  }
}
</style>
