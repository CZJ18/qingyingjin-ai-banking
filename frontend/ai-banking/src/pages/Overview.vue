<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import {
  ArrowUpRight,
  ArrowDownLeft,
  ArrowRight,
  Wallet,
  ShieldCheck,
  Plus,
  RefreshCw,
  Search,
  X,
  Repeat2,
  Target,
  Network,
} from 'lucide-vue-next'
import {
  DialogRoot,
  DialogPortal,
  DialogOverlay,
  DialogContent,
  DialogTitle,
  DialogDescription,
  DialogClose,
} from 'reka-ui'
import { useSession } from '@/lib/store'
import { useAccounts, useSavings, useOperations, period, currentMonth } from '@/lib/queries'
import { api, request, type Schema } from '@/api/client'
import { money, date } from '@/lib/utils'
import State from '@/components/State.vue'
import Status from '@/components/Status.vue'
import Button from '@/components/ui/button/Button.vue'
import JsonDetails from '@/components/JsonDetails.vue'
import FundsTrends from '@/components/overview/FundsTrends.vue'
import ProductProfile from '@/components/overview/ProductProfile.vue'
const trends = ref<InstanceType<typeof FundsTrends>>()
const router = useRouter(),
  s = useSession(),
  accounts = useAccounts(),
  goals = useSavings(),
  operations = useOperations()
const thisMonth = currentMonth(),
  month = ref(thisMonth),
  search = ref(''),
  appliedSearch = ref(''),
  direction = ref(''),
  detailId = ref('')
