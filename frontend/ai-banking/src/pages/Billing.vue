<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { Search, ChevronLeft, ChevronRight, ArrowUpRight, AlertTriangle, X } from 'lucide-vue-next'
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
import { request, type Schema } from '@/api/client'
import { period, currentMonth } from '@/lib/queries'
import { money, date } from '@/lib/utils'
import State from '@/components/State.vue'
import Button from '@/components/ui/button/Button.vue'
import JsonDetails from '@/components/JsonDetails.vue'
import ExpenseTrends from '@/components/billing/ExpenseTrends.vue'
import CategoryBreakdown from '@/components/billing/CategoryBreakdown.vue'
const s = useSession(),
  month = ref(currentMonth()),
  category = ref(''),
  search = ref(''),
  appliedSearch = ref(''),
  page = ref(0),
  detailId = ref(''),
  relatedIds = ref<string[]>([]),
  tableRoot = ref<HTMLElement>()
const start = ref(''),
  end = ref('')
function resetDates() {
  if (!/^\d{4}-\d{2}$/.test(month.value)) {
    start.value = ''
    end.value = ''
    return
  }
  start.value = month.value + '-01'
  end.value =
    month.value +
    '-' +
    String(
      new Date(Number(month.value.slice(0, 4)), Number(month.value.slice(5)), 0).getDate(),
    ).padStart(2, '0')
}
resetDates()
watch(month, resetDates)
const range = computed(() => {
  const from = Date.parse(start.value + 'T00:00:00+08:00'),
    to = Date.parse(end.value + 'T00:00:00+08:00') + 86400000
  return {
    from,
    to,
    valid:
      Number.isFinite(from) &&
      Number.isFinite(to) &&
      to > from &&
      start.value.startsWith(month.value) &&
      end.value.startsWith(month.value),
  }
})
const report = useQuery({
  queryKey: computed(() => ['billing-analysis', range.value.from, range.value.to, s.accountId]),
  enabled: computed(() => s.connected && range.value.valid),
  queryFn: ({ signal }) =>
    request<Schema<'BillingReport'>>('/api/v1/billing/generate_billing_report', {
      method: 'POST',
      signal,
      body: {
        from: new Date(range.value.from).toISOString(),
        to: new Date(range.value.to).toISOString(),
        timezone: period(month.value).timezone,
        limit: 100,
        offset: 0,
        ...(s.accountId ? { account_id: s.accountId } : {}),
      },
    }),
})
watch(
  () => report.data.value?.report_id,
  (id) => {
    if (id) s.saveBillingReport(id)
  },
)
watch([start, end, category, appliedSearch, () => s.accountId, relatedIds], () => (page.value = 0))
watch([start, end, () => s.accountId], () => (relatedIds.value = []))
const transactions = useQuery({
  queryKey: computed(() => [
    'billing-transactions',
    range.value.from,
    range.value.to,
    s.accountId,
    category.value,
    appliedSearch.value,
    page.value,
    relatedIds.value,
  ]),
  enabled: computed(() => s.connected && range.value.valid),
  queryFn: async ({ signal }) => {
    if (relatedIds.value.length) {
      const ids = relatedIds.value.slice(page.value * 10, page.value * 10 + 10)
      return {
        items: await Promise.all(
          ids.map((id) =>
            request<Schema<'Transaction'>>('/api/v1/transactions/' + encodeURIComponent(id), {
              signal,
            }),
          ),
        ),
        total: relatedIds.value.length,
        limit: 10,
        offset: page.value * 10,
      }
    }
    const q = new URLSearchParams({
      limit: '10',
      offset: String(page.value * 10),
      from: String(range.value.from),
      to: String(range.value.to),
    })
    if (s.accountId) q.set('bank_account_id', s.accountId)
    if (category.value) q.set('category', category.value)
    if (appliedSearch.value) q.set('merchant', appliedSearch.value)
    return request<Schema<'TransactionPage'>>('/api/v1/transactions?' + q, { signal })
  },
})
const detail = useQuery({
  queryKey: computed(() => ['transaction', detailId.value]),
  enabled: computed(() => s.connected && !!detailId.value),
  queryFn: ({ signal }) =>
    request<Schema<'Transaction'>>('/api/v1/transactions/' + encodeURIComponent(detailId.value), {
      signal,
    }),
})
const detailOpen = computed({
  get: () => !!detailId.value,
  set: (open: boolean) => {
    if (!open) detailId.value = ''
  },
})
const budgets = computed(
  () => report.data.value?.budget_status?.filter((b) => b.month === month.value) || [],
)
const budget = computed(() => budgets.value[0])
const anomalyIds = computed(() => [
  ...new Set(
    report.data.value?.anomalies?.flatMap((a) => a.evidence.map((t) => t.transaction_id)) || [],
  ),
])
const anomalies = computed(() => report.data.value?.anomalies || [])
const findings = computed(() => report.data.value?.findings || [])
const filterCategories = computed(() =>
  [
    ...new Set([
      ...(report.data.value?.category_breakdown || []).map((c) => c.category),
      ...(report.data.value?.transactions || []).map((t) => t.category),
    ]),
  ].filter(Boolean),
)
function findingExpense(ids: string[]) {
  const evidence = report.data.value?.transactions || []
  if (!ids.length || ids.some((id) => evidence.filter((t) => t.transaction_id === id).length !== 1))
    return undefined
  return evidence
    .filter((t) => ids.includes(t.transaction_id) && t.direction === 'expense')
    .reduce((sum, t) => sum + t.amount_minor, 0)
}
function fullDate(value?: number) {
  return value
    ? new Intl.DateTimeFormat('zh-CN', {
        timeZone: 'Asia/Shanghai',
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false,
      }).format(value) + ' UTC+8'
    : '—'
}
const overruns = computed(() =>
  budgets.value.flatMap((b) =>
    b.categories
      .filter((c) => c.spent_minor > c.allocated_minor)
      .map((c) => ({ ...c, budgetId: b.budget_id })),
  ),
)
function percent(spent: number, total: number) {
  return total > 0 ? ((spent / total) * 100).toFixed(0) + '%' : spent > 0 ? '未设额度' : '0%'
}
function width(spent: number, total: number) {
  return Math.max(0, Math.min(100, total > 0 ? (spent / total) * 100 : spent > 0 ? 100 : 0)) + '%'
}
async function scrollTable() {
  await nextTick()
  tableRoot.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}
