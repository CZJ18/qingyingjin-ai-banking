<script setup lang="ts">
import { computed } from 'vue'
import { ArrowUpRight } from 'lucide-vue-next'
import type { Schema } from '@/api/client'
import { preparedOperationId, type TaskRow } from '@/components/agents/task-view'
import Chart from '@/components/Chart.vue'
import JsonDetails from '@/components/JsonDetails.vue'
import { money } from '@/lib/utils'
const props = defineProps<{ task: TaskRow }>()
defineEmits<{ inspect: [id: string] }>()
const operationId = computed(() => preparedOperationId(props.task.output))
const record = (v: unknown): Record<string, unknown> =>
  v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : {}
const data = computed(() => {
  const result = record(props.task.output?.result)
  const nested = record(result.result)
  return Object.keys(nested).length && 'suggested_savings_minor' in nested
    ? nested
    : Object.keys(result).length
      ? result
      : record(props.task.output)
})
const billing = computed(() =>
  'category_breakdown' in data.value ? (data.value as Schema<'BillingReport'>) : null,
)
const subscription = computed(() =>
  'active_recurring_forecast' in data.value ? (data.value as Schema<'SubscriptionReport'>) : null,
)
const planning = computed(() =>
  'suggested_savings_minor' in data.value ? (data.value as Schema<'PlanningResult'>) : null,
)
const savings = computed(() =>
  'target_minor' in data.value && 'current_minor' in data.value
    ? (data.value as Schema<'SavingsGoal'>)
    : null,
)
const lifeGoal = computed(() =>
  'goal' in data.value && 'version' in data.value && 'text' in record(data.value.version)
    ? (data.value as Schema<'LifeGoalView'>)
    : null,
)
const lifestyle = computed(() =>
  Array.isArray(record(record(data.value.plan).result).options) &&
  Array.isArray(data.value.recommendations)
    ? (data.value as Schema<'LifestylePlanView'>)
    : null,
)
const optionNames: Record<string, string> = {
  conservative: '保守方案',
  balanced: '平衡方案',
  active: '积极储蓄方案',
}
const pendingActions = computed(() =>
  Array.isArray(props.task.output?.next_actions)
    ? (props.task.output.next_actions.filter(
        (a: Record<string, unknown>) => a.requires_confirmation,
      ) as Record<string, unknown>[])
    : [],
)
const destination = computed(() =>
  billing.value
    ? '/billing'
    : subscription.value
      ? '/subscriptions'
      : planning.value
        ? '/planning'
        : lifestyle.value
          ? '/goals'
          : props.task.agent_type === 'savings'
            ? '/goals'
            : '/agents',
)
const title = computed(() =>
  billing.value
    ? '财务分析摘要'
    : subscription.value
      ? '订阅支出与优化建议'
      : planning.value
        ? '资金规划方案'
        : lifestyle.value
          ? '生活目标资金方案'
          : operationId.value
            ? '已准备的金融操作意向'
            : props.task.agent_type === 'supervisor'
              ? '任务解析与执行计划'
              : props.task.agent_type === 'savings'
                ? '储蓄目标与约束'
                : '业务处理结果',
)
const action = computed(() =>
  billing.value
    ? '查看交易明细'
    : subscription.value
      ? '管理订阅'
      : planning.value
        ? '查看资金方案'
        : lifestyle.value
          ? '查看生活目标'
          : props.task.agent_type === 'savings'
            ? '查看健康目标金'
            : '查看执行详情',
)
const evidence = computed(() =>
  Array.isArray(props.task.output?.evidence)
    ? props.task.output.evidence
        .map((e: Record<string, unknown>) => `${e.type} #${e.id}`)
        .join(' · ')
    : '',
)
const chart = computed(() => ({
  tooltip: { trigger: 'item' },
  legend: { bottom: 0, textStyle: { fontSize: 14 } },
  series: [
    {
      type: 'pie',
      radius: ['48%', '70%'],
      center: ['50%', '43%'],
      label: { show: false },
      itemStyle: { borderWidth: 3, borderColor: '#fff', borderRadius: 4 },
      data:
        billing.value?.category_breakdown?.map((c) => ({
          name: c.category,
          value: c.expense_minor / 100,
        })) || [],
    },
  ],
}))
</script>
<template>
  <article class="business-result agent-message">
    <header>
      <div>
        <span>{{ task.tool }}</span>
        <h3>{{ title }}</h3>
      </div>
      <RouterLink :to="destination">{{ action }}<ArrowUpRight :size="15" /></RouterLink>
    </header>
    <p v-if="task.output?.explanation" class="result-explanation">{{ task.output.explanation }}</p>
    <template v-if="billing"
      ><div class="result-metrics">
        <div>
          <span>总收入</span><strong>{{ money(billing.total_income) }}</strong>
        </div>
        <div>
          <span>总支出</span><strong>{{ money(billing.total_expense) }}</strong>
        </div>
        <div v-if="billing.anomalies">
          <span>待核查交易</span><strong>{{ billing.anomalies.length }}<small>项</small></strong>
        </div>
      </div>
      <div v-if="billing.category_breakdown?.length" class="result-analysis">
        <Chart :option="chart" label="消费结构（服务端账单数据）" />
        <div>
          <h4>消费结构</h4>
          <div v-for="c in billing.category_breakdown" :key="c.category" class="result-row">
            <span>{{ c.category }}</span
            ><b>{{ money(c.expense_minor) }}</b>
          </div>
        </div>
      </div>
      <p v-else class="result-note">当前分析区间没有消费分类记录。</p>
      <p v-for="f in billing.findings" :key="f.finding_id" class="result-note">
        {{ f.description }}
      </p></template
    >
    <template v-else-if="subscription"
      ><div class="result-metrics">
        <div>
          <span>月度订阅预测</span
          ><strong>{{ money(subscription.active_recurring_forecast.monthly_minor) }}</strong>
        </div>
        <div>
          <span>重复订阅线索</span
          ><strong>{{ subscription.duplicate_groups?.length ?? 0 }}<small>组</small></strong>
        </div>
      </div>
      <div
        v-for="entry in subscription.subscriptions"
        :key="entry.subscription.id"
        class="result-row"
      >
        <span>{{ entry.subscription.merchant_name }}</span
        ><b>{{ money(entry.subscription.amount_minor) }}</b>
      </div>
      <p v-for="(g, i) in subscription.duplicate_groups" :key="i" class="result-note">
        {{ g.reason }}
      </p>
      <p
        v-for="suggestion in subscription.suggestions"
        :key="suggestion.subscription_id"
        class="result-note"
      >
        {{ suggestion.reason }}
      </p>
      <p class="result-note">订阅取消需在订阅管理中核对详情并单独授权。</p></template
    >
    <template v-else-if="planning"
      ><div class="result-metrics">
        <div>
          <span>建议储蓄</span><strong>{{ money(planning.suggested_savings_minor) }}</strong>
        </div>
        <div>
          <span>必要支出预留</span><strong>{{ money(planning.necessary_reserve_minor) }}</strong>
        </div>
        <div>
          <span>可分配资金</span><strong>{{ money(planning.disposable_minor) }}</strong>
        </div>
      </div>
      <div v-for="option in planning.options" :key="option.name" class="result-row">
        <div>
          <b>{{ option.name }}</b>
          <p>{{ option.explanation }}</p>
        </div>
        <strong>{{ money(option.savings_minor) }}</strong>
      </div>
      <p v-for="note in planning.review_notes" :key="note" class="result-note">{{ note }}</p>
      <p v-for="blocker in planning.blockers" :key="blocker" class="result-warning">
        {{ blocker }}
      </p></template
    >
    <template v-else-if="lifestyle">
      <p class="result-note">
        生活需求与资金安全为强制约束；正常饮食和必要生活开支不因储蓄目标自动削减。
      </p>
      <div v-for="option in lifestyle.plan.result.options" :key="option.name" class="result-row">
        <div>
          <b>{{ optionNames[option.name] || option.name }}</b>
          <p>{{ option.explanation }}</p>
          <span
            >生活预算 {{ money(option.budget_minor) }} · 每月储蓄建议
            {{ money(option.savings_monthly_minor) }}</span
          >
        </div>
        <strong>{{ money(option.expense_reduction_minor) }}<small>预计减少支出</small></strong>
      </div>
      <p v-for="note in lifestyle.plan.result.conflicts" :key="note" class="result-warning">
        {{ note }}
      </p>
      <p
        v-for="note in lifestyle.plan.result.missing_information"
        :key="note"
        class="result-warning"
      >
        待补充：{{ note }}
      </p>
      <p class="result-note">
        预计节余不会提前计入账户余额，也不会根据体重变化发放金融奖励或处罚。
      </p>
    </template>
    <template v-else-if="operationId">
      <div class="result-metrics">
        <div>
          <span>操作金额</span><strong>{{ money(data.amount_minor as number | undefined) }}</strong>
        </div>
        <div>
          <span>金融意向状态</span><strong class="intent-status">{{ data.status }}</strong>
        </div>
      </div>
      <p class="result-note">
        操作意向已生成。请单独核对账户、金额、用途和风险，再决定是否授权执行。
      </p>
      <button class="btn btn-outline" @click="$emit('inspect', operationId)">核对金融意向</button>
    </template>
    <template v-else-if="savings"
      ><h4>{{ savings.name }}</h4>
      <div class="result-metrics">
        <div>
          <span>目标金额</span><strong>{{ money(savings.target_minor) }}</strong>
        </div>
        <div>
          <span>目标金余额</span><strong>{{ money(savings.current_minor) }}</strong>
        </div>
        <div>
          <span>储蓄进度</span
          ><strong>{{ (savings.progress_basis_points / 100).toFixed(1) }}%</strong>
        </div>
      </div></template
    >
    <template v-else-if="lifeGoal"
      ><p class="result-note">{{ lifeGoal.version.text }}</p>
      <div class="result-metrics">
        <div>
          <span>目标储蓄</span
          ><strong>{{ money(lifeGoal.version.parsed?.savings_target_minor) }}</strong>
        </div>
        <div>
          <span>每月储蓄</span
          ><strong>{{ money(lifeGoal.version.parsed?.savings_monthly_minor) }}</strong>
        </div>
      </div>
      <p
        v-for="(constraint, i) in lifeGoal.version.parsed?.constraints"
        :key="i"
        class="result-note"
      >
        {{ constraint.description }}
      </p>
      <p
        v-for="missing in lifeGoal.version.parsed?.missing_information"
        :key="missing"
        class="result-warning"
      >
        待补充：{{ missing }}
      </p></template
    >
    <template v-else
      ><p class="result-note">该步骤已返回服务端记录，可展开业务详情核对具体字段。</p></template
    >
    <div v-if="pendingActions.length" class="result-warning">
      <h4>待确认操作</h4>
      <p v-for="(a, i) in pendingActions" :key="i">{{ a.type }}</p>
      <RouterLink to="/agents">核对操作详情并确认</RouterLink>
      <p>涉及银行执行时，还需单独核对账户、金额、用途与风险。</p>
    </div>
    <footer>
      <details v-if="evidence">
        <summary>查看数据来源</summary>
        <p>{{ evidence }}</p>
      </details>
      <JsonDetails :value="task.output" label="查看业务详情与完整返回" />
    </footer>
  </article>
