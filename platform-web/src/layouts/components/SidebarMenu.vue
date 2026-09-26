<script setup lang="ts">
import { ElMenuItem, ElSubMenu } from 'element-plus'
import type { MenuNavigation } from '../../router/dynamic/routes'
import AppIcon from '../../components/icons/AppIcon.vue'
defineProps<{ nodes: MenuNavigation[] }>()
</script>

<template>
  <template v-for="node in nodes" :key="node.id">
    <ElSubMenu v-if="node.children.length" :index="`directory:${node.id}`">
      <template #title
        ><AppIcon :name="node.icon" /><span>{{ node.title }}</span></template
      >
      <SidebarMenu :nodes="node.children" />
    </ElSubMenu>
    <ElMenuItem v-else-if="node.path" :index="node.path">
      <AppIcon :name="node.icon" /><template #title
        ><span>{{ node.title }}</span></template
      >
    </ElMenuItem>
  </template>
</template>
