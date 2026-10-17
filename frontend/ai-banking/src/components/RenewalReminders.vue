<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRenewals } from '@/lib/queries'
import { useSession, useUI } from '@/lib/store'
import { renewalApi } from '@/api/renewals'
import { money, date } from '@/lib/utils'
import Button from '@/components/ui/button/Button.vue'
const notices = useRenewals(), s = useSession(), ui = useUI()
const enabled = ref(true), days = ref(3), dirty = ref(false), busy = ref(false)
watch(() => notices.data.value?.settings, (v) => {
  if (v && !dirty.value) { enabled.value = v.enabled; days.value = v.lead_days }
}, { immediate: true })
watch(() => s.sessionEpoch, () => { dirty.value = false; enabled.value = true; days.value = 3 })
async function save() {
  const settings = notices.data.value?.settings
  if (!settings || busy.value) return
  busy.value = true
  try {
    await renewalApi.configure({ enabled: enabled.value, lead_days: days.value, expected_version: settings.version })
    dirty.value = false
    await notices.refetch()
    ui.notify('续费提醒设置已保存')
  } catch (e) { ui.notify((e as Error).message); await notices.refetch() }
  finally { busy.value = false }
}
async function read(id: string, version: number) {
  if (busy.value) return
  busy.value = true
  try { await renewalApi.read(id, version); await notices.refetch() }
  catch (e) { ui.notify((e as Error).message); await notices.refetch() }
  finally { busy.value = false }
}
const labels: Record<string, string> = { unread: '未读', read: '已读', withdrawn: '已撤回', expired: '已过期' }
</script>
<template>
  <section class="renewal-panel" aria-labelledby="renewal-heading">
    <div class="renewal-heading"><div><h2 id="renewal-heading">续费提醒</h2><p>后台每分钟检查，通知保存在站内。提醒不会自动续费或扣款。</p></div><span v-if="notices.data.value">{{ notices.data.value.unread_count }} 条未读</span></div>
    <p v-if="!s.connected">登录后设置提醒。</p>
    <p v-else-if="notices.isPending.value">正在加载续费提醒…</p>
    <p v-else-if="notices.error.value" role="alert">{{ notices.error.value.message }} <button @click="notices.refetch()">重试</button></p>
    <template v-else-if="notices.data.value">
      <form class="renewal-settings" @submit.prevent="save">
        <label><input v-model="enabled" type="checkbox" @change="dirty = true" /> 开启站内续费提醒</label>
        <label>提前天数 <input v-model.number="days" aria-label="续费提醒提前天数" type="number" min="0" max="30" required @input="dirty = true" /></label>
        <Button type="submit" :disabled="busy || !dirty">{{ busy ? '保存中…' : '保存提醒设置' }}</Button>
      </form>
      <p class="renewal-note">按上海时区自然日提醒，0 天表示仅提醒当天。仅覆盖已确认的模拟人民币订阅；预计扣费时间不代表已经扣费。</p>
      <p v-if="notices.data.value.last_checked_at" class="renewal-note">最近检查：{{ date(notices.data.value.last_checked_at) }}</p>
      <p v-if="notices.data.value.capacity_limited" role="alert">提醒记录已达 200 条上限，部分提醒未能生成，请检查订阅列表。</p>
      <p v-if="!notices.data.value.items.length">暂无续费提醒，符合提前天数的订阅将由后台生成通知。</p>
      <ul v-else class="renewal-list"><li v-for="n in notices.data.value.items" :key="n.id" :class="{ unread: n.status === 'unread' }">
        <div><strong>{{ n.merchant_name }}</strong><p>预计 {{ date(n.charge_at) }} · {{ money(n.amount_minor) }}</p><small>{{ labels[n.status] }}{{ n.reason === 'subscription_changed' ? ' · 订阅已取消或变更' : '' }}</small></div>
        <Button v-if="n.status === 'unread'" variant="outline" :disabled="busy" @click="read(n.id, n.version)">标为已读</Button>
      </li></ul>
    </template>
  </section>
</template>
<style scoped>
.renewal-panel { border: 1px solid var(--border, #dfe6e2); border-radius: 16px; padding: 24px; margin: 24px 0; background: var(--card, #fff); }
.renewal-heading, .renewal-settings, .renewal-list li { display: flex; align-items: center; justify-content: space-between; gap: 16px; flex-wrap: wrap; }
h2 { font-size: 18px; margin: 0 0 8px; } p { margin: 8px 0; } .renewal-heading p, .renewal-note, small { color: #64746d; font-size: 13px; }
.renewal-settings { justify-content: flex-start; margin: 20px 0 12px; } .renewal-settings input[type=number] { width: 70px; padding: 6px; border: 1px solid #cbd5cf; border-radius: 6px; }
.renewal-list { padding: 0; list-style: none; max-height: 420px; overflow-y: auto; } .renewal-list li { padding: 16px 0; border-top: 1px solid #e8eeea; } .unread strong { color: #16745a; }
</style>
