<script setup lang="ts">
import { showErrorHelp } from "./errorHelp";
defineProps<{ code: string }>();
</script>

<template>
  <button v-if="code" type="button" class="quiet" @click.stop="showErrorHelp(code)">
    查看错误说明
  </button>
</template>
