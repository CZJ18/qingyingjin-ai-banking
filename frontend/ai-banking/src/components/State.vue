<script setup lang="ts">
import { Inbox, AlertCircle, Link2, RotateCw } from 'lucide-vue-next'
import Button from './ui/button/Button.vue'
defineProps<{
  loading?: boolean
  error?: Error | null
  empty?: boolean
  connected?: boolean
  compact?: boolean
  title?: string
  description?: string
}>()
defineEmits<{ retry: [] }>()
</script>
<template>
  <div v-if="loading" class="skeleton-group" aria-busy="true" aria-label="正在加载">
    <div class="skeleton skeleton-title" />
    <div class="skeleton skeleton-value" />
    <div class="skeleton skeleton-line" />
  </div>
  <div v-else-if="error" class="empty-state error-state" role="alert">
    <AlertCircle :size="26" /><strong>暂时无法获取数据</strong>
    <p>{{ error.message }}</p>
    <small v-if="'requestId' in error">请求编号：{{ error.requestId }}</small>
    <Button variant="outline" @click="$emit('retry')"><RotateCw :size="14" />重试</Button>
  </div>
  <div v-else-if="!connected || empty" class="empty-state" :class="{ compact }">
    <component :is="!connected ? Link2 : Inbox" :size="24" />
    <div class="state-copy">
      <strong>{{ title || (!connected ? '尚未登录' : '这里还没有记录') }}</strong>
      <p>
        {{
          !connected
            ? '通过右上角登录后查看业务数据。'
            : description || '新的业务记录会在这里展示。'
        }}
      </p>
    </div>
  </div>
  <slot v-else />
</template>
