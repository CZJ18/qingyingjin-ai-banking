<script setup lang="ts">
import { statusLabels } from '@/lib/utils'
defineProps<{ value: string }>()
</script>
<template>
  <span class="status" :class="value"><i />{{ statusLabels[value] || value }}</span>
</template>
