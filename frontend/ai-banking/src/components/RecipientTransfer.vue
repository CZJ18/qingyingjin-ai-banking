<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { Search, UserRound, ShieldCheck, ArrowRight } from 'lucide-vue-next'
import { recipientApi } from '@/api/recipients'
import { ApiError, type Schema } from '@/api/client'
import { useSession, useUI } from '@/lib/store'
import { useAccounts } from '@/lib/queries'
import { minor } from '@/lib/utils'
import Button from '@/components/ui/button/Button.vue'
import ExecutionDialog from '@/components/ExecutionDialog.vue'
const s = useSession(), ui = useUI(), accounts = useAccounts()
const kind = ref<'name' | 'phone'>('name'), query = ref(''), result = ref<Schema<'RecipientSearchResult'>>(), selected = ref(''), searching = ref(false), error = ref('')
const account = ref(''), amount = ref(''), description = ref(''), dialog = ref<InstanceType<typeof ExecutionDialog>>()
const enabled = ref(false), receiveAccount = ref(''), dirty = ref(false), saving = ref(false)
const available = computed(() => accounts.data.value?.filter(a => a.status === 'active' && a.currency === 'CNY' && !a.savings_goal_id) ?? [])
const chosen = computed(() => result.value?.items.find(r => r.id === selected.value))
const config = useQuery({ queryKey: computed(() => ['recipient-settings', s.sessionEpoch]), enabled: computed(() => s.connected), queryFn: ({ signal }) => recipientApi.settings(signal) })
let generation = 0
watch([kind, query], () => { generation++; result.value = undefined; selected.value = ''; error.value = '' })
watch(() => s.sessionEpoch, () => { generation++; query.value = ''; result.value = undefined; selected.value = ''; amount.value = ''; description.value = ''; error.value = ''; enabled.value = false; dirty.value = false; account.value = ''; receiveAccount.value = '' })
watch(() => available.value, v => { if (!v.some(a => a.id === account.value)) account.value = v[0]?.id ?? ''; if (!v.some(a => a.id === receiveAccount.value)) receiveAccount.value = v[0]?.id ?? '' }, { immediate: true })
watch(() => config.data.value, v => { if (v && !dirty.value) { enabled.value = v.enabled; receiveAccount.value = v.account_id === '0' ? available.value[0]?.id ?? '' : v.account_id } }, { immediate: true })
async function lookup() {
  if (searching.value) return
  const run = ++generation
  searching.value = true; result.value = undefined; selected.value = ''; error.value = ''
  try { const found = await recipientApi.search({ kind: kind.value, query: query.value.trim() }); if (run === generation) result.value = found }
  catch (e) { if (run === generation) error.value = e instanceof ApiError && e.code === 'RECIPIENT_RATE_LIMITED' ? '查询过于频繁，请一分钟后重试。' : (e as Error).message }
  finally { searching.value = false }
}
async function prepare() {
  const r = chosen.value
  if (!r || !account.value) return
  try { await dialog.value?.prepare('recipient_transfer', { recipient_id: r.id, recipient_version: r.version, source_account_id: account.value, amount_minor: minor(amount.value), description: description.value.trim() }) }
  catch (e) { ui.notify((e as Error).message) }
}
async function saveSettings() {
  if (!config.data.value || saving.value) return
  saving.value = true
  try { await recipientApi.configure({ enabled: enabled.value, account_id: enabled.value ? receiveAccount.value : '0', expected_version: config.data.value.version }); dirty.value = false; await config.refetch(); ui.notify('收款设置已保存；旧版本付款意向需重新确认') }
  catch (e) { ui.notify((e as Error).message); await config.refetch() }
  finally { saving.value = false }
}
</script>
<template>
  <section class="recipient-transfer" aria-labelledby="recipient-heading">
    <header class="recipient-title"><div class="recipient-icon"><UserRound :size="22" /></div><div><h2 id="recipient-heading">找人转账</h2><p>通过姓名或手机号找到对方，核对后再付款。</p></div><span>模拟人民币转账</span></header>
    <p v-if="!s.connected">请先登录，再查找收款人。</p>
    <template v-else>
      <div class="recipient-workspace">
        <div class="recipient-search"><h3>1. 查找并选择收款人</h3>
          <form @submit.prevent="lookup"><label>查找方式<select v-model="kind" aria-label="收款人查找方式"><option value="name">姓名／账户显示名</option><option value="phone">完整手机号</option></select></label>
          <label>{{ kind === 'phone' ? '收款人手机号' : '收款人完整姓名／显示名' }}<input v-model="query" aria-label="收款人查询内容" :inputmode="kind === 'phone' ? 'tel' : 'text'" maxlength="128" autocomplete="off" required :placeholder="kind === 'phone' ? '输入完整11位手机号' : '请输入完整显示名，至少两个字'" /></label>
          <Button type="submit" :disabled="searching || !query.trim()"><Search :size="15" />{{ searching ? '查找中…' : '查找收款人' }}</Button></form>
          <p v-if="error" role="alert" class="recipient-error">{{ error }}</p>
          <template v-if="result"><p v-if="!result.items.length">未找到可收款的用户。请核对信息，并让对方先完成手机号验证、开启收款。</p><p v-else>请选择本人认识的收款人，同名不会自动选中。</p><p v-if="result.limited" role="alert">同名结果超过20位，请改用完整手机号查找。</p>
          <fieldset v-if="result.items.length" class="recipient-results"><legend class="sr-only">选择收款人</legend><label v-for="r in result.items" :key="r.id" class="recipient-result" :class="{ selected: selected === r.id }"><input v-model="selected" type="radio" name="recipient" :value="r.id" :aria-label="`选择收款人 ${r.display_name} ${r.phone_masked}`" /><div><strong>{{ r.display_name }}</strong><span>{{ r.phone_masked }}</span><small>{{ r.account_name }} · {{ r.account_masked_number }}</small><small>{{ r.phone_verification_method === 'development' ? '开发模拟验证 · 非真实短信认证' : '手机号已验证 · 非姓名实名认证' }}</small></div></label></fieldset></template>
          <p class="recipient-note">只查找主动开启收款的用户，不支持手机号前缀或模糊搜索。姓名是账户显示名，请通过其他渠道确认对方身份。</p>
        </div>
        <form class="recipient-payment" @submit.prevent="prepare"><h3>2. 填写金额与转账备注</h3>
          <div class="recipient-chosen"><ShieldCheck :size="19" /><div v-if="chosen"><strong>{{ chosen.display_name }} · {{ chosen.phone_masked }}</strong><small>{{ chosen.account_name }} · {{ chosen.account_masked_number }}</small></div><span v-else>请先从查询结果中明确选择收款人</span></div>
          <label>本人的付款账户<select v-model="account" aria-label="找人转账付款账户" required><option value="" disabled>请选择账户</option><option v-for="a in available" :key="a.id" :value="a.id">{{ a.display_name }} · {{ a.masked_account_number }}</option></select></label>
          <label>转账金额 / 元<input v-model="amount" aria-label="找人转账金额" inputmode="decimal" required placeholder="0.00" /></label>
          <label>转账备注<textarea v-model="description" aria-label="找人转账备注" maxlength="512" rows="2" required placeholder="例如：周末聚餐费用" /></label>
          <Button type="submit" :disabled="!chosen || !account">核对收款人与转账<ArrowRight :size="15" /></Button>
          <p class="recipient-note">下一步仅创建待确认意向。核对姓名、脱敏手机号、账户与金额后，由你明确授权；备注会保存在双方流水。</p>
        </form>
      </div>
      <details class="recipient-settings"><summary>我的收款设置 · {{ config.data.value?.enabled ? '已开启' : '默认关闭，可自行开启' }}</summary>
        <p v-if="config.isPending.value">正在加载收款设置…</p><p v-else-if="config.error.value" role="alert">{{ config.error.value.message }} <button @click="config.refetch()">重试</button></p>
        <form v-else-if="config.data.value" @submit.prevent="saveSettings"><p>开启后，其他登录用户可通过完整姓名或手机号查找你，看到你的显示名、脱敏手机号和收款账户掩码。需要先完成项目内的手机号验证。</p>
          <label class="recipient-toggle"><input v-model="enabled" type="checkbox" @change="dirty = true" /> 允许通过姓名或手机号查找我并向我转账</label>
          <label>本人的收款账户<select v-model="receiveAccount" aria-label="我的默认收款账户" :disabled="!enabled" :required="enabled" @change="dirty = true"><option value="" disabled>请选择账户</option><option v-for="a in available" :key="a.id" :value="a.id">{{ a.display_name }} · {{ a.masked_account_number }}</option></select></label>
          <Button type="submit" :disabled="saving || !dirty || enabled && !receiveAccount">{{ saving ? '保存中…' : '保存收款设置' }}</Button><small>关闭或更换收款账户会使旧转账意向失效，不会自动退款或改变余额。</small>
        </form>
      </details>
    </template>
    <ExecutionDialog ref="dialog" banking />
  </section>
