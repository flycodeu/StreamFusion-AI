<template>
  <main id="main-content" class="main-content" tabindex="-1">
    <slot />
  </main>
</template>

<style scoped>
.main-content {
  --content-spacing: clamp(24px, 2vw, 30px);
  width: 100%;
}
@media (max-width: 680px) {
  .main-content {
    --content-spacing: clamp(16px, 3vw, 20px);
  }
}
</style>
