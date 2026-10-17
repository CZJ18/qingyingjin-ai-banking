<script setup lang="ts">
defineProps<{ value: unknown; label?: string }>()
</script>
<template>
  <details class="json-details">
    <summary>{{ label || '查看完整服务器记录' }}</summary>
    <pre>{{ JSON.stringify(value, null, 2) }}</pre>
  </details>
</template>