</template>
<style scoped>
.business-result {
  margin: 0 !important;
  padding: 24px !important;
  border: 1px solid #e7edf3 !important;
  border-radius: 12px !important;
  background: #fff;
  box-shadow: none !important;
  min-width: 0;
  overflow-wrap: anywhere;
}
.business-result header {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  align-items: center;
}
.business-result header span {
  display: block;
  color: #64748b;
  font-size: 12px;
  margin-bottom: 6px;
}
.business-result h3 {
  margin: 0;
  font-size: 17px;
  font-weight: 650;
  color: #172033;
}
.business-result header a {
  display: flex;
  align-items: center;
  gap: 5px;
  color: #0d766e;
  font-size: 14px;
  white-space: nowrap;
}
.result-explanation,
.result-note {
  font-size: 14px !important;
  line-height: 1.8;
  color: #475569;
  margin: 16px 0;
}
.result-metrics {
  display: flex;
  gap: 32px;
  flex-wrap: wrap;
  padding: 20px 0;
  border-bottom: 1px solid #edf1f5;
}
.result-metrics div {
  display: grid;
  gap: 8px;
}
.result-metrics span {
  font-size: 14px;
  color: #64748b;
}
.result-metrics strong {
  font-size: 25px;
  color: #142d4e;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.6px;
}
.result-metrics small {
  font-size: 14px;
  margin-left: 6px;
  color: #64748b;
}
.result-analysis {
  display: grid;
  grid-template-columns: minmax(180px, 1fr) minmax(180px, 1fr);
  gap: 24px;
  align-items: center;
  margin-top: 16px;
}
.result-analysis :deep(.chart) {
  height: 230px;
}
.result-analysis h4 {
  font-size: 14px;
  margin: 0 0 8px;
}
.result-row {
  padding: 12px 0;
  display: flex;
  gap: 16px;
  justify-content: space-between;
  border-bottom: 1px solid #edf1f5;
  font-size: 14px;
}
.result-row b,
.result-row strong {
  font-weight: 600;
}
.result-row p {
  font-size: 14px;
  color: #64748b;
  margin: 8px 0 0;
}
.result-warning {
  font-size: 14px;
  padding: 12px;
  background: #fffbeb;
  color: #92400e;
  border-radius: 8px;
  line-height: 1.7;
}
.business-result footer {
  margin-top: 16px;
  font-size: 14px;
  color: #64748b;
}
.business-result summary {
  cursor: pointer;
}
.business-result footer :deep(pre) {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.business-result footer :deep(summary) {
  font-size: 14px;
}
.business-result footer details {
  margin-top: 12px;
}
@media (max-width: 640px) {
  .business-result {
    padding: 16px !important;
  }
  .business-result header {
    align-items: flex-start;
    flex-direction: column;
    gap: 10px;
  }
  .result-analysis {
    grid-template-columns: 1fr;
    gap: 8px;
  }
  .result-metrics {
    gap: 20px;
  }
  .result-metrics strong {
    font-size: 23px;
  }
  .result-row > strong {
    white-space: nowrap;
  }
}
</style>
