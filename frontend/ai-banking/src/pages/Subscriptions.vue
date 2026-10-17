<script setup lang="ts">
import { ref, computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { Repeat2, ArrowUpRight, Search, Sparkles, CalendarClock } from 'lucide-vue-next'
import { useSession, useUI } from '@/lib/store'
import { api, request, post, type Schema } from '@/api/client'
import { money, date } from '@/lib/utils'
import { period, currentMonth } from '@/lib/queries'
import State from '@/components/State.vue'
import Status from '@/components/Status.vue'
import Button from '@/components/ui/button/Button.vue'
import JsonDetails from '@/components/JsonDetails.vue'
import ExecutionDialog from '@/components/ExecutionDialog.vue'
import RenewalReminders from '@/components/RenewalReminders.vue'
const s = useSession(),
  ui = useUI(),
  search = ref(''),
  status = ref(''),
  detailId = ref(''),
  dialog = ref<InstanceType<typeof ExecutionDialog>>(),
  busy = ref(false)
const subs = useQuery({
  queryKey: ['subscriptions'],
  enabled: computed(() => s.connected),
  queryFn: ({ signal }) => api.subscriptions(signal),
})
const analysis = useQuery({
  queryKey: ['subscription-analysis'],
  enabled: computed(() => s.connected),
  queryFn: () =>
    post<'/api/v1/subscriptions/analyze_subscription_cost', Schema<'SubscriptionReport'>>(
      '/api/v1/subscriptions/analyze_subscription_cost',
      {},
    ),
})
const detail = useQuery({
  queryKey: computed(() => ['subscription', detailId.value]),
  enabled: computed(() => s.connected && !!detailId.value),
  queryFn: ({ signal }) =>
    request<Schema<'SubscriptionDetail'>>('/api/v1/subscriptions/' + detailId.value, { signal }),
})
const filtered = computed(
  () =>
    subs.data.value?.filter(
      (t) =>
        (!search.value || t.merchant_name.includes(search.value)) &&
        (!status.value || t.status === status.value),
    ) || [],
)
async function detect() {
  if (busy.value) return
  busy.value = true
  try {
    await post<'/api/v1/subscriptions/detect_subscriptions', Schema<'SubscriptionReport'>>(
      '/api/v1/subscriptions/detect_subscriptions',
      { ...period(currentMonth()) },
    )
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
        <p>让持续的小额支出，成为可控的生活选择。</p>
      </div>
      <Button @click="detect" :disabled="!s.connected || busy"
        ><Sparkles :size="16" />{{ busy ? '识别中…' : '识别本月订阅' }}</Button
      >
    </div>
    <RenewalReminders />
    <div class="metric-strip">
      <div>
        <span>当前订阅</span
        ><strong
          >{{ subs.data.value?.filter((t) => t.status === 'active').length ?? '—'
          }}<em>项</em></strong
        ><small>已确认使用中的订阅</small>
      </div>
      <div>
        <span>预计每月支出</span
        ><strong>{{ money(analysis.data.value?.active_recurring_forecast.monthly_minor) }}</strong
        ><small>服务端按周期折算</small>
      </div>
      <div>
        <span>重复订阅线索</span
        ><strong>{{ analysis.data.value?.duplicate_groups.length ?? '—' }}<em>组</em></strong
        ><small>需要用户进一步核实</small>
      </div>
      <div>
        <span>取消后预计避免支出</span
        ><strong class="teal">{{
          money(analysis.data.value?.cancelled_avoided_spend.monthly_minor)
        }}</strong
        ><small>预计值，不增加当前余额</small>
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
          <span class="small-muted">模拟代扣管理</span>
        </div>
        <div class="filter-bar">
          <label class="search-field"
            ><Search :size="16" /><input
              v-model="search"
              placeholder="搜索订阅名称"
              aria-label="搜索订阅" /></label
          ><select v-model="status" aria-label="订阅状态">
            <option value="">全部状态</option>
            <option value="active">使用中</option>
            <option value="candidate">待核实</option>
            <option value="cancelled">已取消</option>
          </select>
        </div>
        <State
          :connected="s.connected"
          :loading="subs.isLoading.value"
          :error="subs.error.value"
          :empty="!filtered.length"
          title="暂无匹配的订阅"
          description="调整搜索词或状态筛选，查看其他代扣记录。"
          @retry="subs.refetch()"
          ><div v-for="t in filtered" :key="t.id" class="subscription-row">
            <span class="subscription-monogram">{{ t.merchant_name.slice(0, 1) }}</span>
            <div class="grow">
              <button class="plain-button" @click="detailId = t.id">
                <b>{{ t.merchant_name }}</b
                ><ArrowUpRight :size="14" /></button
              ><small
                >{{ t.category }} · {{ t.billing_interval }} ·
                {{ date(t.next_billing_at) }} 预计扣费</small
              >
            </div>
            <div class="subscription-price">
              <strong>{{ money(t.amount_minor) }}</strong
              ><Status :value="t.status" />
            </div>
            <Button
              variant="outline"
              :disabled="t.status === 'cancelled'"
              @click="
                dialog?.prepare('cancel_subscription', {
                  subscription_id: t.id,
                  expected_version: t.version,
                })
              "
              >{{ t.status === 'cancelled' ? '已取消' : '取消订阅' }}</Button
            >
          </div></State
        >
      </section>
      <aside class="panel subscription-side">
        <CalendarClock :size="25" />
        <h2>即将扣费</h2>
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
