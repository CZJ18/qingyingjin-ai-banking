<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { Split, Users, Copy, ArrowRight } from 'lucide-vue-next'
import { aaApi } from '@/api/aa'
import type { Schema } from '@/api/client'
import { useSession, useUI } from '@/lib/store'
import { useAccounts } from '@/lib/queries'
import { money, minor } from '@/lib/utils'
import Button from '@/components/ui/button/Button.vue'
import ExecutionDialog from '@/components/ExecutionDialog.vue'
import AAManualReview from '@/components/AAManualReview.vue'
const s = useSession(), ui = useUI(), accounts = useAccounts()
const title = ref(''), total = ref(''), mode = ref<'equal' | 'custom'>('equal'), own = ref('0'), names = ref(''), custom = ref<Record<string, string>>({}), paid = ref(false)
const invitation = ref(''), preview = ref<Schema<'AABillView'>>(), selected = ref(''), busy = ref(false), cancelId = ref(''), offset = ref(0)
const dialog = ref<InstanceType<typeof ExecutionDialog>>()
const manualDialog = ref<InstanceType<typeof AAManualReview>>()
const paymentMethod=ref<'simulation'|'wechat_manual'>('wechat_manual'),managementMode=ref<'single_manager'|'collaborative'>('single_manager'),recipientHint=ref(''),memo=ref(''),items=ref([{name:'',amount:''}])
watch(paymentMethod,value=>{if(value==='simulation')managementMode.value='collaborative'})
const participantNames = computed(() => names.value.split('\n').map(v => v.trim()).filter(Boolean))
const available = computed(() => accounts.data.value?.filter(a => a.status === 'active' && a.currency === 'CNY' && !a.savings_goal_id) ?? [])
const account = ref('')
watch(() => available.value, (v) => { if (!v.some(a => a.id === account.value)) account.value = v[0]?.id ?? '' }, { immediate: true })
const bills = useQuery({ queryKey: computed(() => ['aa-bills', s.sessionEpoch, offset.value]), enabled: computed(() => s.connected), queryFn: ({ signal }) => aaApi.list(offset.value, signal), refetchInterval: 10000 })
let pendingCreation: { payload: string; key: string } | undefined
watch(() => s.sessionEpoch, () => { title.value = ''; total.value = ''; names.value = ''; invitation.value = ''; preview.value = undefined; selected.value = ''; paid.value = false; custom.value = {}; cancelId.value = ''; offset.value = 0; pendingCreation = undefined;paymentMethod.value='wechat_manual';managementMode.value='single_manager';recipientHint.value='';memo.value='';items.value=[{name:'',amount:''}] })
async function run(fn: () => Promise<void>) { if (busy.value) return; busy.value = true; try { await fn() } catch (e) { ui.notify((e as Error).message) } finally { busy.value = false } }
async function create() {
  await run(async () => {
    const body: Schema<'AABillInput'> = { title: title.value.trim(), total_minor: minor(total.value), mode: mode.value, ...(paymentMethod.value==='simulation'?{account_id:account.value}:{}), payment_method:paymentMethod.value,management_mode:managementMode.value, ...(paymentMethod.value==='wechat_manual'?{recipient_hint:recipientHint.value.trim(),memo:memo.value.trim(),items:items.value.map(i=>({name:i.name.trim(),amount_minor:minor(i.amount)}))}:{}), already_paid: paid.value as true, owner_share_minor: mode.value === 'custom' ? minor(own.value) : 0, participants: participantNames.value.map(name => ({ name, amount_minor: mode.value === 'custom' ? minor(custom.value[name] ?? '') : 0 })) }
    if (!paid.value) throw new Error('请确认这笔账单由你垫付；创建账单不会生成消费或扣款')
    const payload = JSON.stringify(body)
    if (pendingCreation?.payload !== payload) pendingCreation = { payload, key: crypto.randomUUID() }
    await aaApi.create(body, pendingCreation.key)
    pendingCreation = undefined; title.value = ''; total.value = ''; names.value = ''; paid.value = false; custom.value = {}; offset.value = 0;items.value=[{name:'',amount:''}];memo.value=''
    await bills.refetch(); ui.notify(managementMode.value==='single_manager'?'单人AA账单已创建；朋友无需账号，你核对微信到账后逐人确认':'AA账单已创建，请私下分享邀请码')
  })
}
async function inspectInvitation() { await run(async () => { preview.value = undefined; selected.value = ''; preview.value = await aaApi.preview(invitation.value.trim()) }) }
async function join() { await run(async () => { await aaApi.join(invitation.value.trim(), selected.value); preview.value = undefined; invitation.value = ''; selected.value = ''; offset.value = 0; await bills.refetch(); ui.notify('已加入AA账单，付款仍需你单独授权') }) }
async function pay(b: Schema<'AABillView'>, p: Schema<'AAParticipant'>) {
  if(b.payment_method==='wechat_manual'){await manualDialog.value?.inspect(b,p,'report_paid');return}
  if (!account.value) { ui.notify('请先选择本人的付款账户'); return }
  await dialog.value?.prepare('aa_payment', { owner_id: b.owner_id, bill_id: b.id, participant_id: p.id, source_account_id: account.value })
}
async function cancel(b: Schema<'AABillView'>) { await run(async () => { await aaApi.cancel(b.id, b.version); cancelId.value = ''; await bills.refetch(); ui.notify('已终止未付部分，已付款保留') }) }
async function copy(value: string) { try { await navigator.clipboard.writeText(value); ui.notify('邀请码已复制，请只分享给参与人') } catch { ui.notify('请手动复制下方邀请码') } }
async function copySummary(b:Schema<'AABillView'>){
 const text=[`AA账单：${b.title}`,`收款人提示：${b.recipient_hint}`,`总金额：${money(b.total_minor)}`,...(b.items||[]).map(i=>`${i.name}：${money(i.amount_minor)}`),'分摊明细：',...b.participants.map(p=>`${p.name}：${money(p.amount_minor)}`),`备注：${b.memo||'无'}`,'请在微信核对收款人及金额后自行付款。此明细不是付款凭证，收款记录由发起人核实。'].join('\n')
 try{await navigator.clipboard.writeText(text);ui.notify('分摊明细已复制，可私下发给朋友；不含登录或确认凭据')}catch{ui.notify('无法复制，请根据账单明细手动分享')}
}
const status: Record<string, string> = { open: '待结清', settled: '已结清', cancelled: '已终止', self: '本人分摊 · 已垫付', unclaimed: '待加入', joined: '待付款', paid: '模拟账户已付款',payment_reported:'付款人自报已付 · 待收款人核对',receipt_confirmed:'发起人已核对收款 · 未获微信核验',awaiting_receipt:'待发起人核对收款（无需参与人账号）' }
</script>
<template>
  <section id="aa-bills" class="aa-manager" aria-labelledby="aa-heading">
    <div class="aa-title"><div class="aa-symbol"><Split :size="23" /></div><div><h2 id="aa-heading">AA分摊转账</h2><p>把一笔共同消费，拆成每个人清楚的一份。</p></div><span class="aa-simulation">模拟账户 / 微信线下结算</span></div>
    <p v-if="!s.connected">登录后创建或加入AA账单。</p>
    <template v-else>
      <div class="aa-workspace">
        <form class="aa-create" @submit.prevent="create">
          <h3>我已垫付，发起AA</h3>
          <label>管理模式<select v-model="managementMode" aria-label="AA管理模式" :disabled="paymentMethod==='simulation'"><option value="single_manager">单人管理（默认，其他人无需账号）</option><option value="collaborative">多人协作（参与人分别登录）</option></select></label>
          <p v-if="managementMode==='single_manager'">你一个账号管理全单，朋友通过微信转账。你核对到账后逐人确认收款，不代表参与人本人确认或微信平台核验。</p>
          <label>结算方式<select v-model="paymentMethod" aria-label="AA结算方式"><option value="simulation">模拟账户转账</option><option value="wechat_manual">微信线下转账（人工核对）</option></select></label>
          <label>账单名称<input v-model="title" aria-label="AA账单名称" maxlength="128" required placeholder="例如：周末聚餐" /></label>
          <template v-if="paymentMethod==='wechat_manual'">
            <label>微信收款人提示（非实名认证）<input v-model="recipientHint" aria-label="AA收款人提示" maxlength="128" required placeholder="请填写参与人能核对的称呼"/></label>
            <h4>消费明细（合计须等于总金额）</h4>
            <div v-for="(item,i) in items" :key="i" class="aa-fields"><label>项目<input v-model="item.name" :aria-label="`AA明细${i+1}项目`" maxlength="128" required/></label><label>金额 / 元<input v-model="item.amount" :aria-label="`AA明细${i+1}金额`" inputmode="decimal" required/></label><button v-if="items.length>1" type="button" @click="items.splice(i,1)">删除此项</button></div>
            <Button type="button" variant="outline" :disabled="items.length>=30" @click="items.push({name:'',amount:''})">添加消费明细</Button>
            <label>转账备注<input v-model="memo" aria-label="AA转账备注" maxlength="400"/></label>
          </template>
          <div class="aa-fields"><label>总金额 / 元<input v-model="total" aria-label="AA总金额" inputmode="decimal" required placeholder="360.00" /></label><label>分摊方式<select v-model="mode" aria-label="AA分摊方式"><option value="equal">均分（包含我）</option><option value="custom">自定义金额</option></select></label></div>
          <label>其他参与人（每行一位，最多19位）<textarea v-model="names" aria-label="AA其他参与人" rows="3" required placeholder="小林&#10;小陈&#10;小周" /></label>
          <template v-if="mode === 'custom'"><label>我分摊 / 元<input v-model="own" aria-label="AA本人分摊金额" inputmode="decimal" required /></label><label v-for="(name, i) in participantNames" :key="i">{{ name }}分摊 / 元<input v-model="custom[name]" :aria-label="`AA${name}分摊金额`" inputmode="decimal" required /></label></template>
          <label class="aa-check"><input v-model="paid" type="checkbox" /> 我确认已垫付，这里只创建分摊应收</label>
          <Button type="submit" :disabled="busy || paymentMethod==='simulation'&&!account || !paid">创建AA账单<ArrowRight :size="15" /></Button>
          <small>含本人共 {{ participantNames.length + 1 }} 人。分摊金额由服务器计算；均分余数优先分配给发起人。</small>
        </form>
        <div class="aa-join"><h3><Users :size="18" /> 我来参与AA</h3><p>粘贴发起人私下提供的邀请码，核对账单并认领自己的位置。加入账单不会授权扣款。</p>
          <label>邀请码<textarea v-model="invitation" aria-label="AA邀请码" rows="3" autocomplete="off" spellcheck="false" @input="preview = undefined; selected = ''" /></label>
          <Button variant="outline" :disabled="busy || !invitation.trim()" @click="inspectInvitation">查看邀请账单</Button>
          <div v-if="preview" class="aa-preview"><strong>{{ preview.title }} · {{ money(preview.total_minor) }}</strong><label>我的参与位置<select v-model="selected" aria-label="AA参与位置"><option value="" disabled>请选择你本人</option><option v-for="p in preview.participants.filter(p => p.status === 'unclaimed' || p.is_me && !preview?.is_owner)" :key="p.id" :value="p.id">{{ p.name }} · {{ money(p.amount_minor) }}</option></select></label><Button :disabled="busy || !selected || preview.is_owner" @click="join">确认加入，暂不付款</Button></div>
          <div class="aa-account"><label>本人的模拟账户（仅模拟结算使用）<select v-model="account" aria-label="AA本人账户"><option value="" disabled>请选择账户</option><option v-for="a in available" :key="a.id" :value="a.id">{{ a.display_name }} · {{ a.masked_account_number }}</option></select></label><p v-if="!available.length">没有模拟账户也可参与微信线下结算；模拟结算需要可用的模拟人民币账户。</p></div>
        </div>
      </div>
      <div class="aa-list-heading"><h3>我的AA账单</h3><button class="text-link" :disabled="busy" @click="bills.refetch()">刷新进度</button></div>
      <p v-if="bills.isPending.value">正在加载AA账单…</p>
      <p v-else-if="bills.error.value" role="alert">{{ bills.error.value.message }} <button @click="bills.refetch()">重试</button></p>
      <p v-else-if="!bills.data.value?.length">暂无AA账单。可发起分摊，或通过邀请码加入。</p>
      <article v-for="b in bills.data.value" :key="`${b.owner_id}:${b.id}`" class="aa-bill" :data-bill-id="b.id">
        <div class="aa-bill-heading"><div><h3>{{ b.title }}</h3><small>{{ b.is_owner ? '我发起的' : '我参与的' }} · {{ b.management_mode==='single_manager'?'单人管理':'多人协作' }} · {{ b.mode === 'equal' ? '均分' : '自定义' }} · {{ b.participants.length }}人</small></div><strong>{{ money(b.total_minor) }}</strong><span class="aa-state">{{ status[b.status] }}</span></div>
        <template v-if="b.payment_method==='wechat_manual'"><p>微信线下结算 · 收款人提示：{{ b.recipient_hint }} · 未获平台核验</p><ul><li v-for="(item,i) in b.items" :key="i"><span>{{ item.name }}</span><b>{{ money(item.amount_minor) }}</b></li></ul><p>备注：{{ b.memo||'无' }}</p></template>
        <Button v-if="b.is_owner&&b.management_mode==='single_manager'" variant="outline" @click="copySummary(b)">复制分摊明细给朋友（无需账号）</Button>
        <div class="aa-progress"><span>{{ b.payment_method==='wechat_manual'?'人工确认已收（未获平台核验）':'模拟账户已收' }} <b>{{ money(b.collected_minor) }}</b></span><span>{{ b.status === 'cancelled' ? '已终止未付' : '待收' }} <b>{{ money(b.outstanding_minor) }}</b></span></div>
        <ul><li v-for="p in b.participants" :key="p.id"><div><b>{{ p.name }}</b><small>{{ p.is_me ? ' · 我' : '' }}</small></div><span>{{ money(p.amount_minor) }}</span><small>{{ status[p.status] }}</small><Button v-if="p.is_me && p.status === 'joined' && b.status === 'open'" :disabled="busy || b.payment_method!=='wechat_manual'&&!account" @click="pay(b, p)">{{ b.payment_method==='wechat_manual'?'核对明细并准备微信付款':'确认本人的付款' }}</Button><Button v-if="b.is_owner&&(p.status==='payment_reported'||p.status==='awaiting_receipt'&&b.status==='open')" :disabled="busy" @click="manualDialog?.inspect(b,p,'confirm_received')">核对微信到账并确认收款</Button></li></ul>
        <div v-if="b.is_owner && b.status === 'open'" class="aa-invite"><template v-if="b.management_mode!=='single_manager' && b.invitation"><label>私密邀请码<textarea :value="b.invitation" aria-label="分享AA邀请码" readonly rows="2" /></label><Button variant="outline" @click="copy(b.invitation)"><Copy :size="14" />复制邀请码</Button></template><label class="aa-check"><input :checked="cancelId === b.id" type="checkbox" @change="cancelId = cancelId === b.id ? '' : b.id" /> 确认终止未付分摊，已付款不退款</label><Button variant="outline" :disabled="busy || cancelId !== b.id" @click="cancel(b)">终止未付分摊</Button></div>
      </article>
      <div class="aa-pagination"><Button variant="outline" :disabled="offset === 0" @click="offset -= 50">上一页</Button><span>第 {{ offset / 50 + 1 }} 页</span><Button variant="outline" :disabled="(bills.data.value?.length ?? 0) < 50 || offset >= 100" @click="offset += 50">下一页</Button></div>
      <p class="aa-boundary">单人模式由发起人核对并记录收款；多人模式每人独立确认。创建、加入、终止账单都不会自动转账；已垫付声明不等同于导入消费流水。</p>
    </template>
    <ExecutionDialog ref="dialog" banking />
    <AAManualReview ref="manualDialog" @changed="bills.refetch()"/>
  </section>
