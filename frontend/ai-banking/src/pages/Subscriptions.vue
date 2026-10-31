<script setup lang="ts">
import { ref, computed,watch } from 'vue'
import {useRoute} from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { Repeat2, ArrowUpRight, Search, Sparkles, CalendarClock } from 'lucide-vue-next'
import { useSession, useUI } from '@/lib/store'
import { api, request, post, type Schema } from '@/api/client'
import { money, date } from '@/lib/utils'
import State from '@/components/State.vue'
import Status from '@/components/Status.vue'
import Button from '@/components/ui/button/Button.vue'
import JsonDetails from '@/components/JsonDetails.vue'
import ExecutionDialog from '@/components/ExecutionDialog.vue'
import RenewalReminders from '@/components/RenewalReminders.vue'
import {useAccounts} from '@/lib/queries'
import {subscriptionReference,subscriptionForecastView} from '@/lib/subscription-scope'
const s = useSession(),
  ui = useUI(),
  search = ref(''),
  status = ref(''),
  detailId = ref(''),
  dialog = ref<InstanceType<typeof ExecutionDialog>>(),
  busy = ref(false)
const route=useRoute(),accounts=useAccounts(),listAccount=ref(s.accountId)
const reference=computed(()=>subscriptionReference(route.query))
const referenceDate=(raw:string)=>new Intl.DateTimeFormat('zh-CN',{timeZone:reference.value?.timezone||'Asia/Shanghai',year:'numeric',month:'2-digit',day:'2-digit'}).format(new Date(raw))
const accountName=(id:string)=>accounts.data.value?.find(a=>a.id===id)?.display_name||'账户 #'+id
watch(()=>[s.sessionEpoch,s.user?.id,s.accountId].join(':'),()=>{listAccount.value=s.accountId;detailId.value='';search.value='';status.value=''},{flush:'sync'})
watch(listAccount,()=>{detailId.value=''})
const subs = useQuery({
  queryKey: computed(()=>['subscriptions',s.sessionEpoch]),
  enabled: computed(() => s.connected),
  queryFn: ({ signal }) => api.subscriptions(signal),
})
const analysis = useQuery({
  queryKey: computed(()=>['subscription-analysis',s.sessionEpoch]),
  enabled: computed(() => s.connected),
  queryFn: () =>
    post<'/api/v1/subscriptions/analyze_subscription_cost', Schema<'SubscriptionReport'>>(
      '/api/v1/subscriptions/analyze_subscription_cost',
      {},
    ),
})
const detail = useQuery({
  queryKey: computed(() => ['subscription',s.sessionEpoch, detailId.value]),
  enabled: computed(() => s.connected && !!detailId.value),
  queryFn: ({ signal }) =>
    request<Schema<'SubscriptionDetail'>>('/api/v1/subscriptions/' + detailId.value, { signal }),
})
const filtered = computed(
  () =>
    subs.data.value?.filter(
      (t) =>
        (!listAccount.value || t.bank_account_id===listAccount.value) &&
        (!search.value || t.merchant_name.includes(search.value)) &&
        (!status.value || t.status === status.value),
    ) || [],
)
const forecasts=computed(()=>subscriptionForecastView(analysis.data.value))
async function detect() {
  if (busy.value) return
  busy.value = true
  try {
    await api.detectSubscriptions()
    await subs.refetch()
    await analysis.refetch()
    ui.notify('已完成订阅识别，请核实候选项目')
  } catch (e) {
    ui.notify((e as Error).message)
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <div class="page subscriptions-page">
    <div class="page-heading">
      <div>
        <h1>订阅与代扣管理</h1>
        <p>从近三年可用流水识别周、月、季、年度周期消费，核实后管理模拟订阅；历史不足时可能无法识别。</p>
      </div>
      <Button @click="detect" :disabled="!s.connected || busy"
        ><Sparkles :size="16" />{{ busy ? '识别中…' : '识别近期订阅' }}</Button
      >
    </div>
    <section class="subscription-scope panel" aria-label="订阅管理范围">
      <p v-if="reference">上一页参考查询：账户 #{{reference.accountId}}，{{referenceDate(reference.from)}} 至 {{referenceDate(reference.to)}}（{{reference.timezone}}，不含终点）。</p>
      <p><strong>管理口径：全部本人账户当前登记的订阅及已关联扣款证据。</strong>这里不按上一页账期筛选，不代表全部银行支出。</p>
      <p>续费提醒与顶部费用汇总采用上述口径；下方列表默认显示当前账户，可手动切换查看全部本人账户。不会自动重新识别、续费或取消订阅。</p>
      <RouterLink to="/assistant">返回助手继续此前填写 →</RouterLink>
    </section>
    <RenewalReminders />
    <h2 class="scope-heading">全部本人账户 · 条件性费用参考</h2>
    <div class="metric-strip">
      <div>
        <span>当前订阅</span
        ><strong
          >{{ subs.data.value?.filter((t) => t.status === 'active').length ?? '—'
          }}<em>项</em></strong
        ><small>已确认使用中的订阅</small>
      </div>
      <div>
        <span>全部活跃订阅确认停扣后的月度费用参考</span
        ><strong>{{ forecasts.activeMinor===null ? '暂无法估算' : money(forecasts.activeMinor) }}</strong
        ><small>无可估算活跃记录时保留未知，不代表实际费用为零</small>
      </div>
      <div>
        <span>重复订阅线索</span
        ><strong>{{ analysis.data.value?.duplicate_groups.length ?? '—' }}<em>组</em></strong
        ><small>需要用户进一步核实</small>
      </div>
      <div>
        <span>已取消订阅的月度费用参考</span
        ><strong class="teal">{{
          forecasts.cancelledMinor===null ? '暂无法估算' : money(forecasts.cancelledMinor)
        }}</strong
        ><small>条件估算，不是已节省金额，不增加当前余额</small>
      </div>
    </div>
    <div v-if="analysis.error.value" class="warning-note">
      {{ analysis.error.value.message
      }}<Button variant="ghost" @click="analysis.refetch()">重试分析</Button>
    </div>
    <div v-for="(group, i) in analysis.data.value?.duplicate_groups" :key="i" class="warning-note">
      <Repeat2 :size="18" />
      <div>
        <b>可能重复的订阅：{{ group.subscription_ids.join('、') }}</b>
        <p>{{ group.reason }}</p>
      </div>
    </div>
    <div class="subscription-layout">
      <section class="panel">
        <div class="section-heading">
          <h2>我的订阅</h2>
          <span class="small-muted">{{listAccount?'当前列表：'+accountName(listAccount)+'（#'+listAccount+'）':'当前列表：全部本人账户'}} · 模拟代扣管理</span>
        </div>
        <div class="filter-bar">
          <label>列表账户<select v-model="listAccount" aria-label="订阅列表账户范围"><option value="">全部本人账户</option><option v-for="account in accounts.data.value||[]" :key="account.id" :value="account.id">{{account.display_name}} · #{{account.id}}</option></select></label>
          <label class="search-field"
            ><Search :size="16" /><input
              v-model="search"
              placeholder="搜索订阅名称"
              aria-label="搜索订阅" /></label
          ><select v-model="status" aria-label="订阅状态">
            <option value="">全部状态</option>
            <option value="active">使用中</option>
            <option value="candidate">待核实</option>
            <option value="cancelled">本地已取消</option>
          </select>
        </div>
        <State
          :connected="s.connected"
          :loading="subs.isLoading.value"
          :error="subs.error.value"
          :empty="!filtered.length"
          title="暂无匹配的订阅"
          description="当前列表账户和筛选条件下没有匹配记录。可手动调整列表账户或筛选；不代表其他账户或未关联的真实费用不存在。"
          @retry="subs.refetch()"
          ><div v-for="t in filtered" :key="t.id" class="subscription-row">
            <span class="subscription-monogram">{{ t.merchant_name.slice(0, 1) }}</span>
            <div class="grow">
              <button class="plain-button" @click="detailId = t.id">
                <b>{{ t.merchant_name }}</b
                ><ArrowUpRight :size="14" /></button
              ><small
                >{{accountName(t.bank_account_id)}} · #{{t.bank_account_id}} · {{ t.category }} · {{ t.billing_interval }} ·
                {{ date(t.next_billing_at) }} 预计扣费</small
              >
            </div>
            <div class="subscription-price">
              <strong>{{ money(t.amount_minor) }}</strong
              ><Status :value="t.status" />
            </div>
            <Button
              v-if="t.status === 'candidate'"
              variant="outline"
              @click="
                dialog?.prepare('subscription_manage', {
                  subscription_id: t.id,
                  input: { expected_version: t.version, confirm_local_simulation: true },
                })
              "
              >核实并确认订阅</Button
            >
            <Button
              v-else
              variant="outline"
              :disabled="t.status === 'cancelled'"
              @click="
                dialog?.prepare('cancel_subscription', {
                  subscription_id: t.id,
                  expected_version: t.version,
                })
              "
              >{{ t.status === 'cancelled' ? '本地已取消' : '取消模拟订阅' }}</Button
            >
          </div></State
        >
      </section>
      <aside class="panel subscription-side">
        <CalendarClock :size="25" />
        <h2>即将扣费</h2>
        <p class="small-muted">全部本人账户的已确认模拟订阅，不受左侧列表筛选影响。</p>
        <State
          :connected="s.connected"
          :empty="!subs.data.value?.some((t) => t.status === 'active')"
          title="暂无预计扣费"
          description="使用中的订阅会按预计时间排列。"
          ><div
            v-for="t in subs.data.value
              ?.filter((t) => t.status === 'active')
              .sort((a, b) => a.next_billing_at - b.next_billing_at)
              .slice(0, 5)"
            :key="t.id"
            class="upcoming-row"
          >
            <b>{{ t.merchant_name }}</b
            ><strong>{{ money(t.amount_minor) }}</strong
            ><small>{{ date(t.next_billing_at) }} · 预计时间</small>
          </div></State
        >
        <div class="inspector-note">
          <p>预测不代表商户实际扣款承诺。取消前需核对服务端意向并授权。</p>
        </div>
      </aside>
    </div>
    <section v-if="detailId" class="panel transaction-detail">
      <div class="section-heading">
        <h2>订阅详情</h2>
        <Button variant="ghost" @click="detailId = ''">关闭详情</Button>
      </div>
      <State
        :connected="s.connected"
        :loading="detail.isLoading.value"
        :error="detail.error.value"
        @retry="detail.refetch()"
        ><h3>{{ detail.data.value?.subscription.merchant_name }}</h3>
        <dl class="detail-grid">
          <dt>三个月预测费用</dt>
          <dd>{{ money(detail.data.value?.forecast.three_month_minor) }}</dd>
          <dt>年度预测费用</dt>
          <dd>{{ money(detail.data.value?.forecast.annual_minor) }}</dd>
          <dt>使用情况</dt>
          <dd>{{ detail.data.value?.usage_evidence || '未知，需用户核实' }}</dd>
        </dl>
        <JsonDetails
          :value="detail.data.value?.payments"
          label="扣费记录与重复交易证据" /><JsonDetails
          :value="detail.data.value?.operation_history"
          label="操作历史"
      /></State>
    </section>
    <ExecutionDialog ref="dialog" />
  </div>
</template>
<style scoped>
.subscription-scope{padding:16px 20px;line-height:1.75;overflow-wrap:anywhere}.subscription-scope p{margin:6px 0;font-size:14px}.scope-heading{font-size:17px;margin:20px 0 12px}.metric-strip>div{min-width:0}.metric-strip strong{font-size:clamp(18px,2vw,28px);overflow-wrap:anywhere}.filter-bar label{min-width:0}.filter-bar select{max-width:100%}
</style>
