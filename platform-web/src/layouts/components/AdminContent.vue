<template>
  <main id="main-content" class="main-content" tabindex="-1">
    <slot />
  </main>
</template>

<style scoped>
.main-content {
  --content-spacing: clamp(18px, 1.7vw, 26px);
  width: 100%;
}
@media (max-width: 680px) {
  .main-content {
    --content-spacing: 14px;
  }
}
</style>