function filterCategory(value: string) {
  relatedIds.value = []
  category.value = value
  search.value = ''
  appliedSearch.value = ''
  scrollTable()
}
function showRelated(ids: string[]) {
  category.value = ''
  search.value = ''
  appliedSearch.value = ''
  relatedIds.value = [...new Set(ids)]
  scrollTable()
}
function applySearch() {
  relatedIds.value = []
  appliedSearch.value = search.value
  page.value = 0
}
function clearRelated() {
  relatedIds.value = []
}
function categoryChanged() {
  relatedIds.value = []
}
function accountName(id: string) {
  return report.data.value?.accounts?.find((a) => a.id === id)?.display_name || id
}
const integrity = computed(() => {
  const r = report.data.value,
    warnings: string[] = []
  if (!r) return warnings
  const ids = r.transactions?.map((t) => t.transaction_id) || []
  if (new Set(ids).size !== ids.length) warnings.push('分析证据存在重复交易 ID')
  if ((r.category_breakdown || []).reduce((n, c) => n + c.expense_minor, 0) !== r.total_expense)
    warnings.push('分类支出合计与周期支出不一致')
  for (const grain of ['daily', 'weekly', 'monthly'] as const) {
    const rows = r[grain] || []
    if (
      rows.length &&
      (rows.reduce((n, d) => n + d.expense_minor, 0) !== r.total_expense ||
        rows.reduce((n, d) => n + d.income_minor, 0) !== r.total_income)
    )
      warnings.push(
        { daily: '日', weekly: '周', monthly: '月' }[grain] + '趋势合计与周期汇总不一致',
      )
  }
  const items = transactions.data.value?.items || []
  if (new Set(items.map((t) => t.id)).size !== items.length)
    warnings.push('当前分页存在重复交易 ID')
  if (!relatedIds.value.length) {
    if (
      items.some(
        (t) =>
          t.occurred_at < range.value.from ||
          t.occurred_at >= range.value.to ||
          (s.accountId && t.bank_account_id !== s.accountId),
      )
    )
      warnings.push('流水范围与账户筛选不一致')
    for (const direction of ['income', 'expense'] as const) {
      if (
        items.filter((t) => t.direction === direction).reduce((n, t) => n + t.amount_minor, 0) >
        (direction === 'income' ? r.total_income : r.total_expense)
      )
        warnings.push('当前分页' + (direction === 'income' ? '收入' : '支出') + '超过周期汇总')
    }
  }
  for (const b of budgets.value) {
    if (b.total_minor - b.spent_minor !== b.remaining_minor)
      warnings.push('预算剩余额度与已用金额不一致')
  }
  return [...new Set(warnings)]
})
</script>
<template>
  <div class="page billing-page">
    <div class="page-heading">
      <div>
        <h1>智能账单分析</h1>
        <p>支出结构、预算执行与每一笔资金记录。</p>
      </div>
      <label class="month-control"
        >账单月份<input type="month" v-model="month" aria-label="账单月份"
      /></label>
    </div>
    <div class="range-toolbar">
      <span class="simulation-label">模拟银行环境 · 非真实资金</span>
      <div>
        <label
          >开始日期<input
            type="date"
            v-model="start"
            :min="month + '-01'"
            :max="end"
            aria-label="开始日期" /></label
        ><span>—</span
        ><label
          >结束日期<input type="date" v-model="end" :min="start" aria-label="结束日期"
        /></label>
      </div>
    </div>
    <p v-if="!range.valid" class="range-error" role="alert">
      请选择同一账单月份内有效的开始和结束日期。
    </p>
    <div class="financial-summary" :aria-busy="report.isFetching.value">
      <div class="expense-metric">
        <span>周期支出</span><strong>{{ money(report.data.value?.total_expense) }}</strong
        ><small>{{ start }} — {{ end }} · {{ s.accountId ? '当前账户' : '全部账户' }}</small>
      </div>
      <div class="budget-metric">
        <span>剩余预算</span
        ><strong :class="{ negative: (budget?.remaining_minor ?? 0) < 0 }">{{
          money(budget?.remaining_minor)
        }}</strong>
        <div class="mini-budget">
          <div class="budget-track">
            <i :style="{ width: width(budget?.spent_minor ?? 0, budget?.total_minor ?? 0) }" />
          </div>
          <small>{{
            budget ? percent(budget.spent_minor, budget.total_minor) + ' 已用' : '尚无预算'
          }}</small>
        </div>
        <small>全部账户 · 完整 UTC 月</small>
      </div>
      <div class="supporting-metrics">
        <div>
          <span>周期收入</span><strong>{{ money(report.data.value?.total_income) }}</strong>
        </div>
        <button @click="showRelated(anomalyIds)" :disabled="!anomalyIds.length">
          <span>需核实交易 <ArrowUpRight :size="14" /></span
          ><strong>{{ report.data.value ? anomalyIds.length : '—' }}<small>笔</small></strong
          ><small>异常线索待核实，不代表损失</small>
        </button>
      </div>
    </div>
    <div v-if="integrity.length" class="integrity-warning" role="alert">
      <AlertTriangle :size="18" />
      <div>
        <b>数据口径需核实</b>
        <p>{{ integrity.join('；') }}。保留服务器原始金额，请核对数据来源。</p>
      </div>
    </div>
    <State
      :connected="s.connected && range.valid"
      :loading="report.isLoading.value"
      :error="report.error.value"
      @retry="report.refetch()"
    >
      <template v-if="report.data.value">
        <div class="billing-charts">
          <ExpenseTrends :report="report.data.value" /><CategoryBreakdown
            :categories="report.data.value.category_breakdown || []"
            :total="report.data.value.total_expense"
            @filter="filterCategory"
          />
        </div>
        <div class="analysis-grid">
          <section class="billing-surface budget-panel">
            <header>
              <h2>预算执行</h2>
              <span>全部账户 · UTC 整月</span>
            </header>
            <p class="scope-note">预算覆盖完整 UTC 月及全部账户，独立于上方账单日期和账户筛选。</p>
            <State
              :connected="true"
              :empty="!budgets.length"
              compact
              title="本月尚无预算"
              description="已有预算将按服务端的完整月口径展示。"
            >
              <div v-for="b in budgets" :key="b.budget_id" class="budget-group">
                <div class="budget-total">
                  <span>{{ b.month }} · 总预算</span
                  ><b
                    >{{ money(b.spent_minor) }} <span>/ {{ money(b.total_minor) }}</span></b
                  >
                </div>
                <button
                  v-for="c in b.categories"
                  :key="c.category"
                  class="budget-row"
                  @click="filterCategory(c.category)"
                >
                  <div>
                    <b>{{ c.category }}</b
                    ><span :class="{ negative: c.spent_minor > c.allocated_minor }"
                      >{{ percent(c.spent_minor, c.allocated_minor) }} <ArrowUpRight :size="13"
                    /></span>
                  </div>
                  <div class="budget-track">
                    <i
                      :class="{ over: c.spent_minor > c.allocated_minor }"
                      :style="{ width: width(c.spent_minor, c.allocated_minor) }"
                    />
                  </div>
                  <div>
                    <span
                      >{{ money(c.spent_minor) }}
                      <span class="muted">/ {{ money(c.allocated_minor) }}</span></span
                    ><small v-if="c.spent_minor > c.allocated_minor" class="negative"
                      >超出 {{ money(c.spent_minor - c.allocated_minor) }}</small
                    >
                  </div>
                </button>
              </div>
            </State>
          </section>
          <section class="billing-surface insights-panel">
            <header>
              <h2>消费洞察</h2>
              <span>有据可查的分析</span>
            </header>
            <State
              :connected="true"
              :empty="!findings.length && !overruns.length"
              compact
              title="暂无有效消费洞察"
              description="存在可追溯的分析或预算偏差时，在此展示。"
            >
              <article v-for="f in findings" :key="f.finding_id" class="insight-item">
                <p>{{ f.description }}</p>
                <p v-if="findingExpense(f.transaction_ids)" class="finding-amount">
                  关联支出 {{ money(findingExpense(f.transaction_ids)) }} ·
                  {{
                    report.data.value.total_expense > 0
                      ? (
                          (findingExpense(f.transaction_ids)! / report.data.value.total_expense) *
                          100
                        ).toFixed(1)
                      : '0.0'
                  }}% 的周期支出
                </p>
                <div>
                  <span>账单分析 · {{ f.transaction_ids.length }} 笔来源交易</span
                  ><button v-if="f.transaction_ids.length" @click="showRelated(f.transaction_ids)">
                    查看相关交易 <ArrowUpRight :size="14" />
                  </button>
                </div>
              </article>
              <article v-for="c in overruns" :key="c.budgetId + c.category" class="insight-item">
                <p>
                  <b>{{ c.category }}</b
                  >支出超过分类预算 <b>{{ money(c.spent_minor - c.allocated_minor) }}</b
                  >，已用 {{ percent(c.spent_minor, c.allocated_minor) }}。
                </p>
                <div>
                  <span>预算记录 · 全账户 UTC 月</span
                  ><button @click="filterCategory(c.category)">
                    核对当前账单 <ArrowUpRight :size="14" />
                  </button>
                </div>
              </article>
              <JsonDetails
                :value="{
                  report_id: report.data.value.report_id,
                  analysis_period: report.data.value.analysis_period,
                  evidence_transaction_ids: report.data.value.evidence_transaction_ids,
                }"
                label="查看分析数据来源"
              />
            </State>
          </section>
        </div>
        <section v-if="anomalies.length" class="review-panel">
          <header>
            <h2>交易核实提醒</h2>
            <span>{{ anomalies.length }} 条线索</span>
          </header>
          <div v-for="(a, i) in anomalies" :key="i" class="review-row">
            <AlertTriangle :size="17" />
            <div>
              <b>{{
                {
                  possible_duplicate: '可能重复交易',
                  category_unknown: '分类待核实',
                  large_amount_review: '大额交易核实',
                  unusual_amount: '金额异常线索',
                }[a.type]
              }}</b>
              <p>{{ a.rule }}</p>
              <JsonDetails :value="a.evidence" label="查看异常交易证据" />
            </div>
            <button
              v-if="a.evidence.length"
              @click="showRelated(a.evidence.map((t) => t.transaction_id))"
            >
              查看交易 <ArrowUpRight :size="14" />
            </button>
          </div>
        </section>
      </template>
    </State>
    <section ref="tableRoot" class="billing-surface transaction-table">
      <header>
        <div>
          <h2>交易流水</h2>
          <p>当前账单筛选 · {{ transactions.data.value?.total ?? 0 }} 笔</p>
        </div>
        <span>人民币 CNY</span>
      </header>
      <div v-if="relatedIds.length" class="related-context">
        <span>关联证据 · 按交易 ID 查询，可能包含账单范围外记录</span
        ><button @click="clearRelated">返回账单列表 <X :size="14" /></button>
      </div>
      <form class="billing-filters" @submit.prevent="applySearch">
        <label class="billing-search"
          ><Search :size="16" /><input
            v-model="search"
            placeholder="搜索商户名称"
            aria-label="搜索商户" /></label
        ><select v-model="category" @change="categoryChanged" aria-label="消费分类">
          <option value="">全部分类</option>
          <option v-for="c in filterCategories" :key="c" :value="c">
            {{ c }}
          </option></select
        ><Button variant="outline" type="submit">搜索</Button>
      </form>
      <State
        :connected="s.connected && range.valid"
        :loading="transactions.isLoading.value"
        :error="transactions.error.value"
        :empty="!transactions.data.value?.items.length"
        title="未找到交易记录"
        description="调整日期、分类或商户名称后重试。"
        @retry="transactions.refetch()"
        ><div class="table-scroll">
          <table>
            <thead>
              <tr>
                <th>交易 / 商户</th>
                <th>分类</th>
                <th>账户</th>
                <th>交易时间</th>
                <th class="text-right">金额</th>
                <th class="detail-column">详情</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(t, i) in transactions.data.value?.items" :key="t.id + '-' + i">
                <td>
                  <button class="merchant-button" @click="detailId = t.id">
                    {{ t.merchant_name || t.description || '银行交易' }}</button
                  ><small class="transaction-id">ID {{ t.id }}</small>
                </td>
                <td>{{ t.category || '未分类' }}</td>
                <td>{{ accountName(t.bank_account_id) }}</td>
                <td>{{ date(t.occurred_at) }}</td>
                <td class="text-right ledger-amount" :class="{ income: t.direction === 'income' }">
                  {{ t.direction === 'income' ? '+' : '−' }}{{ money(t.amount_minor) }}
                </td>
                <td class="detail-column">
                  <button
                    class="icon-button"
                    @click="detailId = t.id"
                    :aria-label="'查看交易 ' + t.id"
                  >
                    <ArrowUpRight :size="16" />
                  </button>
                </td>
              </tr>
            </tbody>
          </table></div
      ></State>
      <div class="pagination">
        <span>共 {{ transactions.data.value?.total ?? 0 }} 笔 · 第 {{ page + 1 }} 页</span>
        <div>
          <Button
            variant="outline"
            :disabled="page === 0 || transactions.isFetching.value"
            @click="page--"
            ><ChevronLeft :size="15" />上一页</Button
          ><Button
            variant="outline"
            :disabled="
              !transactions.data.value ||
              (page + 1) * 10 >= transactions.data.value.total ||
              transactions.isFetching.value
            "
            @click="page++"
            >下一页<ChevronRight :size="15"
          /></Button>
        </div>
      </div>
    </section>
    <DialogRoot v-model:open="detailOpen"
      ><DialogPortal
        ><DialogOverlay class="billing-dialog-overlay" /><DialogContent class="billing-dialog"
          ><div class="dialog-heading">
            <div>
              <DialogTitle>交易详情</DialogTitle
              ><DialogDescription>模拟银行交易记录 · 服务端数据</DialogDescription>
            </div>
            <DialogClose class="icon-button" aria-label="关闭交易详情"
              ><X :size="18"
            /></DialogClose>
          </div>
          <State
            :connected="s.connected"
            :loading="detail.isLoading.value"
            :error="detail.error.value"
            @retry="detail.refetch()"
            ><strong
              class="detail-amount"
              :class="{ income: detail.data.value?.direction === 'income' }"
              >{{ detail.data.value?.direction === 'income' ? '+' : '−'
              }}{{ money(detail.data.value?.amount_minor) }}</strong
            >
            <dl class="billing-detail-grid">
              <dt>交易 ID</dt>
              <dd>{{ detailId }}</dd>
              <dt>商户</dt>
              <dd>{{ detail.data.value?.merchant_name || '—' }}</dd>
              <dt>时间</dt>
              <dd>{{ fullDate(detail.data.value?.occurred_at) }}</dd>
              <dt>分类</dt>
              <dd>{{ detail.data.value?.category || '未分类' }}</dd>
              <dt>账户</dt>
              <dd>{{ accountName(detail.data.value?.bank_account_id || '') }}</dd>
              <dt>交易类型</dt>
              <dd>{{ detail.data.value?.kind }}</dd>
              <dt>描述</dt>
              <dd>{{ detail.data.value?.description || '—' }}</dd>
              <dt>审计编号</dt>
              <dd>{{ detail.data.value?.audit_id || '—' }}</dd>
            </dl>
            <JsonDetails
              :value="detail.data.value"
              label="查看完整交易记录" /></State></DialogContent></DialogPortal
    ></DialogRoot>
  </div>
