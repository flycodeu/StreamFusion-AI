<template>
  <main id="main-content" class="main-content" tabindex="-1">
    <slot />
  </main>
</template>

<style scoped>
.main-content {
  padding: 36px;
}
@media (max-width: 680px) {
  .main-content {
    padding: 26px 18px;
  }
}
</style>