</template>
<style scoped>
.aa-manager { margin: 24px 0; padding: 28px; background: #fff; border: 1px solid #dfe7ed; border-radius: 18px; }
.aa-title, .aa-bill-heading, .aa-list-heading { display: flex; align-items: center; gap: 14px; flex-wrap: wrap; } .aa-symbol { padding: 12px; border-radius: 12px; background: #e8f4ef; color: #147568; }
h2 { font-size: 22px; margin: 0 0 6px; } h3 { font-size: 16px; margin: 0 0 16px; } p, small { color: #697c92; font-size: 13px; line-height: 1.8; } .aa-simulation { margin-left: auto; color: #148779; font-size: 12px; }
.aa-workspace { display: grid; grid-template-columns: 1fr 1fr; gap: 28px; margin: 28px 0; } .aa-create, .aa-join { padding: 22px; border: 1px solid #e4eaf0; border-radius: 12px; } .aa-join { background: #f8fbfd; } .aa-join h3 { display: flex; align-items: center; gap: 8px; }
label { display: flex; flex-direction: column; gap: 8px; margin-bottom: 16px; font-size: 13px; color: #354d68; } input:not([type=checkbox]), select, textarea { width: 100%; min-width: 0; border: 1px solid #d5e0e8; border-radius: 8px; padding: 10px 12px; background: #fff; color: #173251; font: inherit; } textarea { resize: vertical; overflow-wrap: anywhere; } .aa-check { flex-direction: row; align-items: flex-start; gap: 8px; line-height: 1.7; } .aa-check input { margin-top: 4px; width: 15px; height: 15px; accent-color: #168b7c; }
.aa-fields { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; } .aa-create > small { display: block; margin-top: 12px; } .aa-account { margin-top: 28px; padding-top: 20px; border-top: 1px solid #dfe8ee; } .aa-preview { margin-top: 18px; } .aa-preview strong { display: block; margin-bottom: 14px; }
.aa-list-heading { justify-content: space-between; margin: 28px 0 12px; } .aa-list-heading h3 { margin: 0; } .aa-bill { padding: 22px; margin: 16px 0; border: 1px solid #dfe8ef; border-radius: 12px; } .aa-bill-heading h3 { margin-bottom: 4px; } .aa-bill-heading > strong { margin-left: auto; font-size: 22px; } .aa-state { font-size: 12px; background: #edf5f3; color: #267d6c; padding: 5px 10px; border-radius: 6px; }
.aa-progress { display: flex; gap: 24px; padding: 18px 0; font-size: 13px; color: #6c7e91; } .aa-progress b { color: #193552; } ul { list-style: none; padding: 0; margin: 0; } li { display: flex; align-items: center; gap: 16px; border-top: 1px solid #edf1f5; padding: 12px 0; flex-wrap: wrap; } li > div { min-width: 130px; } li > span { margin-left: auto; } .aa-invite { border-top: 1px solid #e6edf1; margin-top: 12px; padding-top: 18px; } .aa-invite .aa-check { margin-top: 18px; } .aa-pagination { display: flex; justify-content: flex-end; align-items: center; gap: 12px; font-size: 13px; } .aa-boundary { border-top: 1px solid #e6edf1; padding-top: 16px; }
@media(max-width: 900px) { .aa-workspace { grid-template-columns: 1fr; } } @media(max-width: 600px) { .aa-manager { padding: 18px; } .aa-create, .aa-join, .aa-bill { padding: 16px; } .aa-fields { grid-template-columns: 1fr; gap: 0; } .aa-progress { flex-direction: column; gap: 6px; } .aa-bill-heading > strong { margin-left: 0; } }
</style>