</template>
<style scoped>
.recipient-transfer { background:#fff; border:1px solid #dfe8ee; border-radius:18px; padding:28px; margin:24px 0; } .recipient-title { display:flex; align-items:center; gap:14px; flex-wrap:wrap; } .recipient-title h2 { margin:0 0 6px; font-size:22px; } .recipient-title p { margin:0; } .recipient-title>span { margin-left:auto; color:#148477; font-size:12px; } .recipient-icon { color:#147b6e; background:#e8f5ef; border-radius:12px; padding:12px; } h3 { font-size:16px; margin:0 0 20px; } p,small { font-size:13px; color:#697d91; line-height:1.8; } .recipient-workspace { display:grid; grid-template-columns:1fr 1fr; gap:28px; margin-top:28px; } .recipient-search,.recipient-payment { border:1px solid #e3ebf0; padding:22px; border-radius:12px; } .recipient-payment { background:#f8fbfd; } label { display:flex; flex-direction:column; gap:8px; font-size:13px; color:#34506b; margin-bottom:16px; } input:not([type=radio]):not([type=checkbox]),select,textarea { width:100%; min-width:0; background:#fff; border:1px solid #d6e1e9; border-radius:8px; padding:10px 12px; font:inherit; color:#183650; } textarea { resize:vertical; } .recipient-results { border:0; padding:0; margin:12px 0; max-height:400px; overflow:auto; } .recipient-result { padding:14px; display:flex; flex-direction:row; border:1px solid #e0e9ee; border-radius:10px; align-items:flex-start; cursor:pointer; gap:12px; } .recipient-result.selected { border-color:#159385; background:#eff9f5; } .recipient-result input { margin-top:5px; accent-color:#148b79; } .recipient-result strong,.recipient-result small { display:block; } .recipient-result span { display:block; font-size:13px; margin:5px 0; } .recipient-chosen { display:flex; align-items:center; gap:12px; padding:16px; margin-bottom:20px; border-radius:10px; background:#eaf4f2; color:#176f63; font-size:13px; } .recipient-chosen small { display:block; margin-top:5px; } .recipient-note { margin-top:16px; } .recipient-error { color:#ad493c; } .recipient-settings { margin-top:24px; padding:18px 22px; border-top:1px solid #e1e9ef; } summary { cursor:pointer; font-size:14px; font-weight:600; color:#29475f; } .recipient-settings form { max-width:640px; margin-top:18px; } .recipient-settings small { display:block; margin-top:12px; } .recipient-toggle { flex-direction:row; align-items:flex-start; } .recipient-toggle input { margin-top:5px; accent-color:#148b79; }
@media(max-width:900px) { .recipient-workspace { grid-template-columns:1fr; } } @media(max-width:600px) { .recipient-transfer { padding:18px; } .recipient-search,.recipient-payment { padding:16px; } .recipient-title>span { margin-left:0; } }
</style>
