<script setup lang="ts">
import { computed, ref } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import type { Schema } from '@/api/client'
import { request } from '@/api/client'
import { useSession } from '@/lib/store'
import { period } from '@/lib/queries'
import { money } from '@/lib/utils'
import Chart from '@/components/Chart.vue'
import State from '@/components/State.vue'
const props = defineProps<{
  report?: Schema<'BillingReport'>
  loading: boolean
  error: Error | null
  month: string
  planningNote?: string
}>()
defineEmits<{ retry: [] }>()
const s = useSession(),
  mode = ref('cashflow'),
  granularity = ref<'daily' | 'weekly'>('daily')
const ledger = useQuery({
  queryKey: computed(() => ['overview-ledger', s.accountId]),
  enabled: computed(() => s.connected && !!s.accountId && mode.value === 'balance'),
  queryFn: ({ signal }) =>
    request<Schema<'LedgerEntry'>[]>(
      `/api/v1/accounts/${encodeURIComponent(s.accountId)}/ledger?limit=100&offset=0`,
      { signal },
    ),
})
const rows = computed(() => props.report?.[granularity.value] || [])
const changes = computed(() => {
  if (!/^\d{4}-(0[1-9]|1[0-2])$/.test(props.month)) return []
  const p = period(props.month),
    totals = new Map<string, number>()
  for (const entry of ledger.data.value || []) {
    if (entry.created_at < Date.parse(p.from) || entry.created_at >= Date.parse(p.to)) continue
    const day = new Intl.DateTimeFormat('sv-SE', {
      timeZone: 'Asia/Shanghai',
      month: '2-digit',
      day: '2-digit',
    }).format(entry.created_at)
    totals.set(day, (totals.get(day) || 0) + entry.delta_minor)
  }
  return [...totals].sort(([a], [b]) => a.localeCompare(b))
})
const option = computed(() => ({
  tooltip: { trigger: 'axis' },
  legend: { bottom: 0, icon: 'circle', textStyle: { fontSize: 13 } },
  grid: { left: 8, right: 16, top: 20, bottom: 44, containLabel: true },
  xAxis: {
    type: 'category',
    boundaryGap: mode.value === 'balance',
    data:
      mode.value === 'balance' ? changes.value.map(([day]) => day) : rows.value.map((d) => d.label),
    axisLabel: { fontSize: 13 },
  },
  yAxis: {
    type: 'value',
    axisLabel: {
      fontSize: 12,
      formatter: (v: number) => (Math.abs(v) >= 10000 ? `¥${(v / 10000).toFixed(1)}万` : `¥${v}`),
    },
  },
  series:
    mode.value === 'balance'
      ? [
          {
            name: '已入账余额变动',
            type: 'bar',
            barMaxWidth: 22,
            data: changes.value.map(([, v]) => ({
              value: v / 100,
              itemStyle: { color: v >= 0 ? '#0D9488' : '#142D4E', borderRadius: [3, 3, 0, 0] },
            })),
          },
        ]
      : [
          {
            name: '收入',
            type: 'line',
            smooth: false,
            symbol: 'circle',
            symbolSize: 5,
            showSymbol: rows.value.length < 8,
            lineStyle: { width: 2.5, color: '#0D9488' },
            itemStyle: { color: '#0D9488' },
            areaStyle: { color: '#0D9488', opacity: 0.06 },
            data: rows.value.map((d) => d.income_minor / 100),
          },
          {
            name: '支出',
            type: 'line',
            smooth: false,
            symbol: 'circle',
            symbolSize: 5,
            showSymbol: rows.value.length < 8,
            lineStyle: { width: 2, color: '#142D4E' },
            itemStyle: { color: '#142D4E' },
            data: rows.value.map((d) => d.expense_minor / 100),
          },
        ],
}))
const pie = computed(() => ({
  tooltip: { trigger: 'item' },
  series: [
    {
      type: 'pie',
      radius: ['60%', '80%'],
      center: ['50%', '50%'],
      label: { show: false },
      itemStyle: { borderColor: '#fff', borderWidth: 3, borderRadius: 4 },
      data:
        props.report?.category_breakdown?.map((c) => ({
          name: c.category,
          value: c.expense_minor / 100,
        })) || [],
    },
  ],
}))
function retry() {
  void ledger.refetch()
}
function refresh() {
  if (s.connected && s.accountId && mode.value === 'balance') void ledger.refetch()
}
defineExpose({ refresh })
</script>
<template>
  <div class="funds-trends">
    <div class="trend-main">
      <div class="trend-controls">
        <div class="view-tabs" role="group" aria-label="资金图表类型">
          <button :aria-pressed="mode === 'cashflow'" @click="mode = 'cashflow'">收入与支出</button
          ><button :aria-pressed="mode === 'balance'" @click="mode = 'balance'">
            账户余额变化
          </button>
        </div>
        <div v-if="mode === 'cashflow'" class="period-tabs" role="group" aria-label="趋势粒度">
          <button :aria-pressed="granularity === 'daily'" @click="granularity = 'daily'">日</button
          ><button :aria-pressed="granularity === 'weekly'" @click="granularity = 'weekly'">
            周
          </button>
        </div>
      </div>
      <State
        :connected="s.connected"
        :loading="mode === 'balance' ? ledger.isLoading.value : loading"
        :error="mode === 'balance' ? ledger.error.value : error"
        :empty="mode === 'balance' ? !changes.length : !rows.length"
        :title="mode === 'balance' && !s.accountId ? '请选择一个账户' : '所选月份暂无资金记录'"
        :description="
          mode === 'balance'
            ? '选择具体账户后，查看已入账分录产生的实际资金增减。'
            : '切换月份查看已有收支记录。'
        "
        @retry="mode === 'balance' ? retry() : $emit('retry')"
        ><Chart
          :option="option"
          :label="mode === 'balance' ? '账户已入账余额变动' : '账户收入与支出趋势'"
      /></State>
      <p v-if="mode === 'balance'" class="trend-scope">
        所选月份中最近 100 笔分录的资金增减；仅记录的账单不计入。
      </p>
      <div v-else-if="report?.findings?.length || planningNote" class="embedded-insight">
        <span>{{ planningNote ? '规划提醒' : '资金观察' }}</span>
        <p>{{ planningNote || report?.findings?.[0]?.description }}</p>
        <RouterLink to="/planning">查看资金规划</RouterLink>
      </div>
    </div>
    <div class="trend-categories">
      <header>
        <h3>消费分类</h3>
        <b>{{ money(report?.total_expense) }}</b>
      </header>
      <State
        :connected="s.connected"
        :loading="loading"
        :error="error"
        :empty="!report?.category_breakdown?.length"
        title="暂无消费分类"
        description="支出记录产生后展示分类占比。"
        @retry="$emit('retry')"
        ><Chart :option="pie" label="账户消费分类分布" />
        <div class="category-legend">
          <div v-for="(c, index) in report?.category_breakdown?.slice(0, 5)" :key="c.category">
            <i
              :style="{
                background: ['#0D9488', '#142D4E', '#7EA9BF', '#E4AF68', '#8996B3'][index],
              }"
            /><span>{{ c.category }}</span
            ><b>{{ money(c.expense_minor) }}</b>
          </div>
        </div>
        <RouterLink to="/billing">查看完整消费结构</RouterLink></State
      >
    </div>
  </div>
