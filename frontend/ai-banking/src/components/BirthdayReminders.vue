<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { birthdayApi, type BirthdayEntry, type BirthdayUpcoming } from '@/api/birthdays'
import { sessionVersion } from '@/api/client'
import { useSession, useUI } from '@/lib/store'
import Button from '@/components/ui/button/Button.vue'

const emit = defineEmits<{ prepare: [value: { name: string; birthday: string }] }>()
const s = useSession(), ui = useUI(), cache = useQueryClient()
const key = computed(() => ['birthday-calendar', s.sessionEpoch])
const calendar = useQuery({ queryKey: key, enabled: computed(() => s.connected), queryFn: ({ signal }) => birthdayApi.read(signal), refetchInterval: 60000 })
const enabled = ref(true), days = ref(30), dirty = ref(false), busy = ref(false)
const name = ref(''), monthDay = ref('')
watch(() => calendar.data.value, (value) => {
  if (value && !dirty.value) { enabled.value = value.enabled; days.value = value.lead_days }
}, { immediate: true })
watch(() => s.sessionEpoch, () => {
  enabled.value = true; days.value = 30; dirty.value = false; busy.value = false; name.value = ''; monthDay.value = ''
})
async function persist(entries: BirthdayEntry[], options: { added?: boolean; saveSettings?: boolean } = {}) {
  const value = calendar.data.value
  if (!value || busy.value) return
  const epoch = sessionVersion()
  const settings = options.saveSettings
    ? { enabled: enabled.value, lead_days: days.value }
    : { enabled: value.enabled, lead_days: value.lead_days }
  busy.value = true
  try {
    const saved = await birthdayApi.save({ expected_version: value.version, ...settings, entries })
    if (epoch !== sessionVersion()) return
    if (options.saveSettings && enabled.value === settings.enabled && days.value === settings.lead_days)
      dirty.value = false
    cache.setQueryData(key.value, saved)
    if (options.added) { name.value = ''; monthDay.value = '' }
    ui.notify('生日日历已保存；资金安排仍需单独确认')
  } catch (error) {
    if (epoch !== sessionVersion()) return
    ui.notify((error as Error).message)
    await calendar.refetch()
  } finally { if (epoch === sessionVersion()) busy.value = false }
}
async function add() {
  if (!name.value.trim() || !/^\d{2}-\d{2}$/.test(monthDay.value)) { ui.notify('请明确填写对象和 MM-DD 格式的生日'); return }
  await persist([...(calendar.data.value?.entries || []), { id: crypto.randomUUID(), name: name.value.trim(), month_day: monthDay.value }], { added: true })
}
function prepare(entry: BirthdayUpcoming) { emit('prepare', { name: entry.name, birthday: entry.next_date }) }
</script>

<template>
  <section class="birthday-panel" aria-labelledby="birthday-heading">
    <h2 id="birthday-heading">生日日历与站内提醒</h2>
    <p>只保存你明确填写的对象称呼和月日。每年按北京时间识别临近生日，不读取联系人或推断关系。</p>
    <p v-if="!s.connected">登录后保存生日。</p>
    <p v-else-if="calendar.isPending.value">正在加载生日日历…</p>
    <p v-else-if="calendar.error.value" role="alert">{{ calendar.error.value.message }} <button @click="calendar.refetch()">重试</button></p>
    <template v-else-if="calendar.data.value">
      <form class="birthday-form" @submit.prevent="persist(calendar.data.value!.entries, { saveSettings: true })">
        <label><input v-model="enabled" type="checkbox" @change="dirty = true" /> 开启生日站内提醒</label>
        <label>提前天数<input v-model.number="days" aria-label="生日提醒提前天数" type="number" min="0" max="30" required @input="dirty = true" /></label>
        <Button type="submit" :disabled="busy || !dirty">保存生日提醒设置</Button>
      </form>
      <form class="birthday-form" @submit.prevent="add">
        <label>对象称呼<input v-model="name" aria-label="生日对象称呼" maxlength="32" required /></label>
        <label>生日月日<input v-model="monthDay" aria-label="生日月日" placeholder="例如 11-01" pattern="[0-9]{2}-[0-9]{2}" maxlength="5" required /></label>
        <Button type="submit" :disabled="busy || calendar.data.value.entries.length >= 50">保存这个生日</Button>
      </form>
      <p class="birthday-note">最多保存50个生日，不保存出生年份。2月29日仅在下一个实际闰年提醒，不改为2月28日或3月1日。0天只提醒当天。</p>
      <ul class="birthday-list" aria-label="已保存的生日">
        <li v-for="entry in calendar.data.value.entries" :key="entry.id"><span>{{ entry.name }} · 每年 {{ entry.month_day }}</span><Button variant="ghost" :disabled="busy" @click="persist(calendar.data.value!.entries.filter(item => item.id !== entry.id))">删除 {{ entry.name }} 的生日</Button></li>
      </ul>
      <div class="birthday-upcoming" aria-live="polite">
        <h3>临近生日</h3>
        <p v-if="!calendar.data.value.enabled">提醒已关闭，已保存的日期仍可管理。</p>
        <p v-else-if="!calendar.data.value.upcoming.length">未来{{ calendar.data.value.lead_days }}天内暂无已保存的生日。</p>
        <article v-for="entry in calendar.data.value.upcoming" :key="entry.id">
          <strong>{{ entry.name }} · {{ entry.next_date }}</strong><span>{{ entry.days_until === 0 ? '今天生日' : entry.days_until + '天后生日' }}</span>
          <Button variant="outline" @click="prepare(entry)">填写庆祝计划</Button>
        </article>
      </div>
      <p class="birthday-note">打开页面时读取并每分钟刷新。提醒和填写表单都不会创建资金预留、下单或授权；庆祝计划仍需你逐项核对并确认。</p>
    </template>
  </section>
</template>

<style scoped>
.birthday-panel{padding:22px;margin:20px 0;border:1px solid #dbe4ea;border-radius:9px;background:#fff;line-height:1.75;overflow-wrap:anywhere}.birthday-panel h2{font-size:20px;margin:0 0 12px}.birthday-panel p{font-size:13px;color:#52657b}.birthday-form{display:flex;gap:14px;align-items:end;flex-wrap:wrap;margin:18px 0}.birthday-form label{display:flex;gap:6px;flex-direction:column;font-size:13px}.birthday-form label:has(input[type=checkbox]){flex-direction:row;align-items:center}.birthday-form input:not([type=checkbox]){padding:9px;border:1px solid #cbd5e1;border-radius:6px;font:inherit;min-width:0}.birthday-list{list-style:none;padding:0;max-height:300px;overflow-y:auto}.birthday-list li,.birthday-upcoming article{display:flex;gap:14px;align-items:center;justify-content:space-between;flex-wrap:wrap;padding:10px 0;border-top:1px solid #dbe4ea}.birthday-upcoming h3{font-size:16px}.birthday-note{font-size:12px}@media(max-width:600px){.birthday-panel{padding:15px}.birthday-form{display:grid;grid-template-columns:1fr}.birthday-form input:not([type=checkbox]){width:100%;box-sizing:border-box}}
</style>