</template>
<style scoped>
.billing-page {
  display: flex;
  flex-direction: column;
  gap: 20px;
  color: #172033;
}
.billing-page .page-heading {
  margin-bottom: 0;
}
.billing-page .page-heading h1 {
  font-size: 27px;
}
.billing-page .page-heading p {
  font-size: 14px;
  color: #596779;
}
.month-control {
  flex-direction: row;
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 14px;
}
.range-toolbar {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  align-items: center;
  font-size: 13px;
  color: #596779;
}
.simulation-label {
  padding: 5px 9px;
  background: #edf2f7;
  border: 1px solid #e1e8ef;
  border-radius: 5px;
  white-space: nowrap;
}
.range-toolbar > div,
.range-toolbar label {
  display: flex;
  align-items: center;
  gap: 8px;
}
.range-toolbar input {
  min-width: 0;
  font-size: 13px;
  height: 36px;
}
.range-error,
.negative {
  color: #b45309;
}
.financial-summary {
  display: grid;
  grid-template-columns: 1.1fr 1fr 0.9fr;
  background: #fff;
  border: 1px solid #e7edf3;
  border-radius: 12px;
  overflow: hidden;
}
.expense-metric,
.budget-metric {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 24px 28px;
}
.expense-metric {
  background: #f0f4f8;
  border-right: 1px solid #e7edf3;
}
.financial-summary span {
  font-size: 14px;
  color: #46556a;
}
.financial-summary strong {
  font-size: 28px;
  letter-spacing: -0.5px;
  font-weight: 600;
  line-height: 1.25;
  font-variant-numeric: tabular-nums;
}
.financial-summary small {
  font-size: 12px;
  line-height: 1.6;
  color: #596779;
}
.budget-metric > strong {
  color: #0d766e;
}
.budget-metric > strong.negative {
  color: #b45309;
}
.mini-budget {
  display: flex;
  gap: 12px;
  align-items: center;
}
.mini-budget .budget-track {
  flex: 1;
}
.mini-budget small {
  white-space: nowrap;
}
.budget-track {
  height: 5px;
  background: #e9eff3;
  border-radius: 4px;
  overflow: hidden;
}
.budget-track i {
  display: block;
  height: 100%;
  background: #0d9488;
  border-radius: 4px;
  transition: width 0.2s;
}
.budget-track i.over {
  background: #c38a37;
}
.supporting-metrics {
  border-left: 1px solid #e7edf3;
  display: grid;
  grid-template-columns: 1fr;
  padding: 20px 24px;
  gap: 16px;
}
.supporting-metrics > div,
.supporting-metrics > button {
  display: grid;
  gap: 5px;
  text-align: left;
}
.supporting-metrics > button {
  background: none;
  border: 0;
  padding: 0;
  cursor: pointer;
}
.supporting-metrics > button:disabled {
  cursor: default;
}
.supporting-metrics span {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}
.supporting-metrics strong {
  font-size: 21px;
}
.supporting-metrics strong small {
  font-size: 13px;
  margin-left: 6px;
}
.billing-charts {
  margin: 0;
  display: grid;
  grid-template-columns: minmax(0, 1.08fr) minmax(0, 1fr);
  gap: 20px;
}
.billing-surface {
  background: #fff;
  border: 1px solid #e7edf3;
  border-radius: 12px;
  min-width: 0;
  overflow: hidden;
}
.billing-surface > header,
.review-panel > header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  padding: 22px 24px 16px;
}
.billing-page h2 {
  font-size: 17px;
  font-weight: 600;
  margin: 0;
}
.billing-surface header span,
.review-panel header span {
  font-size: 13px;
  color: #596779;
}
.analysis-grid {
  align-items: start;
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 20px;
}
.scope-note {
  font-size: 12px;
  color: #596779;
  line-height: 1.7;
  padding: 0 24px;
  margin: 0 0 12px;
}
.budget-group {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: 32px;
  padding: 0 24px 16px;
}
.budget-total {
  grid-column: 1 / -1;
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 0 12px;
  font-size: 14px;
  border-bottom: 1px solid #e7edf3;
}
.budget-total b {
  font-weight: 500;
  font-variant-numeric: tabular-nums;
}
.budget-total b span,
.muted {
  color: #596779;
  font-size: 12px;
}
.budget-row {
  display: grid;
  gap: 8px;
  padding: 14px 0;
  background: none;
  border: 0;
  border-bottom: 1px solid #edf1f5;
  width: 100%;
  text-align: left;
  cursor: pointer;
}
.budget-row:hover {
  background: #f8fafc;
}
.budget-row > div:not(.budget-track) {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  font-size: 13px;
}
.budget-row b {
  font-size: 14px;
  font-weight: 500;
}
.budget-row > div > span {
  display: flex;
  align-items: center;
  gap: 4px;
}
.budget-row small {
  font-size: 12px;
  white-space: nowrap;
}
.insights-panel {
  padding-bottom: 20px;
}
.insight-item {
  margin: 0 24px;
  padding: 16px 0;
  border-bottom: 1px solid #e7edf3;
}
.insight-item p {
  font-size: 14px;
  line-height: 1.8;
  margin: 0 0 10px;
}
.insight-item .finding-amount {
  font-size: 13px;
  color: #46556a;
  font-variant-numeric: tabular-nums;
}
.insight-item > div {
  display: flex;
  gap: 8px;
  justify-content: space-between;
  flex-wrap: wrap;
}
.insight-item > div > span {
  font-size: 12px;
  color: #596779;
}
.insight-item button,
.review-row > button,
.related-context button {
  display: flex;
  gap: 4px;
  align-items: center;
  background: none;
  border: 0;
  font-size: 13px;
  color: #0d766e;
  cursor: pointer;
  padding: 0;
}
.insights-panel :deep(details) {
  margin: 16px 24px 0;
}
.review-panel {
  background: #fffaf1;
  border: 1px solid #ecdfc7;
  border-radius: 12px;
}
.review-row {
  display: flex;
  gap: 12px;
  align-items: flex-start;
  padding: 16px 24px;
  border-top: 1px solid #ecdfc7;
}
.review-row > svg {
  color: #a16b23;
  flex: none;
  margin-top: 3px;
}
.review-row > div {
  flex: 1;
  min-width: 0;
}
.review-row b {
  font-size: 14px;
  font-weight: 500;
}
.review-row p {
  font-size: 13px;
  line-height: 1.7;
  margin: 6px 0;
}
.review-row > button {
  white-space: nowrap;
  margin-top: 3px;
}
.integrity-warning {
  display: flex;
  gap: 12px;
  padding: 14px 18px;
  background: #fff7e9;
  border: 1px solid #e8d8b8;
  border-radius: 8px;
  font-size: 14px;
}
.integrity-warning svg {
  flex: none;
  color: #b45309;
}
.integrity-warning p {
  font-size: 13px;
  line-height: 1.7;
  margin: 4px 0 0;
}
.transaction-table {
  scroll-margin-top: 88px;
}
.transaction-table header p {
  font-size: 13px;
  color: #596779;
  margin: 6px 0 0;
}
.billing-filters {
  align-items: center;
  display: flex;
  gap: 12px;
  padding: 0 24px 20px;
}
.billing-search {
  margin: 0;
  height: 40px;
  flex-direction: row;
  display: flex;
  align-items: center;
  gap: 8px;
  border: 1px solid #dce4ec;
  border-radius: 7px;
  padding: 0 12px;
  max-width: 340px;
  flex: 1;
  min-width: 0;
  color: #64748b;
}
.billing-search input {
  border: 0;
  background: none;
  padding: 0;
  box-shadow: none;
  width: 100%;
  min-width: 0;
  font-size: 14px;
}
.billing-filters select {
  font-size: 14px;
  max-width: 180px;
}
.table-scroll {
  width: 100%;
  overflow-x: auto;
}
.transaction-table table {
  width: 100%;
  min-width: 780px;
  table-layout: auto;
  border-collapse: collapse;
}
.transaction-table th {
  background: #f7f9fb;
  color: #526176;
  font-size: 12px;
  font-weight: 500;
  padding: 14px 20px;
  white-space: nowrap;
}
.transaction-table td {
  border-bottom: 1px solid #edf1f5;
  padding: 16px 20px;
  font-size: 14px;
  color: #46556a;
  white-space: nowrap;
}
.transaction-table tbody tr {
  transition: background 0.18s;
}
.transaction-table tbody tr:hover {
  background: #f6f9fb;
}
.merchant-button {
  border: 0;
  background: none;
  padding: 0;
  text-align: left;
  color: #172033;
  font-size: 14px;
  font-weight: 500;
  cursor: pointer;
}
.transaction-table .transaction-id {
  display: block;
  font-size: 12px;
  color: #596779;
  max-width: 180px;
  overflow: hidden;
  text-overflow: ellipsis;
  margin-top: 5px;
}
.transaction-table .ledger-amount {
  font-size: 16px;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  color: #172033;
}
.income {
  color: #0d766e !important;
}
.detail-column {
  width: 50px;
  text-align: center;
}
.transaction-table .pagination {
  padding: 16px 24px;
  font-size: 13px;
  color: #596779;
  gap: 12px;
}
.related-context {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 24px 18px;
  color: #596779;
  font-size: 13px;
}
.billing-dialog-overlay {
  position: fixed;
  inset: 0;
  background: #142d4e55;
  z-index: 100;
}
.billing-dialog {
  position: fixed;
  left: 50%;
  top: 50%;
  transform: translate(-50%, -50%);
  width: min(540px, calc(100vw - 32px));
  max-height: calc(100dvh - 40px);
  overflow-y: auto;
  background: #fff;
  border-radius: 14px;
  padding: 24px;
  z-index: 101;
  box-shadow: 0 20px 60px #142d4e25;
}
.dialog-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 24px;
}
.dialog-heading :deep(h2) {
  font-size: 18px;
}
.dialog-heading :deep(p) {
  font-size: 13px;
  color: #596779;
  margin-top: 8px;
}
.detail-amount {
  display: block;
  font-size: 28px;
  letter-spacing: -0.5px;
  font-variant-numeric: tabular-nums;
  margin: 0 0 24px;
}
.billing-detail-grid {
  display: grid;
  grid-template-columns: 88px minmax(0, 1fr);
  gap: 16px;
  font-size: 14px;
  line-height: 1.6;
}
.billing-detail-grid dt {
  color: #596779;
}
.billing-detail-grid dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.billing-page button:focus-visible {
  outline: 2px solid #0d9488;
  outline-offset: 2px;
}
@media (max-width: 1100px) {
  .billing-charts {
    margin: 0;
    grid-template-columns: minmax(0, 1fr);
  }
  .expense-metric,
  .budget-metric {
    padding: 22px 20px;
  }
  .supporting-metrics {
    padding: 20px;
  }
  .financial-summary strong {
    font-size: 25px;
  }
  .supporting-metrics strong {
    font-size: 20px;
  }
  .range-toolbar {
    flex-wrap: wrap;
  }
  .analysis-grid {
    align-items: start;
    grid-template-columns: 1fr;
  }
}
@media (max-width: 700px) {
  .billing-page {
    display: flex;
    flex-direction: column;
    gap: 16px;
  }
  .billing-page .page-heading {
    align-items: flex-start;
    flex-wrap: wrap;
    gap: 16px;
  }
  .billing-page .page-heading {
    margin-bottom: 0;
  }
  .billing-page .page-heading h1 {
    font-size: 24px;
  }
  .range-toolbar > div {
    flex-wrap: wrap;
    gap: 8px;
  }
  .range-toolbar label {
    flex-direction: column;
    align-items: flex-start;
  }
  .range-toolbar > div > span {
    margin-top: 20px;
  }
  .financial-summary {
    grid-template-columns: 1fr 1fr;
  }
  .expense-metric,
  .budget-metric {
    padding: 20px 16px;
    gap: 10px;
  }
  .financial-summary strong {
    font-size: 24px;
  }
  .supporting-metrics {
    grid-column: 1/-1;
    border-left: 0;
    border-top: 1px solid #e7edf3;
    grid-template-columns: 1fr 1fr;
    padding: 16px;
    gap: 16px;
  }
  .supporting-metrics strong {
    font-size: 20px;
  }
  .expense-metric small {
    overflow-wrap: anywhere;
  }
  .billing-charts {
    margin: 0;
    grid-template-columns: 1fr;
    gap: 16px;
  }
  .billing-surface > header,
  .review-panel > header {
    padding: 20px 16px 14px;
  }
  .scope-note {
    padding: 0 16px;
  }
  .budget-group {
    grid-template-columns: 1fr;
    display: grid;
    column-gap: 32px;
    padding: 0 16px 16px;
  }
  .insight-item {
    margin: 0 16px;
  }
  .insights-panel :deep(details) {
    margin-left: 16px;
    margin-right: 16px;
  }
  .billing-filters {
    align-items: center;
    padding: 0 16px 16px;
    flex-wrap: wrap;
    gap: 8px;
  }
  .billing-search {
    margin: 0;
    height: 40px;
    flex-direction: row;
    flex-basis: 100%;
    max-width: none;
    height: 40px;
  }
  .billing-filters select {
    flex: 1;
    max-width: none;
  }
  .transaction-table .pagination {
    padding: 16px;
    flex-wrap: wrap;
  }
  .pagination > div {
    gap: 6px;
  }
  .review-row {
    padding: 16px;
    flex-wrap: wrap;
  }
  .related-context {
    padding: 10px 16px 16px;
    flex-wrap: wrap;
  }
  .billing-dialog {
    padding: 20px;
  }
  .billing-detail-grid {
    grid-template-columns: 72px minmax(0, 1fr);
    gap: 12px;
  }
}
</style>