</template>
<style scoped>
.funds-trends {
  display: grid;
  grid-template-columns: minmax(0, 2.3fr) minmax(240px, 1fr);
  color: #172033;
}
.trend-main {
  padding: 0 24px 20px;
  min-width: 0;
}
.trend-controls {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  margin-bottom: 8px;
}
.view-tabs,
.period-tabs {
  display: flex;
  gap: 4px;
}
.view-tabs button,
.period-tabs button {
  border: 0;
  background: transparent;
  color: #64748b;
  padding: 8px 10px;
  border-radius: 6px;
  font-size: 14px;
  cursor: pointer;
  transition: background 0.18s;
}
.view-tabs button[aria-pressed='true'] {
  background: #eaf4f2;
  color: #0d766e;
  font-weight: 600;
}
.period-tabs {
  background: #f3f5f8;
  padding: 3px;
  border-radius: 7px;
}
.period-tabs button {
  padding: 4px 9px;
}
.period-tabs button[aria-pressed='true'] {
  background: #fff;
  color: #142d4e;
  box-shadow: 0 1px 3px #142d4e0c;
}
.trend-main :deep(.chart) {
  height: 252px;
}
.trend-categories {
  border-left: 1px solid #e7edf3;
  padding: 0 24px 20px;
  min-width: 0;
}
.trend-categories header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.trend-categories h3 {
  font-size: 14px;
  font-weight: 600;
  margin: 0;
}
.trend-categories header b {
  font-size: 16px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.trend-categories :deep(.chart) {
  height: 155px;
}
.category-legend {
  display: grid;
  gap: 10px;
}
.category-legend > div {
  display: flex;
  gap: 8px;
  align-items: center;
  font-size: 14px;
}
.category-legend i {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex: none;
}
.category-legend span {
  flex: 1;
  overflow-wrap: anywhere;
}
.category-legend b {
  font-weight: 500;
  font-variant-numeric: tabular-nums;
}
.trend-categories a {
  display: block;
  margin-top: 16px;
  color: #0d766e;
  font-size: 13px;
}
.embedded-insight {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  gap: 10px;
  padding: 12px 14px;
  background: #f5f8fa;
  border-radius: 8px;
  align-items: start;
  font-size: 13px;
  line-height: 1.6;
}
.embedded-insight > span {
  color: #0d766e;
  font-weight: 600;
}
.embedded-insight p {
  margin: 0;
  color: #475569;
}
.embedded-insight a {
  color: #0d766e;
  white-space: nowrap;
}
.trend-scope {
  font-size: 13px;
  color: #64748b;
  line-height: 1.6;
  margin: 12px 0 0;
}
.funds-trends :deep(.empty-state) {
  min-height: 220px;
  padding: 24px;
}
.funds-trends :deep(.empty-state strong),
.funds-trends :deep(.empty-state p) {
  font-size: 14px;
}
.funds-trends button:focus-visible {
  outline: 2px solid #0d9488;
  outline-offset: 2px;
}
@media (max-width: 1100px) {
  .trend-main,
  .trend-categories {
    padding-left: 20px;
    padding-right: 20px;
  }
  .embedded-insight {
    grid-template-columns: auto 1fr;
  }
  .embedded-insight a {
    grid-column: 2;
  }
  .funds-trends {
    grid-template-columns: minmax(0, 1.8fr) minmax(240px, 1fr);
  }
}
@media (max-width: 750px) {
  .funds-trends {
    grid-template-columns: 1fr;
  }
  .trend-categories {
    border-left: 0;
    border-top: 1px solid #e7edf3;
    padding-top: 20px;
  }
  .trend-controls {
    flex-wrap: wrap;
  }
  .trend-main :deep(.chart) {
    height: 230px;
  }
  .trend-categories :deep(.chart) {
    height: 180px;
  }
}
</style>
