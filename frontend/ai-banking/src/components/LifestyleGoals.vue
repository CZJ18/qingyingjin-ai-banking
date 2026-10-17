<script setup lang="ts">
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { api, request, type Schema } from '@/api/client'
import { agentApi } from '@/api/agent-client'
import { useSession, useUI } from '@/lib/store'
import { money, date } from '@/lib/utils'
import { period, currentMonth } from '@/lib/queries'
import Button from '@/components/ui/button/Button.vue'
import State from '@/components/State.vue'
import JsonDetails from '@/components/JsonDetails.vue'
import { createStableActions, isCurrentProductContext } from '@/lib/product-actions'
const actions = createStableActions()
let active = true
const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  clock = setInterval(() => {
    now.value = Date.now()
  }, 1000)
})
onBeforeUnmount(() => {
  active = false
  clearInterval(clock)
})
const s = useSession(),
  ui = useUI(),
  text = ref(''),
  parsed = ref<Schema<'LifestyleParseResult'> | null>(null),
  confirmed = ref(false),
  busy = ref(false),
  error = ref(''),
  updating = ref(false),
  lookup = ref(s.lifestyleGoalId),
  budgetId = ref(''),
  reportId = ref(s.billingReportId),
  month = ref(currentMonth())
const goal = useQuery({
  queryKey: computed(() => ['lifestyle-goal', s.lifestyleGoalId]),
  enabled: computed(() => s.connected && !!s.lifestyleGoalId),
  queryFn: ({ signal }) => api.lifestyle.goal(s.lifestyleGoalId, signal),
})
const budgets = useQuery({
  queryKey: computed(() => ['lifestyle-budgets', s.sessionEpoch]),
  enabled: computed(() => s.connected),
  queryFn: ({ signal }) => request<Schema<'Budget'>[]>('/api/v1/budgets?limit=100', { signal }),
})
const plan = useQuery({
  queryKey: computed(() => ['lifestyle-plan', s.lifestyleGoalId, s.lifestylePlanId]),
  enabled: computed(() => s.connected && !!s.lifestylePlanId),
  queryFn: async ({ signal }) => {
    const id = s.lifestyleGoalId
    const value = await api.lifestyle.plan(s.lifestylePlanId, signal)
    if (value.plan.goal_id !== id) throw new Error('该方案不属于当前生活目标，请重新选择')
    return value
  },
})
const history = useQuery({
  queryKey: computed(() => ['lifestyle-plans', s.lifestyleGoalId]),
  enabled: computed(() => s.connected && !!s.lifestyleGoalId),
  queryFn: ({ signal }) => api.lifestyle.plans(s.lifestyleGoalId, signal),
})
const latest = computed(() =>
  goal.data.value?.versions.find((v) => v.version === goal.data.value?.goal.current_version),
)
const optionNames: Record<string, string> = {
  conservative: '保守方案',
  balanced: '平衡方案',
  active: '积极储蓄方案',
}
const recommendationStatus: Record<string, string> = {
  pending: '待反馈',
  accepted: '已接受',
  rejected: '已拒绝',
  superseded: '已被替代',
}
const resultLabels: Record<string, string> = {
  budget: '月度预算及类别分配',
  complete_historical_month_billing_report: '完整历史月的全账户账单快照',
  financial_plan_with_known_necessary_expenses: '已明确必要开支的资金计划',
  user_adjustable_category_preferences: '您允许调整的消费类别',
  'expense reduction exceeds evidence-backed optional budget; request a lower target or new preferences':
    '可调整类别的真实消费不足以达到减少支出目标，请核对类别或重新确认目标。',
  'savings exceed current safe cash capacity; reduced spending is future only':
    '储蓄目标超过当前安全可储蓄上限；未来预计少花的资金尚未到账。',
  'Reduction forecasts compare the lower optional budget with historical spending. User-approved optional categories only; normal meals and necessary reserves stay protected. Future avoided spending is not cash or authorization.':
    '依据历史消费调整您明确允许的非必要类别，保留正常饮食及必要开支。预计减少的支出不计入当前余额，接受建议也不构成资金操作授权。',
}
const categoryLabels: Record<string, string> = {
  food: '餐饮',
  dining: '餐饮',
  housing: '住房',
  shopping: '购物',
  exercise: '运动',
  sports: '运动',
  transport: '交通',
  medical: '医疗',
  subscriptions: '订阅',
  subscription: '订阅',
  education: '教育',
  other: '其他',
}
watch(text, () => {
  parsed.value = null
  confirmed.value = false
})
watch(
  () => s.lifestyleGoalId,
  (value) => {
    lookup.value = value
  },
)
function context() {
  return {
    connected: s.connected,
    sessionEpoch: s.sessionEpoch,
    userId: s.user?.id || '',
    goalId: s.lifestyleGoalId,
  }
}
async function action(work: (current: () => boolean) => Promise<void>) {
  if (busy.value || !s.connected) return
  const captured = context()
  const current = () => active && isCurrentProductContext(captured, context())
  busy.value = true
  error.value = ''
  try {
    await work(current)
  } catch (e) {
    if (current()) error.value = (e as Error).message
  } finally {
    if (active) busy.value = false
  }
}
function selectGoal(id: string) {
  if (busy.value) return
  s.saveLifestyleGoal(id)
  updating.value = false
  parsed.value = null
  confirmed.value = false
}
function edit() {
  text.value = latest.value?.text || ''
  updating.value = true
}
function reset() {
  updating.value = false
  text.value = ''
  parsed.value = null
  confirmed.value = false
}
async function parse() {
  await action(async (current) => {
    const result = await api.lifestyle.parse({ text: text.value.trim() })
    if (!current()) return
    parsed.value = result
    confirmed.value = false
  })
}
async function save() {
  await action(async (current) => {
    if (!confirmed.value || parsed.value?.status !== 'parsed' || !parsed.value.spec)
      throw new Error('请核对目标解析结果后勾选确认')
    const body = {
      text: text.value.trim(),
      spec: parsed.value.spec,
      expected_version: updating.value ? goal.data.value!.goal.current_version : 0,
    }
    const id = s.lifestyleGoalId,
      isUpdate = updating.value
    const result = await actions.run(isUpdate ? 'goal-update:' + id : 'goal-create', body, (key) =>
      isUpdate ? api.lifestyle.update(id, body, key) : api.lifestyle.create(body, key),
    )
    if (!current()) return
    s.clearLifestylePlan()
    s.saveLifestyleGoal(result.goal.id)
    lookup.value = result.goal.id
    reset()
    ui.notify('生活目标已保存，尚未执行金融操作')
    await goal.refetch()
  })
}
async function generateReport() {
  await action(async (current) => {
    const result = await request<Schema<'BillingReport'>>(
      '/api/v1/billing/generate_billing_report',
      { method: 'POST', body: { ...period(month.value), limit: 100, offset: 0 } },
    )
    if (!current()) return
    reportId.value = result.report_id
    s.saveBillingReport(result.report_id)
    ui.notify('已生成全账户真实交易账单快照')
  })
}
async function compile() {
  await action(async (current) => {
    if (!goal.data.value) throw new Error('请先选择已保存的目标')
    const id = s.lifestyleGoalId,
      body = {
        expected_version: goal.data.value.goal.current_version,
        budget_id: budgetId.value || '0',
        billing_report_id: reportId.value,
        financial_plan_id: s.planId || '0',
      }
    const result = await actions.run('compile:' + id, body, (key) =>
      api.lifestyle.compile(id, body, key),
    )
    if (!current()) return
    if (result.plan.goal_id !== id) throw new Error('返回方案与当前目标不一致')
    s.saveLifestylePlan(result.plan.id)
    ui.notify('生活方案已编译，所有金额来自金融服务计算')
    await Promise.all([plan.refetch(), history.refetch()])
  })
}
async function synchronize() {
  await action(async (current) => {
    const resources = [
      { kind: 'lifestyle_goal', source_id: s.lifestyleGoalId },
      ...(s.lifestylePlanId ? [{ kind: 'lifestyle_plan', source_id: s.lifestylePlanId }] : []),
    ]
    await actions.run('projection-refresh', { resources }, (key) =>
      agentApi.refresh({ resources }, key),
    )
    if (current()) ui.notify('目标和方案已同步至个人建议记录')
  })
}
async function decide(row: Schema<'LifestyleRecommendation'>, decision: 'accept' | 'reject') {
  await action(async (current) => {
    if (row.status !== 'pending' || row.expires_at <= Date.now())
      throw new Error('该建议已结束或过期，请重新编译生活方案')
    if (!plan.data.value) return
    const id = s.lifestylePlanId,
      body = {
        expected_version: row.version,
        decision,
        plan_digest: plan.data.value.plan.input_digest,
      }
    await actions.run('recommendation:' + row.id, body, (key) =>
      api.lifestyle.decide(row.id, body, key),
    )
    if (!current() || s.lifestylePlanId !== id) return
    await plan.refetch()
    if (!current()) return
    ui.notify(
      decision === 'accept' ? '已接受这项建议；如需资金操作，请独立授权' : '已记录拒绝这项建议',
    )
    const resources = [{ kind: 'lifestyle_plan', source_id: id }]
    try {
      await actions.run('projection-refresh', { resources }, (key) =>
        agentApi.refresh({ resources }, key),
      )
    } catch {
      if (current())
        error.value = '建议反馈已保存，但个人建议记录尚未同步，请点击同步个人建议重试。'
    }
  })
}
</script>
<template>
  <section class="bank-panel lifestyle-v2" aria-label="健康生活目标编译器">
    <header class="bank-section-heading">
      <div>
        <h2>健康生活目标编译器</h2>
        <p>理解生活目标，分别规划减少支出和储蓄；保护正常饮食及必要生活开支。</p>
      </div>
      <Button v-if="updating" variant="outline" @click="reset">新建目标</Button>
    </header>
    <State :connected="s.connected">
      <form class="p6-goal-input" @submit.prevent="parse">
        <label
          >描述你的生活和资金目标<textarea
            v-model="text"
            rows="3"
            maxlength="4000"
            placeholder="未来三个月保持规律饮食和运动，每月合理储蓄200元，不影响正常生活。"
            required
            :disabled="busy"
          /></label
        ><Button type="submit" :disabled="busy || !text.trim()">{{
          busy ? '正在处理…' : '解析生活目标'
        }}</Button>
      </form>
      <div v-if="parsed" class="p6-parse-result">
        <p v-if="parsed.status === 'needs_information'" role="status">
          请补充：{{ parsed.missing_information.join('、') }}。当前目标尚未形成可执行计划。
        </p>
        <template v-else-if="parsed.spec"
          ><div class="p6-stats">
            <span
              >周期<b>{{ parsed.spec.period_months }}个月</b></span
            ><span
              >每月减少支出<b>{{ money(parsed.spec.expense_reduction_monthly_minor) }}</b></span
            ><span
              >每月储蓄目标<b>{{ money(parsed.spec.savings_monthly_minor) }}</b></span
            >
          </div>
          <p>
            保护的类别：{{ parsed.spec.protected_categories.join('、') }}；禁止操作：{{
              parsed.spec.prohibited_operations.join('、') || '按金融安全规则执行'
            }}。
          </p>
          <JsonDetails label="查看完整解析与约束" :value="parsed.spec" /><label class="p6-checkbox"
            ><input v-model="confirmed" type="checkbox" />我已核对上述目标和约束</label
          ><Button :disabled="busy || !confirmed" @click="save">{{
            updating ? '保存目标新版本' : '确认并保存生活目标'
          }}</Button></template
        >
      </div>
      <p v-if="error" class="form-error" role="alert">{{ error }}</p>
      <div class="p6-goal-select">
        <label
          >已保存的生活目标<select
            :disabled="busy"
            aria-label="已保存的生活目标"
            :value="s.lifestyleGoalId"
            @change="selectGoal(($event.target as HTMLSelectElement).value)"
          >
            <option value="">请选择</option>
            <option v-for="id in s.lifestyleGoalIds" :key="id" :value="id">目标 #{{ id }}</option>
          </select></label
        >
        <form @submit.prevent="selectGoal(lookup.trim())">
          <label
            >恢复已有目标编号<input
              :disabled="busy"
              v-model="lookup"
              placeholder="已授权目标 ID" /></label
          ><Button variant="outline" type="submit" :disabled="busy || !lookup.trim()"
            >查询目标</Button
          >
        </form>
      </div>
      <State
        v-if="s.lifestyleGoalId"
        :connected="s.connected"
        :loading="goal.isLoading.value"
        :error="goal.error.value"
        @retry="goal.refetch()"
      >
        <template v-if="latest"
          ><p>
            <b>目标 #{{ s.lifestyleGoalId }} · 版本 {{ latest.version }}</b> {{ latest.text }}
            <Button variant="outline" :disabled="busy" @click="edit">更新生活目标</Button>
          </p>
          <JsonDetails label="目标版本历史" :value="goal.data.value?.versions" />
          <div class="p6-compile-form">
            <label
              >预算数据<select :disabled="busy" aria-label="预算数据" v-model="budgetId">
                <option value="">尚未选择预算</option>
                <option v-for="b in budgets.data.value" :key="b.id" :value="b.id">
                  {{ b.month }} · {{ money(b.total_minor) }} (#{{ b.id }})
                </option>
              </select></label
            ><label>账单月份<input :disabled="busy" v-model="month" type="month" /></label
            ><Button
              variant="outline"
              :disabled="busy || !/^\d{4}-(0[1-9]|1[0-2])$/.test(month)"
              @click="generateReport"
              >生成账单快照</Button
            ><label
              >账单快照编号<input
                :disabled="busy"
                v-model="reportId"
                placeholder="生成或选择实际账单 report_id"
            /></label>
          </div>
          <p class="form-hint">
            资金计划：{{
              s.planId ? '#' + s.planId : '尚未创建'
            }}。缺少预算或账单时，编译器会返回待补充信息；预计节余不会提前入账。
          </p>
          <div class="p6-actions">
            <Button :disabled="busy" @click="compile">编译生活资金方案</Button
            ><Button variant="outline" :disabled="busy" @click="synchronize">同步个人建议</Button
            ><RouterLink to="/planning">查看动态资金规划 →</RouterLink>
          </div>
        </template>
      </State>
      <State
        v-if="s.lifestylePlanId"
        :connected="s.connected"
        :loading="plan.isLoading.value"
        :error="plan.error.value"
        @retry="plan.refetch()"
      >
        <template v-if="plan.data.value"
          ><p>
            <b>生活方案 #{{ plan.data.value.plan.id }}</b> · 目标版本
            {{ plan.data.value.plan.goal_version }} · 有效至
            {{ date(plan.data.value.plan.expires_at) }}
          </p>
          <p v-if="plan.data.value.plan.result.missing_information.length">
            需要补充：{{
              plan.data.value.plan.result.missing_information
                .map((v) => resultLabels[v] || v)
                .join('、')
            }}
          </p>
          <p v-if="plan.data.value.plan.result.conflicts.length" role="status">
            目标冲突：{{
              plan.data.value.plan.result.conflicts.map((v) => resultLabels[v] || v).join('、')
            }}
          </p>
          <div class="p6-lifestyle-options">
            <article v-for="r in plan.data.value.recommendations" :key="r.id">
              <h3>{{ optionNames[r.option.name] || r.option.name }}</h3>
              <span>每月储蓄建议</span><strong>{{ money(r.option.savings_monthly_minor) }}</strong>
              <p>
                减少支出：{{ money(r.option.expense_reduction_minor) }} · 调整后预算：{{
                  money(r.option.budget_minor)
                }}
              </p>
              <p>{{ resultLabels[r.explanation] || r.explanation }}</p>
              <ul>
                <li v-for="c in r.option.categories" :key="c.category">
                  {{ categoryLabels[c.category] || c.category }}：{{ money(c.original_minor) }} →
                  {{ money(c.proposed_minor) }}
                  {{ c.protected ? '（必要支出保护）' : '' }}
                </li>
              </ul>
              <p>建议状态：{{ recommendationStatus[r.status] }}</p>
              <p v-if="r.status === 'pending' && r.expires_at <= now" role="status">
                该建议已过期，请重新编译生活方案。
              </p>
              <div class="p6-actions">
                <Button
                  :disabled="busy || r.status !== 'pending' || r.expires_at <= now"
                  @click="decide(r, 'accept')"
                  >接受建议</Button
                ><Button
                  variant="outline"
                  :disabled="busy || r.status !== 'pending' || r.expires_at <= now"
                  @click="decide(r, 'reject')"
                  >拒绝建议</Button
                >
              </div>
            </article>
          </div>
          <JsonDetails label="实际交易证据与计算来源" :value="plan.data.value.plan.snapshot"
        /></template>
      </State>
      <div v-if="history.data.value?.length" class="p6-plan-history">
        <label
          >生活方案历史<select
            :disabled="busy"
            aria-label="生活方案历史"
            :value="s.lifestylePlanId"
            @change="s.saveLifestylePlan(($event.target as HTMLSelectElement).value)"
          >
            <option value="">选择历史方案</option>
            <option v-for="p in history.data.value" :key="p.plan.id" :value="p.plan.id">
              #{{ p.plan.id }} · 目标版本 {{ p.plan.goal_version }} · {{ date(p.plan.created_at) }}
            </option>
          </select></label
        >
      </div>
    </State>
  </section>
</template>
<style scoped>
.lifestyle-v2 {
  margin: 20px 0;
  padding: 24px;
}
.p6-goal-input {
  display: grid;
  gap: 14px;
  margin: 20px 0;
}
.p6-goal-input button {
  justify-self: start;
}
.p6-parse-result {
  background: #f2f8f8;
  padding: 20px;
  border-radius: 10px;
  margin-bottom: 20px;
}
.p6-stats,
.p6-lifestyle-options {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}
.p6-stats span {
  display: grid;
  gap: 8px;
}
.p6-stats b {
  font-size: 22px;
}
.p6-checkbox {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 16px 0;
}
.p6-checkbox input {
  width: 18px;
  height: 18px;
}
.p6-goal-select,
.p6-goal-select form,
.p6-compile-form {
  display: grid;
  grid-template-columns: 1fr 1fr;
  align-items: end;
  gap: 14px;
  margin: 16px 0;
}
.p6-goal-select {
  grid-template-columns: 1fr 2fr;
}
.p6-lifestyle-options article {
  border: 1px solid #dce6f0;
  border-radius: 10px;
  padding: 20px;
  min-width: 0;
}
.p6-lifestyle-options strong {
  display: block;
  font-size: 28px;
  color: #008b91;
  margin-top: 8px;
}
.p6-lifestyle-options ul {
  padding-left: 18px;
  font-size: 13px;
  line-height: 1.8;
}
.p6-lifestyle-options p {
  line-height: 1.7;
}
.p6-actions {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
  align-items: center;
  margin: 16px 0;
}
.p6-plan-history {
  margin: 20px 0;
}
@media (max-width: 1000px) {
  .p6-goal-select,
  .p6-stats,
  .p6-lifestyle-options {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 600px) {
  .lifestyle-v2 {
    padding: 16px;
  }
  .p6-goal-select form,
  .p6-compile-form {
    grid-template-columns: 1fr;
  }
}
</style>
