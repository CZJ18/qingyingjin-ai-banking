<script setup lang="ts">
import { computed } from 'vue'
import { Bell, ArrowRight } from 'lucide-vue-next'
import { PopoverRoot, PopoverTrigger, PopoverPortal, PopoverContent, PopoverClose } from 'reka-ui'
import { useOperations, useRenewals } from '@/lib/queries'
import { useSession, useUI } from '@/lib/store'
import { renewalApi } from '@/api/renewals'
import { money, date } from '@/lib/utils'
const renewals = useRenewals(), ui = useUI()
const unread = computed(() => renewals.data.value?.items.filter(n => n.status === 'unread') ?? [])
async function read(id: string, version: number) {
  try { await renewalApi.read(id, version); await renewals.refetch() }
  catch (e) { ui.notify((e as Error).message); await renewals.refetch() }
}
const s = useSession(),
  ops = useOperations(),
  pending = computed(
    () => ops.data.value?.filter((o) => o.status === 'pending_authorization') || [],
  )
</script>
<template>
  <PopoverRoot
    ><PopoverTrigger class="icon-button notification-button" aria-label="通知"
      ><Bell :size="18" /><i v-if="pending.length || unread.length" /></PopoverTrigger
    ><PopoverPortal
      ><PopoverContent class="notification-popover" :side-offset="12" align="end"
        ><h3>待处理提醒</h3>
        <template v-if="s.connected">
          <h4>续费提醒 <span v-if="unread.length">{{ unread.length }}</span></h4>
          <p v-if="renewals.error.value">续费提醒暂时不可用，请前往订阅页重试。</p>
          <p v-else-if="renewals.isPending.value">正在加载续费提醒…</p>
          <p v-else-if="!unread.length">暂无未读续费提醒。</p>
          <div v-for="n in unread.slice(0, 5)" :key="n.id" class="renewal-preview">
            <strong>{{ n.merchant_name }}</strong><p>预计 {{ date(n.charge_at) }} · {{ money(n.amount_minor) }}</p>
            <button class="text-link" @click="read(n.id, n.version)">标为已读</button>
          </div>
          <PopoverClose as-child><RouterLink to="/subscriptions" class="text-link">查看全部续费提醒<ArrowRight :size="14" /></RouterLink></PopoverClose>
          <hr />
        </template>
        <p v-if="!s.connected">登录后查看待确认操作。</p>
        <p v-else-if="ops.error.value">提醒暂时不可用，请到安全中心重试。</p>
        <p v-else-if="!pending.length">暂无待授权金融操作。</p>
        <p v-else>有 {{ pending.length }} 项金融操作等待你的确认。</p>
        <PopoverClose as-child
          ><RouterLink to="/security" class="text-link"
            >前往安全中心<ArrowRight
              :size="14" /></RouterLink></PopoverClose></PopoverContent></PopoverPortal
  ></PopoverRoot>
</template>
<style scoped>
.notification-popover { width: min(360px, calc(100vw - 32px)); max-height: 75vh; overflow-y: auto; }
.renewal-preview { padding: 12px 0; border-bottom: 1px solid #e8eeea; }
.renewal-preview p { font-size: 13px; } h4 { margin: 16px 0 8px; } hr { margin: 16px 0; border: 0; border-top: 1px solid #e8eeea; }
</style>
