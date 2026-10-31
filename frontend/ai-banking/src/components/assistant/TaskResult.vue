<script setup lang="ts">
import { categoryLabel } from '@/lib/categories'
import { computed } from 'vue'
import { ArrowUpRight } from 'lucide-vue-next'
import type { Schema } from '@/api/client'
import { operationSuggestion, preparedOperationId, type TaskRow, type OperationSuggestion } from '@/components/agents/task-view'
import Chart from '@/components/Chart.vue'
import ScenarioOverview from '@/components/assistant/ScenarioOverview.vue'
import JsonDetails from '@/components/JsonDetails.vue'
import { money } from '@/lib/utils'
import { billingFindingText } from '@/lib/business-copy'
import { selectedExpenseActions } from '@/lib/expense-goal-actions'
import {subscriptionManagementPath,subscriptionForecastView} from '@/lib/subscription-scope'
const props = defineProps<{ task: TaskRow }>()
defineEmits<{ inspect: [id: string]; goal: []; prepare: [proposal: OperationSuggestion] }>()
const operationId = computed(() => preparedOperationId(props.task.output))
const suggestion = computed(() => operationSuggestion(props.task))
const scenarioResult = computed(() => {
  const task = props.task
  if (task.agent_type !== 'supervisor' || task.skill_id !== 'task_planning' ||
    task.skill_version !== 1 || task.status !== 'succeeded' ||
    task.output?.financial_execution_enabled !== false ||
    task.tool && task.tool !== 'get_scenario_overview' ||
    task.execution_mode && task.execution_mode !== 'read_only') return null
  // Natural-language requests preserve tasks:[]; the persisted result still has
  // a typed scenario DTO. Render that DTO without inventing missing plan fields.
  const result = data.value
  return typeof result.scenario === 'string' &&
    ['transfer', 'cards', 'investment', 'subscriptions', 'birthday'].includes(result.scenario) &&
    result.is_simulated === true && Array.isArray(result.items) && Array.isArray(result.limitations)
    ? result : null
})
const isScenarioOverview = computed(() => props.task.tool === 'get_scenario_overview' || scenarioResult.value !== null)
const operationNames: Record<string, string> = {
  transfer:'账户转账',recipient_transfer:'收款人转账',scheduled_transfer:'定时转账',aa_payment:'AA付款',
  cancel_subscription:'取消订阅',card_manage:'卡片管理',investment_assessment:'风险测评',investment_order:'理财申赎',
  occasion_plan:'生日资金预留',occasion_order:'生日订购',occasion_release:'退回生日预留',
  savings_deposit:'存入目标金',savings_withdrawal:'取出目标金',create_savings_goal:'创建目标金',budget_rule_update:'调整预算规则',
}
const actionNames: Record<string, string> = {lock:'锁定卡片',unlock:'解锁卡片',report_lost:'挂失卡片',apply:'申请卡片',bind:'绑定卡片',unbind:'解绑卡片',set_default:'设为默认卡',adjust_limit:'调整信用额度',set_transaction_limit:'调整交易限额',buy:'申购',redeem:'赎回'}
const proposalFields = computed(() => {
  const params = suggestion.value?.parameters as Record<string, unknown> | undefined
  if (!params) return []
  const result: {label:string;value:string}[] = []
  for (const [key,label] of Object.entries({action:'建议操作',account_id:'账户编号',source_account_id:'转出账户编号',target_account_id:'转入账户编号',card_id:'卡片编号',subscription_id:'订阅编号',goal_id:'目标编号',product_id:'产品编号',plan_id:'计划编号',name:'对象称呼',birthday:'生日日期',description:'用途'})) {
    const value = params[key]
    if (typeof value === 'string' && value.length <= 256) result.push({label,value:key==='action' ? actionNames[value] || value : value})
  }
  for (const [key,label] of Object.entries({amount_minor:'建议金额',reserve_minor:'建议预留',credit_limit_minor:'建议信用额度',transaction_limit_minor:'建议单笔限额'})) {
    const value=params[key]
    if (typeof value==='number' && Number.isSafeInteger(value) && value>=0) result.push({label,value:money(value)})
  }
  return result
})
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
const billingActions = computed(() => {
  const report = billing.value
  if (!report?.report_id || !Array.isArray(report.category_breakdown)) return []
  const observed = report.category_breakdown
    .filter(row => Number.isSafeInteger(row.expense_minor) && row.expense_minor > 0 &&
      Array.isArray(row.transaction_ids) && row.transaction_ids.length > 0)
    .slice().sort((a, b) => b.expense_minor - a.expense_minor)
  return selectedExpenseActions(observed.map(row => row.category)).slice(0, 3).map(action => ({
    ...action,
    evidence: observed.find(row => row.category === action.category)!,
  }))
})
const subscription = computed(() =>
  'active_recurring_forecast' in data.value ? (data.value as Schema<'SubscriptionReport'>) : null,
)
const subscriptionForecast=computed(()=>subscriptionForecastView(subscription.value))
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
      ? subscriptionManagementPath(props.task.parameters)
      : planning.value
        ? '/planning'
        : lifestyle.value
          ? '/goals'
          : props.task.agent_type === 'savings'
            ? '/goals'
            : '/agents',
)
const title = computed(() =>
  isScenarioOverview.value ? '场景可用资源（只读）' : billing.value
    ? '财务分析摘要'
    : subscription.value
      ? '订阅支出与优化建议'
      : planning.value
        ? '资金规划方案'
        : lifestyle.value
          ? '生活目标资金方案'
          : suggestion.value
            ? '待核对的操作建议'
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
          name: categoryLabel(c.category),
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
    <h4 v-if="billing">结论</h4>
    <p v-if="task.output?.explanation" class="result-explanation">{{ task.output.explanation }}</p>
    <p v-else-if="billing" class="result-note">以下为服务端账单统计；尚无本次模型文字解读。</p>
    <ScenarioOverview v-if="isScenarioOverview" :result="scenarioResult" />
    <template v-else-if="billing"
      ><h4>数据依据</h4><p class="result-note">已记录 {{ billing.transaction_count }} 笔交易。金额来自 Go 账单工具，模型不能修改。</p><div class="result-metrics">
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
            <span>{{ categoryLabel(c.category) }}</span
            ><b>{{ money(c.expense_minor) }}</b>
          </div>
        </div>
      </div>
      <p v-else class="result-note">当前分析区间没有消费分类记录。</p>
      <p v-for="f in billing.findings" :key="f.finding_id" class="result-note">
        {{ billingFindingText(f, billing) }}
      </p>
      <h4>分析局限</h4>
      <p class="result-note">尚未核验是否覆盖完整月份。没有记录不等于没有花费，不能据此确认下月可节省金额，也不能将收入记录当作可用余额。</p>
      <h4>具体建议</h4>
      <section v-if="billingActions.length" aria-label="按账单生成的可选行动">
        <p class="result-note">以下是规则生成的可选行动，与上方模型解读分开。由你决定是否试行，不会替你选择削减类别或修改预算。</p>
        <ol class="result-note">
          <li v-for="action in billingActions" :key="action.category">
            <strong>{{ action.title }}</strong>
            <p>{{ categoryLabel(action.category) }}已记录 {{ money(action.evidence.expense_minor) }} · {{ action.evidence.transaction_ids.length }} 笔；记录金额不是可节省额度。</p>
            <p>{{ action.action }}</p>
            <p>怎么复盘：{{ action.check }}</p>
            <RouterLink :to="{path:'/billing',query:{report_id:billing.report_id,category:action.category}}">核对{{ categoryLabel(action.category) }}明细 →</RouterLink>
          </li>
        </ol>
        <p class="result-note">先试行、记录并在一周后核对总支出。完整月份账单尚未核实，不能据此承诺下月能省到目标金额；必要生活支出优先保留。</p>
      </section>
      <ol v-else class="result-note"><li>补齐上一完整月份账单，再选择你愿意调整的开支，保留必要生活支出。</li><li>将目标拆解为每周观察，试行后对比实际花费；不把延期消费或未确认取消记作已节省。</li></ol>
      <button type="button" class="result-goal-link" @click="$emit('goal')">设置下月少花目标 →</button>
      <p v-if="task.output?.workflow_execute_id" class="result-note">模型费用未正式对账；响应或用量记录不代表已结算。原始调用依据可在执行详情中查看。</p>
    </template
    >
    <template v-else-if="subscription"
      ><p v-if="!subscription.subscriptions.length" class="result-note" role="status">当前账户和分析区间未识别到订阅候选，暂无法估算；这不代表实际订阅费用为零。可到订阅管理核对已登记项目及扣费记录。</p>
      <div class="result-metrics">
        <div>
          <span>全部活跃订阅确认停扣后的月度费用参考</span
          ><strong>{{ subscriptionForecast.activeMinor===null ? '暂无法估算' : money(subscriptionForecast.activeMinor) }}</strong>
        </div>
        <div>
          <span>重复订阅线索</span
          ><strong>{{ subscription.duplicate_groups?.length ?? 0 }}<small>组</small></strong>
        </div>
      </div>
      <p v-if="subscription.subscriptions.length" class="result-note">该金额以已识别活跃订阅全部确认停扣为条件，没有可估算的活跃记录时保留未知。不是下月实际账单，不是已节省金额，也不会增加可用余额。</p>
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
    <template v-else-if="suggestion">
      <h4>{{ operationNames[suggestion.kind] || '业务操作' }} · 模拟业务</h4>
      <dl class="proposal-fields"><div v-for="field in proposalFields" :key="field.label"><dt>{{ field.label }}</dt><dd>{{ field.value }}</dd></div></dl>
      <p class="result-note">这是待核对的业务参数，尚未通过账户归属、余额和风控校验，也未创建金融操作意向。</p>
      <p class="result-note">请先核对建议。下一步由 Go 按当前账户状态校验并创建待授权意向，实际执行仍需你单独授权。</p>
      <JsonDetails :value="suggestion" label="核对建议的操作参数" />
      <button type="button" class="result-goal-link" @click="$emit('prepare', suggestion)">核对并创建操作意向 →</button>
    </template>
    <template v-else
      ><p class="result-note">该步骤已返回服务端记录，可展开业务详情核对具体字段。</p></template
    >
    <div v-if="pendingActions.length && !suggestion" class="result-warning">
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
      <JsonDetails v-if="!isScenarioOverview" :value="task.output" label="查看业务详情与完整返回" />
    </footer>
  </article>
</template>
<style scoped>
.proposal-fields{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:12px;margin:16px 0}.proposal-fields div{min-width:0}.proposal-fields dt{font-size:13px;color:#64748b}.proposal-fields dd{margin:5px 0 0;font-size:14px;overflow-wrap:anywhere}
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
