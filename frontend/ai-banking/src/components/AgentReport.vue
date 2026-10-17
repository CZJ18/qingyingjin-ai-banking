<script setup lang="ts">
import { computed } from 'vue'
import { money } from '@/lib/utils'
import Chart from './Chart.vue'
import JsonDetails from './JsonDetails.vue'
import type { Schema } from '@/api/client'
const props = defineProps<{ output: Record<string, unknown> }>()
const data = computed(() => {
  const result = props.output.result
  return result && typeof result === 'object' ? (result as Record<string, unknown>) : props.output
})
const billing = computed(() =>
  'total_income' in data.value && 'category_breakdown' in data.value
    ? (data.value as Schema<'BillingReport'>)
    : null,
)
const planning = computed(() => {
  const result = data.value.result
  const candidate =
    result && typeof result === 'object' ? (result as Record<string, unknown>) : data.value
  return 'suggested_savings_minor' in candidate ? (candidate as Schema<'PlanningResult'>) : null
})
const subscription = computed(() =>
  'active_recurring_forecast' in data.value ? (data.value as Schema<'SubscriptionReport'>) : null,
)
const chart = computed(() => ({
  tooltip: { trigger: 'item' },
  legend: { bottom: 0, icon: 'circle' },
  series: [
    {
      type: 'pie',
      radius: ['48%', '70%'],
      center: ['50%', '43%'],
      label: { show: false },
      itemStyle: { borderColor: '#FFFFFF', borderWidth: 3, borderRadius: 4 },
      data:
        billing.value?.category_breakdown.map((c) => ({
          name: c.category,
          value: c.expense_minor / 100,
        })) || [],
    },
  ],
}))
const evidenceText = computed(() =>
  Array.isArray(props.output.evidence)
    ? props.output.evidence.map((e: Record<string, unknown>) => e.type + ' #' + e.id).join(' · ')
    : '',
)
</script>
<template>
  <div class="agent-report">
    <p v-if="output.explanation" class="report-explanation">{{ output.explanation }}</p>
    <template v-if="billing"
      ><div class="report-metrics">
        <div>
          <small>总收入</small><strong>{{ money(billing.total_income) }}</strong>
        </div>
        <div>
          <small>总支出</small><strong>{{ money(billing.total_expense) }}</strong>
        </div>
      </div>
      <Chart
        v-if="billing.category_breakdown.length"
        :option="chart"
        label="Agent 返回的真实消费分类"
      />
      <p v-for="f in billing.findings" :key="f.finding_id">{{ f.description }}</p></template
    ><template v-if="planning"
      ><div class="report-metrics">
        <div>
          <small>建议储蓄</small><strong>{{ money(planning.suggested_savings_minor) }}</strong>
        </div>
        <div>
          <small>必要支出预留</small><strong>{{ money(planning.necessary_reserve_minor) }}</strong>
        </div>
      </div>
      <p v-for="n in planning.review_notes" :key="n">{{ n }}</p>
      <div v-for="o in planning.options" :key="o.name" class="report-allocation">
        <b>{{ o.name }}</b
        ><span>{{ money(o.savings_minor) }}</span>
      </div></template
    ><template v-if="subscription"
      ><div class="report-metrics">
        <div>
          <small>月度订阅预测</small
          ><strong>{{ money(subscription.active_recurring_forecast.monthly_minor) }}</strong>
        </div>
        <div>
          <small>重复订阅线索</small><strong>{{ subscription.duplicate_groups.length }}</strong>
        </div>
      </div>
      <p v-for="(g, i) in subscription.duplicate_groups" :key="i">{{ g.reason }}</p>
      <p v-for="r in subscription.suggestions" :key="r.subscription_id">{{ r.reason }}</p></template
    >
    <div v-if="Array.isArray(output.evidence)" class="report-evidence">
      数据来源：{{ evidenceText }}
    </div>
    <JsonDetails :value="output" label="查看完整 Agent 输出与证据" />
  </div>
</template>