function openJob(id: string) {
  s.selectedJob = id
  void router.push('/agents')
}
function billingQuery(selectedMonth: string) {
  return api.billing({
    ...period(selectedMonth),
    ...(s.accountId ? { account_id: s.accountId } : {}),
  })
}
const report = useQuery({
  queryKey: computed(() => ['overview-report', month.value, s.accountId]),
  enabled: computed(() => s.connected && /^\d{4}-(0[1-9]|1[0-2])$/.test(month.value)),
  queryFn: () => billingQuery(month.value),
})
const summary = useQuery({
  queryKey: computed(() => ['overview-report', thisMonth, s.accountId]),
  enabled: computed(() => s.connected),
  queryFn: () => billingQuery(thisMonth),
})
const tx = useQuery({
  queryKey: computed(() => ['recent-transactions', s.accountId, appliedSearch.value]),
  enabled: computed(() => s.connected),
  queryFn: ({ signal }) => {
    const q = new URLSearchParams({ limit: '5' })
    if (s.accountId) q.set('bank_account_id', s.accountId)
    if (appliedSearch.value) q.set('merchant', appliedSearch.value)
    return request<Schema<'TransactionPage'>>('/api/v1/transactions?' + q, { signal })
  },
})
const jobs = useQuery({
  queryKey: computed(() => ['recent-jobs', s.jobIds]),
  enabled: computed(() => s.connected && s.jobIds.length > 0),
  queryFn: () => Promise.all(s.jobIds.slice(0, 3).map((id) => api.job(id))),
})
const subscriptions = useQuery({
  queryKey: ['overview-subscriptions'],
  enabled: computed(() => s.connected),
  queryFn: ({ signal }) =>
    request<Schema<'SubscriptionReport'>>('/api/v1/subscriptions', { signal }),
})
const plan = useQuery({
  queryKey: computed(() => ['plan', s.planId]),
  enabled: computed(() => s.connected && !!s.planId),
  queryFn: ({ signal }) =>
    request<Schema<'FinancialPlanView'>>('/api/v1/financial-plans/' + s.planId, { signal }),
})
const detail = useQuery({
  queryKey: computed(() => ['transaction', detailId.value]),
  enabled: computed(() => s.connected && !!detailId.value),
  queryFn: ({ signal }) =>
    request<Schema<'Transaction'>>('/api/v1/transactions/' + detailId.value, { signal }),
})
const balance = computed(() =>
  accounts.data.value
    ?.filter((a) => !s.accountId || a.id === s.accountId)
    .reduce((n, a) => n + a.balance_minor, 0),
)
const account = computed(() => accounts.data.value?.find((a) => a.id === s.accountId))
const goalTotal = computed(() => goals.data.value?.reduce((n, g) => n + g.current_minor, 0))
const pending = computed(
  () =>
    operations.data.value?.filter((o) =>
      ['prepared', 'pending_authorization', 'authorized', 'pending_approval'].includes(o.status),
    ) || [],
)
const activeSubs = computed(
  () =>
    subscriptions.data.value?.subscriptions?.filter(
      (row) => row.subscription.status === 'active',
    ) || [],
)
const filteredTx = computed(
  () =>
    tx.data.value?.items.filter((t) => !direction.value || t.direction === direction.value) || [],
)
const operationNames: Record<string, string> = {
  transfer: '模拟转账',
  cancel_subscription: '订阅取消',
  savings_deposit: '目标金存入',
  savings_withdrawal: '目标金取出',
}
function searchTransactions() {
  appliedSearch.value = search.value.trim()
}
function refresh() {
  trends.value?.refresh()
  void accounts.refetch()
  void summary.refetch()
  if (/^\d{4}-(0[1-9]|1[0-2])$/.test(month.value)) void report.refetch()
  void tx.refetch()
  void goals.refetch()
  void subscriptions.refetch()
  void operations.refetch()
  if (s.planId) void plan.refetch()
  if (s.jobIds.length) void jobs.refetch()
}
</script>
<template>
  <div class="page banking-overview">
    <header class="dashboard-heading">
      <div>
        <span class="dashboard-date">{{
          new Intl.DateTimeFormat('zh-CN', {
            timeZone: 'Asia/Shanghai',
            month: 'long',
            day: 'numeric',
            weekday: 'long',
          }).format(new Date())
        }}</span>
        <h1>账户总览</h1>
        <p>{{ s.user?.display_name ? s.user.display_name + '，' : '' }}查看账户资金与近期业务。</p>
      </div>
      <div class="dashboard-actions">
        <Button
          variant="outline"
          :disabled="!s.connected || report.isFetching.value"
          @click="refresh"
          ><RefreshCw :size="15" />刷新</Button
        ><Button @click="router.push('/assistant')">发起金融任务<ArrowUpRight :size="15" /></Button>
      </div>
    </header>
    <ProductProfile />
    <section class="account-overview" aria-label="账户概览">
      <div class="primary-balance">
        <div class="balance-label"><Wallet :size="18" />当前账户可用余额<span>CNY</span></div>
        <div
          v-if="accounts.isLoading.value"
          class="balance-skeleton skeleton"
          aria-label="正在加载余额"
        />
        <strong v-else class="primary-amount">{{ s.connected ? money(balance) : '¥ —' }}</strong>
        <p>
          {{
            account
              ? account.display_name + ' · ' + account.masked_account_number
              : s.connected
                ? '全部账户余额合计'
                : '登录后查看可用资金'
          }}
        </p>
        <div class="primary-balance-footer">
          <span><ShieldCheck :size="13" />模拟银行账户</span
          ><RouterLink to="/security">转账与账户管理<ArrowUpRight :size="14" /></RouterLink>
        </div>
      </div>
      <div class="supporting-metrics">
        <div>
          <span>本月收入</span><strong>{{ money(summary.data.value?.total_income) }}</strong
          ><small>当前账户 · 本月账单</small>
        </div>
        <div>
          <span>本月支出</span><strong>{{ money(summary.data.value?.total_expense) }}</strong
          ><small>含已记录的消费与交易</small>
        </div>
        <div>
          <span>本月可用预算</span
          ><strong class="budget-amount">{{
            money(summary.data.value?.budget_status?.[0]?.available_after_reserve_minor)
          }}</strong
          ><small>{{
            summary.data.value?.budget_status?.length
              ? '全用户月预算 · 已扣必要预留'
              : '尚无预算记录'
          }}</small>
        </div>
        <div>
          <span>健康目标金余额</span><strong>{{ s.connected ? money(goalTotal) : '—' }}</strong
          ><RouterLink to="/goals">全部健康目标<ArrowRight :size="13" /></RouterLink>
        </div>
      </div>
    </section>
    <State
      v-if="accounts.error.value"
      :connected="s.connected"
      :error="accounts.error.value"
      @retry="accounts.refetch()"
    /><State
      v-if="summary.error.value"
      :connected="s.connected"
      :error="summary.error.value"
      @retry="summary.refetch()"
    /><State
      v-if="goals.error.value"
      :connected="s.connected"
      :error="goals.error.value"
      @retry="goals.refetch()"
    />
    <section class="dashboard-panel funds-panel">
      <header class="dashboard-section-heading">
        <div>
          <h2>资金趋势</h2>
          <p>收支、已入账资金变化与消费结构。</p>
        </div>
        <label class="month-control"
          >分析月份<input type="month" v-model="month" aria-label="趋势月份" required
        /></label>
      </header>
      <FundsTrends
        ref="trends"
        :report="report.data.value"
        :loading="report.isLoading.value"
        :error="report.error.value"
        :month="month"
        :planning-note="plan.data.value?.result.review_notes?.[0]"
        @retry="report.refetch()"
      />
      <div v-if="plan.error.value" class="inline-data-error" role="alert">
        资金规划暂不可用。<button @click="plan.refetch()">重新查询</button>
      </div>
    </section>
    <div class="business-grid">
      <section class="dashboard-panel recent-transactions">
        <header class="dashboard-section-heading">
          <div>
            <h2>最近交易</h2>
            <p>最新 5 笔记录，点击名称查看详情。</p>
          </div>
          <RouterLink to="/billing">全部流水<ArrowUpRight :size="15" /></RouterLink>
        </header>
        <form class="transaction-filters" @submit.prevent="searchTransactions">
          <label class="search-control"
            ><Search :size="16" /><input
              v-model="search"
              placeholder="搜索商户"
              aria-label="搜索最近交易商户"
            /><button type="submit" aria-label="查询最近交易">查询</button></label
          ><select v-model="direction" aria-label="筛选已加载交易方向">
            <option value="">全部收支</option>
            <option value="income">收入</option>
            <option value="expense">支出</option>
          </select>
        </form>
        <State
          :connected="s.connected"
          :loading="tx.isLoading.value"
          :error="tx.error.value"
          :empty="!filteredTx.length"
          title="暂无匹配交易"
          description="调整筛选，或前往账单页查看完整交易流水。"
          @retry="tx.refetch()"
          ><div class="transaction-table-wrap">
            <table class="dashboard-table">
              <thead>
                <tr>
                  <th>交易 / 商户</th>
                  <th>时间</th>
                  <th class="amount-column">金额</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="t in filteredTx" :key="t.id">
                  <td>
                    <div class="merchant-cell">
                      <span class="merchant-icon" :class="t.direction"
                        ><component
                          :is="t.direction === 'income' ? ArrowDownLeft : ArrowUpRight"
                          :size="16"
                      /></span>
                      <div>
                        <button @click="detailId = t.id">
                          {{ t.merchant_name || t.description || '银行交易' }}</button
                        ><span>{{ t.category }}</span>
                      </div>
                    </div>
                  </td>
                  <td class="transaction-time">{{ date(t.occurred_at) }}</td>
                  <td class="amount-column" :class="{ incoming: t.direction === 'income' }">
                    {{ t.direction === 'income' ? '+' : '−' }}{{ money(t.amount_minor) }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div></State
        >
        <div v-if="report.data.value?.anomalies?.length" class="business-notice">
          {{ report.data.value.anomalies.length }} 项交易线索待核查<RouterLink to="/billing"
            >查看异常提醒<ArrowRight :size="14"
          /></RouterLink>
        </div>
      </section>
      <section class="dashboard-panel pending-operations">
        <header class="dashboard-section-heading">
          <div>
            <h2>待确认金融操作</h2>
            <p>核对内容后单独授权。</p>
          </div>
          <span v-if="s.connected && operations.data.value" class="count-label">{{
            pending.length
          }}</span>
        </header>
        <State
          :connected="s.connected"
          :loading="operations.isLoading.value"
          :error="operations.error.value"
          :empty="!pending.length"
          title="暂无待确认操作"
          description="新的操作意向将在这里显示，账户资金不会自动执行。"
          @retry="operations.refetch()"
          ><RouterLink
            v-for="o in pending.slice(0, 3)"
            :key="o.id"
            to="/security"
            class="operation-row"
            ><div>
              <b>{{ operationNames[o.kind] || o.kind }}</b
              ><span>{{ date(o.created_at) }}</span>
            </div>
            <div>
              <strong>{{ money(o.amount_minor) }}</strong
              ><Status :value="o.status" /></div></RouterLink></State
        ><RouterLink to="/security" class="section-footer-link"
          >进入可信执行中心<ArrowRight :size="14"
        /></RouterLink>
      </section>
    </div>
    <div class="services-grid">
      <section class="dashboard-panel subscription-summary">
        <header class="dashboard-section-heading">
          <div>
            <h2>当前订阅</h2>
            <p>全部账户的固定订阅支出。</p>
          </div>
          <RouterLink to="/subscriptions" aria-label="管理订阅"><Repeat2 :size="18" /></RouterLink>
        </header>
        <State
          :connected="s.connected"
          :loading="subscriptions.isLoading.value"
          :error="subscriptions.error.value"
          :empty="!activeSubs.length"
          title="暂无已启用订阅"
          description="订阅识别与管理入口可在订阅页面查看。"
          @retry="subscriptions.refetch()"
          ><div class="subscription-total">
            <span>月度支出预测</span
            ><strong>{{
              money(subscriptions.data.value?.active_recurring_forecast.monthly_minor)
            }}</strong>
          </div>
          <RouterLink
            v-for="row in activeSubs.slice(0, 3)"
            :key="row.subscription.id"
            to="/subscriptions"
            class="service-row"
            ><div>
              <b>{{ row.subscription.merchant_name }}</b
              ><span>下次扣费 {{ date(row.subscription.next_billing_at) }}</span>
            </div>
            <strong>{{ money(row.subscription.amount_minor) }}</strong></RouterLink
          >
          <div
            v-if="subscriptions.data.value?.duplicate_groups?.length"
            class="subscription-notice"
          >
            <b>{{ subscriptions.data.value.duplicate_groups.length }} 组重复订阅线索</b>
            <p>{{ subscriptions.data.value.duplicate_groups[0]?.reason }}</p>
            <RouterLink to="/subscriptions"
              >核实订阅支出<ArrowRight :size="13"
            /></RouterLink></div></State
        ><RouterLink to="/subscriptions" class="section-footer-link"
          >查看全部订阅<ArrowRight :size="14"
        /></RouterLink>
      </section>
      <section class="dashboard-panel goals-summary">
        <header class="dashboard-section-heading">
          <div>
            <h2>储蓄目标进度</h2>
            <p>将健康生活计划落实到目标金。</p>
          </div>
          <RouterLink to="/goals" aria-label="查看健康目标"><Target :size="18" /></RouterLink>
        </header>
        <State
          :connected="s.connected"
          :loading="goals.isLoading.value"
          :error="goals.error.value"
          :empty="!goals.data.value?.length"
          title="暂无储蓄目标"
          description="设定目标金额，跟踪健康生活资金进度。"
          @retry="goals.refetch()"
          ><RouterLink
            v-for="g in goals.data.value?.slice(0, 3)"
            :key="g.id"
            to="/goals"
            class="savings-row"
            ><div>
              <b>{{ g.name }}</b
              ><span>{{ (g.progress_basis_points / 100).toFixed(1) }}%</span>
            </div>
            <div class="savings-progress">
              <i
                :style="{ width: Math.max(0, Math.min(100, g.progress_basis_points / 100)) + '%' }"
              />
            </div>
            <p>
              <strong>{{ money(g.current_minor) }}</strong
              ><span>目标 {{ money(g.target_minor) }}</span>
            </p></RouterLink
          ></State
        ><RouterLink to="/goals" class="section-footer-link"
          ><Plus :size="14" />创建健康目标金</RouterLink
        >
      </section>
      <section class="dashboard-panel jobs-summary">
        <header class="dashboard-section-heading">
          <div>
            <h2>近期金融任务</h2>
            <p>查看分析结果与执行进度。</p>
          </div>
          <RouterLink to="/agents" aria-label="进入协同中心"><Network :size="18" /></RouterLink>
        </header>
        <State
          :connected="s.connected"
          :loading="jobs.isLoading.value"
          :error="jobs.error.value"
          :empty="!jobs.data.value?.length"
          title="暂无金融任务"
          description="发起账单、订阅或资金规划任务后查看执行记录。"
          @retry="jobs.refetch()"
          ><button
            v-for="j in jobs.data.value"
            :key="j.job.id"
            class="overview-job-row"
            @click="openJob(j.job.id)"
          >
            <div>
              <b>金融协同任务</b
              ><span>{{ j.tasks.length }} 个步骤 · {{ date(j.job.created_at) }}</span>
            </div>
            <Status :value="j.job.status" /></button></State
        ><RouterLink to="/assistant" class="section-footer-link"
          >发起金融任务<ArrowRight :size="14"
        /></RouterLink>
      </section>
    </div>
    <DialogRoot
      :open="!!detailId"
      @update:open="
        (v) => {
          if (!v) detailId = ''
        }
      "
      ><DialogPortal
        ><DialogOverlay class="dialog-overlay" /><DialogContent
          class="dialog-content overview-transaction-dialog"
          ><DialogTitle>交易详情</DialogTitle
          ><DialogDescription>来自 Banking Service 的交易记录。</DialogDescription
          ><State
            :connected="s.connected"
            :loading="detail.isLoading.value"
            :error="detail.error.value"
            @retry="detail.refetch()"
            ><div v-if="detail.data.value" class="transaction-detail">
              <strong
                >{{ detail.data.value.direction === 'income' ? '+' : '−'
                }}{{ money(detail.data.value.amount_minor) }}</strong
              >
              <dl>
                <div>
                  <dt>商户</dt>
                  <dd>{{ detail.data.value.merchant_name || '—' }}</dd>
                </div>
                <div>
                  <dt>用途</dt>
                  <dd>{{ detail.data.value.description || '—' }}</dd>
                </div>
                <div>
                  <dt>时间</dt>
                  <dd>{{ date(detail.data.value.occurred_at) }}</dd>
                </div>
                <div>
                  <dt>分类</dt>
                  <dd>{{ detail.data.value.category }}</dd>
                </div>
                <div>
                  <dt>记账方式</dt>
                  <dd>
                    {{
                      detail.data.value.effect_mode === 'record_only'
                        ? '仅记录账单'
                        : '已应用模拟账户资金变化'
                    }}
                  </dd>
                </div>
              </dl>
              <JsonDetails :value="detail.data.value" label="查看服务器完整记录" /></div></State
          ><DialogClose class="dialog-x" aria-label="关闭交易详情"
            ><X :size="18" /></DialogClose></DialogContent></DialogPortal
    ></DialogRoot>
  </div>
</template>
<style scoped>
.banking-overview {
  padding: 24px 28px !important;
  max-width: 1640px;
  margin: auto;
  color: #172033;
  font-size: 14px;
}
.dashboard-heading {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 24px;
  margin-bottom: 24px;
}
.dashboard-date {
  font-size: 13px;
  color: #64748b;
  display: block;
  margin-bottom: 8px;
}
.dashboard-heading h1 {
  font-size: 28px;
  letter-spacing: -0.5px;
  font-weight: 650;
  margin: 0 0 8px;
}
.dashboard-heading p {
  font-size: 14px;
  color: #64748b;
  margin: 0;
}
.dashboard-actions {
  display: flex;
  gap: 10px;
}
.account-overview {
  display: grid;
  grid-template-columns: minmax(300px, 0.95fr) minmax(0, 1.65fr);
  background: #fff;
  border: 1px solid #e7edf3;
  border-radius: 12px;
  overflow: hidden;
  margin-bottom: 24px;
}
.primary-balance {
  background: #142d4e;
  color: #fff;
  padding: 28px 28px 22px;
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.balance-label {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  color: #dae4ee;
}
.balance-label span {
  margin-left: auto;
  font-size: 12px;
  border: 1px solid #ffffff30;
  padding: 3px 7px;
  border-radius: 4px;
}
.primary-amount {
  font-size: 40px;
  font-weight: 550;
  line-height: 1.3;
  letter-spacing: -1.4px;
  margin: 22px 0 10px;
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
}
.primary-balance > p {
  color: #c4d2e0;
  font-size: 13px;
  line-height: 1.7;
  margin: 0 0 24px;
  overflow-wrap: anywhere;
}
.primary-balance-footer {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  border-top: 1px solid #ffffff24;
  padding-top: 16px;
  margin-top: auto;
  font-size: 12px;
  align-items: center;
}
.primary-balance-footer span,
.primary-balance-footer a {
  display: flex;
  gap: 5px;
  align-items: center;
  color: #d9e5f0;
}
.primary-balance-footer a {
  color: #fff;
  font-size: 13px;
}
.balance-skeleton {
  height: 50px;
  margin: 20px 0;
  background: #ffffff30;
}
.supporting-metrics {
  display: grid;
  grid-template-columns: 1fr 1fr;
  min-width: 0;
}
.supporting-metrics > div {
  padding: 23px 28px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 9px;
}
.supporting-metrics > div:nth-child(odd) {
  border-right: 1px solid #e7edf3;
}
.supporting-metrics > div:nth-child(-n + 2) {
  border-bottom: 1px solid #e7edf3;
}
.supporting-metrics span {
  font-size: 14px;
  color: #475569;
}
.supporting-metrics strong {
  font-size: 27px;
  font-weight: 600;
  letter-spacing: -0.7px;
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
}
.supporting-metrics small,
.supporting-metrics a {
  font-size: 13px;
  color: #64748b;
}
.supporting-metrics a {
  display: flex;
  gap: 5px;
  align-items: center;
  color: #0d766e;
}
.supporting-metrics .budget-amount {
  color: #0d766e;
}
.dashboard-panel {
  border: 1px solid #e7edf3;
  border-radius: 12px;
  background: #fff;
  min-width: 0;
  overflow: hidden;
}
.funds-panel {
  margin-bottom: 24px;
}
.dashboard-section-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 22px 24px 20px;
}
.dashboard-section-heading h2 {
  font-size: 17px;
  margin: 0 0 7px;
  font-weight: 600;
  color: #172033;
}
.dashboard-section-heading p {
  font-size: 13px;
  color: #64748b;
  margin: 0;
  line-height: 1.6;
}
.dashboard-section-heading > a {
  display: flex;
  align-items: center;
  gap: 5px;
  color: #0d766e;
  font-size: 13px;
  white-space: nowrap;
}
.month-control {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 13px;
  color: #64748b;
}
.month-control input {
  border: 1px solid #e7edf3;
  background: #fff;
  color: #172033;
  font-size: 14px;
  border-radius: 7px;
  padding: 8px 10px;
  max-width: 160px;
}
.business-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.8fr) minmax(280px, 1fr);
  gap: 24px;
  margin-bottom: 24px;
  align-items: start;
}
.services-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 24px;
  align-items: start;
}
.transaction-filters {
  display: flex;
  gap: 12px;
  padding: 0 24px 16px;
}
.search-control {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  flex: 1;
  border: 1px solid #e7edf3;
  padding: 8px 10px;
  border-radius: 7px;
  color: #64748b;
}
.banking-overview .transaction-filters label.search-control {
  display: flex;
  flex-direction: row;
  height: 42px;
  margin: 0;
}
.transaction-filters select {
  height: 42px;
  align-self: center;
}
.search-control input {
  width: 100%;
  min-width: 0;
  background: transparent;
  border: 0 !important;
  outline: none;
  font-size: 14px;
  padding: 0 !important;
  box-shadow: none !important;
}
.search-control button {
  border: 0;
  background: transparent;
  color: #0d766e;
  cursor: pointer;
  font-size: 13px;
  white-space: nowrap;
}
.transaction-filters select {
  border: 1px solid #e7edf3;
  border-radius: 7px;
  padding: 8px 10px;
  background: #fff;
  font-size: 14px;
  color: #475569;
  max-width: 125px;
}
.transaction-table-wrap {
  overflow-x: auto;
}
.dashboard-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 14px;
  table-layout: fixed;
}
.dashboard-table th {
  padding: 10px 24px;
  background: #f8fafc;
  border-block: 1px solid #edf1f5;
  text-align: left;
  font-size: 12px;
  color: #64748b;
  font-weight: 500;
}
.dashboard-table th:first-child {
  width: 48%;
}
.dashboard-table td {
  padding: 14px 24px;
  border-bottom: 1px solid #edf1f5;
}
.dashboard-table tr:last-child td {
  border-bottom: 0;
}
.dashboard-table tr:hover td {
  background: #fafcfd;
}
.merchant-cell {
  display: flex;
  gap: 10px;
  align-items: center;
  min-width: 0;
}
.merchant-icon {
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  flex: none;
  background: #f1f5f9;
  color: #142d4e;
  border-radius: 8px;
}
.merchant-icon.income {
  background: #edf8f4;
  color: #0d9488;
}
.merchant-cell > div {
  min-width: 0;
  display: grid;
  gap: 4px;
}
.merchant-cell button {
  border: 0;
  padding: 0;
  background: transparent;
  font-size: 14px;
  font-weight: 500;
  color: #172033;
  text-align: left;
  cursor: pointer;
  overflow-wrap: anywhere;
}
.merchant-cell button:hover {
  color: #0d766e;
}
.merchant-cell span:not(.merchant-icon) {
  font-size: 12px;
  color: #64748b;
}
.transaction-time {
  font-size: 13px;
  color: #64748b;
}
.dashboard-table .amount-column {
  text-align: right;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  font-weight: 550;
}
.dashboard-table .incoming {
  color: #0d766e;
}
.count-label {
  padding: 5px 8px;
  font-size: 13px;
  background: #f2f5f8;
  border-radius: 5px;
  color: #475569;
}
.operation-row,
.service-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 16px 24px;
  border-top: 1px solid #edf1f5;
  transition: background 0.18s;
}
.operation-row:hover,
.service-row:hover {
  background: #fafcfd;
}
.operation-row > div,
.service-row > div {
  display: grid;
  gap: 8px;
  min-width: 0;
}
.operation-row b,
.service-row b {
  font-size: 14px;
  color: #172033;
  font-weight: 500;
  overflow-wrap: anywhere;
}
.operation-row span,
.service-row span {
  font-size: 12px;
  color: #64748b;
}
.operation-row > div:last-child {
  justify-items: end;
}
.operation-row strong,
.service-row strong {
  font-size: 15px;
  font-weight: 600;
  color: #172033;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.section-footer-link {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  border-top: 1px solid #edf1f5;
  padding: 14px 16px;
  font-size: 14px;
  color: #0d766e;
  background: #fcfdfe;
}
.subscription-total {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 0 24px 18px;
}
.subscription-total span {
  font-size: 14px;
  color: #64748b;
}
.subscription-total strong {
  font-size: 24px;
  font-weight: 600;
  color: #142d4e;
  font-variant-numeric: tabular-nums;
}
.subscription-notice {
  margin: 12px 20px 20px;
  padding: 14px;
  background: #fffaf0;
  border-radius: 8px;
  color: #92400e;
  font-size: 13px;
  line-height: 1.7;
}
.subscription-notice b {
  font-weight: 600;
}
.subscription-notice p {
  margin: 8px 0;
}
.subscription-notice a {
  display: flex;
  align-items: center;
  gap: 5px;
  color: #92400e;
}
.savings-row {
  display: block;
  padding: 16px 24px;
  border-top: 1px solid #edf1f5;
}
.savings-row > div:first-child,
.savings-row p {
  display: flex;
  justify-content: space-between;
  gap: 10px;
  align-items: center;
}
.savings-row b {
  font-size: 14px;
  font-weight: 500;
  color: #172033;
}
.savings-row span {
  font-size: 12px;
  color: #64748b;
}
.savings-progress {
  height: 5px;
  background: #edf1f5;
  border-radius: 4px;
  margin: 12px 0;
  overflow: hidden;
}
.savings-progress i {
  height: 100%;
  display: block;
  background: #0d9488;
  border-radius: 4px;
}
.savings-row p {
  margin: 0;
}
.savings-row strong {
  font-size: 14px;
  color: #142d4e;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
}
.overview-job-row {
  display: flex;
  width: 100%;
  align-items: center;
  gap: 10px;
  justify-content: space-between;
  padding: 16px 24px;
  border: 0;
  border-top: 1px solid #edf1f5;
  background: #fff;
  cursor: pointer;
  text-align: left;
}
.overview-job-row:hover {
  background: #fafcfd;
}
.overview-job-row > div {
  display: grid;
  gap: 8px;
  min-width: 0;
}
.overview-job-row b {
  font-size: 14px;
  font-weight: 500;
  color: #172033;
}
.overview-job-row div > span {
  font-size: 12px;
  color: #64748b;
}
.business-notice {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  background: #fffaf0;
  color: #92400e;
  font-size: 13px;
  padding: 14px 24px;
}
.business-notice a {
  display: flex;
  align-items: center;
  gap: 5px;
}
.banking-overview :deep(.empty-state) {
  min-height: 190px;
  padding: 24px;
  gap: 10px;
}
.banking-overview :deep(.empty-state strong),
.banking-overview :deep(.empty-state p) {
  font-size: 14px;
}
.banking-overview :deep(.skeleton-group) {
  margin: 20px 24px;
}
.banking-overview :deep(.status) {
  font-size: 12px;
  white-space: nowrap;
}
.inline-data-error {
  padding: 12px 24px;
  color: #b91c1c;
  font-size: 14px;
  border-top: 1px solid #edf1f5;
}
.inline-data-error button {
  border: 0;
  background: transparent;
  color: inherit;
  text-decoration: underline;
  cursor: pointer;
  margin-left: 8px;
}
.overview-transaction-dialog {
  font-size: 14px;
}
.transaction-detail > strong {
  font-size: 32px;
  color: #142d4e;
  font-variant-numeric: tabular-nums;
  display: block;
  margin: 24px 0;
}
.transaction-detail dl > div {
  display: flex;
  justify-content: space-between;
  gap: 24px;
  padding: 12px 0;
  border-bottom: 1px solid #e7edf3;
}
.transaction-detail dt {
  color: #64748b;
  white-space: nowrap;
}
.transaction-detail dd {
  margin: 0;
  color: #172033;
  text-align: right;
  overflow-wrap: anywhere;
}
.transaction-detail :deep(pre) {
  white-space: pre-wrap;
}
.banking-overview button:focus-visible,
.banking-overview a:focus-visible {
  outline: 2px solid #0d9488;
  outline-offset: 3px;
}
@media (max-width: 1300px) {
  .banking-overview {
    padding: 24px !important;
  }
  .primary-balance {
    padding: 24px;
  }
  .primary-amount {
    font-size: 35px;
  }
  .supporting-metrics > div {
    padding: 22px 24px;
  }
  .supporting-metrics strong {
    font-size: 25px;
  }
  .dashboard-section-heading {
    padding: 20px;
  }
  .transaction-filters {
    padding: 0 20px 16px;
  }
  .dashboard-table th,
  .dashboard-table td {
    padding-left: 20px;
    padding-right: 20px;
  }
  .services-grid,
  .business-grid {
    gap: 20px;
  }
  .operation-row,
  .service-row,
  .savings-row,
  .overview-job-row {
    padding-left: 20px;
    padding-right: 20px;
  }
  .primary-balance-footer {
    align-items: flex-start;
    flex-wrap: wrap;
  }
  .dashboard-table th:first-child {
    width: 44%;
  }
  .dashboard-table th:nth-child(2) {
    width: 26%;
  }
  .dashboard-table th:last-child {
    width: 30%;
  }
}
@media (max-width: 1050px) {
  .account-overview {
    grid-template-columns: minmax(260px, 0.85fr) minmax(0, 1.3fr);
  }
  .primary-amount {
    font-size: 32px;
  }
  .supporting-metrics > div {
    padding: 20px;
  }
  .supporting-metrics strong {
    font-size: 23px;
  }
  .business-grid {
    grid-template-columns: minmax(0, 1.5fr) minmax(250px, 1fr);
  }
  .services-grid {
    grid-template-columns: 1fr 1fr;
  }
  .jobs-summary {
    grid-column: 1/-1;
  }
  .dashboard-table th,
  .dashboard-table td {
    padding-left: 12px;
    padding-right: 12px;
  }
  .transaction-time {
    font-size: 12px;
  }
}
@media (max-width: 750px) {
  .banking-overview {
    padding: 20px 16px !important;
  }
  .dashboard-heading {
    align-items: flex-start;
    gap: 16px;
    flex-direction: column;
  }
  .dashboard-heading h1 {
    font-size: 25px;
  }
  .dashboard-actions {
    width: 100%;
  }
  .account-overview {
    grid-template-columns: 1fr;
  }
  .primary-amount {
    font-size: 38px;
  }
  .supporting-metrics > div {
    padding: 20px;
  }
  .supporting-metrics strong {
    font-size: 23px;
  }
  .business-grid,
  .services-grid {
    grid-template-columns: 1fr;
    gap: 16px;
  }
  .jobs-summary {
    grid-column: auto;
  }
  .month-control {
    gap: 6px;
    font-size: 12px;
  }
  .month-control input {
    max-width: 138px;
    padding: 8px;
  }
  .dashboard-section-heading {
    gap: 12px;
  }
  .dashboard-section-heading h2 {
    font-size: 16px;
  }
  .dashboard-table th:first-child {
    width: 45%;
  }
  .dashboard-table th:nth-child(2) {
    width: 24%;
  }
  .dashboard-table th:last-child {
    width: 31%;
  }
  .merchant-icon {
    display: none;
  }
  .dashboard-table td {
    padding: 14px 10px;
  }
  .dashboard-table .amount-column {
    font-size: 13px;
  }
  .transaction-time {
    font-size: 12px;
  }
  .business-notice {
    padding: 12px 16px;
  }
  .transaction-filters {
    gap: 8px;
  }
  .supporting-metrics small {
    font-size: 12px;
  }
}
</style>
