<script setup lang="ts">
import CameraManage from '../camera/Manage.vue'
// Keep saved menu links working while distinguishing group-focused entry from device list entry.
defineOptions({ name: 'CameraGroupManage' })
</script>
<template><CameraManage focus-groups /></template>
