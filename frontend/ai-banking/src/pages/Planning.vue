<script setup lang="ts">
import { ref, computed, watch, onBeforeUnmount } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { RefreshCw, ShieldCheck, ArrowRight, Plus, X } from 'lucide-vue-next'
import { request, post, type Schema } from '@/api/client'
import { useSession, useUI } from '@/lib/store'
import { currentMonth, useSavings, useOperations } from '@/lib/queries'
import { money, minor } from '@/lib/utils'
import Button from '@/components/ui/button/Button.vue'
import State from '@/components/State.vue'
import Chart from '@/components/Chart.vue'
import PlanHistory from '@/components/planning/PlanHistory.vue'
import ExecutionDialog from '@/components/ExecutionDialog.vue'
import { agentApi } from '@/api/agent-client'
import Status from '@/components/Status.vue'
import {
  DialogRoot,
  DialogPortal,
  DialogOverlay,
  DialogContent,
  DialogTitle,
  DialogDescription,
  DialogClose,
} from 'reka-ui'
const s = useSession(),
  ui = useUI(),
  lookup = ref(s.planId),
  showForm = ref(!s.planId),
  name = ref('健康生活资金计划'),
  essential = ref(''),
  living = ref(''),
  safety = ref(''),
  buffer = ref(''),
  income = ref(''),
  saveError = ref(''),
  busy = ref(false),
  selected = ref('balanced')
const savings = useSavings(),
  selectedSavings = ref<string[]>([])
let active = true
onBeforeUnmount(() => {
  active = false
})
function currentContext() {
  const epoch = s.sessionEpoch,
    userId = s.user?.id,
    planId = s.planId,
    accountId = s.accountId
  return () =>
    active &&
    s.connected &&
    s.sessionEpoch === epoch &&
    s.user?.id === userId &&
    s.planId === planId &&
    s.accountId === accountId
}
function lookupPlan() {
  if (!s.connected || busy.value || confirming.value) return
  s.savePlan(lookup.value)
}
async function applyProfileReserve() {
  if (busy.value || !s.connected) return
  const current = currentContext()
  busy.value = true
  saveError.value = ''
  try {
    const profile = await agentApi.profile()
    if (!current()) return
    safety.value = String(profile.safety_reserve_minor / 100)
    ui.notify('资金安全偏好已填入表单，保存计划后生效')
  } catch (e) {
    if (!current()) return
    saveError.value = (e as Error).message
  } finally {
    if (active) busy.value = false
  }
}
const plan = useQuery({
  queryKey: computed(() => ['plan', s.planId]),
  enabled: computed(() => s.connected && !!s.planId),
  queryFn: ({ signal }) =>
    request<Schema<'FinancialPlanView'>>('/api/v1/financial-plans/' + s.planId, { signal }),
  refetchInterval: 5000,
})
const versions = useQuery({
  queryKey: computed(() => ['plan-versions', s.planId]),
  enabled: computed(() => s.connected && !!s.planId),
  queryFn: ({ signal }) =>
    request<Schema<'FinancialPlanVersionList'>>(
      '/api/v1/financial-plans/' + s.planId + '/versions?limit=100',
      { signal },
    ),
  refetchInterval: 5000,
})
watch(
  () => plan.data.value?.plan.id,
  () => {
    const value = plan.data.value?.plan.settings
    if (!value) return
    essential.value = String((value.essential_remaining_minor || 0) / 100)
    living.value = String((value.living_remaining_minor || 0) / 100)
    safety.value = String(value.safety_reserve_minor / 100)
    buffer.value = String(value.cash_buffer_minor / 100)
    income.value = String(value.baseline_income_minor / 100)
    name.value = plan.data.value?.plan.name || ''
    selectedSavings.value = value.savings_goal_ids.map(String)
  },
  { immediate: true },
)
function safeId(id: string) {
  const n = Number(id)
  if (!Number.isSafeInteger(n) || n <= 0)
    throw new Error('当前规划接口仅接受 JavaScript 可安全表示的账户 ID')
  return n
}
function settings() {
  return {
    period: currentMonth(),
    account_ids: [safeId(s.accountId)],
    savings_goal_ids: selectedSavings.value.map(safeId),
    life_goal_ids: s.goalIds.map(safeId),
    essential_remaining_minor: minor(essential.value),
    living_remaining_minor: minor(living.value),
    safety_reserve_minor: minor(safety.value),
    cash_buffer_minor: minor(buffer.value),
    baseline_income_minor: minor(income.value),
    cashflows: plan.data.value?.plan.settings.cashflows || [],
  }
}
async function save() {
  if (busy.value || !s.connected) return
  const current = currentContext(),
    planId = s.planId
  saveError.value = ''
  if (!selectedSavings.value.length) {
    saveError.value = '请至少选择一个关联健康目标金，银行服务需要储蓄目标才能计算规划。'
    return
  }
  busy.value = true
  try {
    if (planId) {
      await request(`/api/v1/financial-plans/${planId}/settings`, {
        method: 'PUT',
        body: { expected_version: plan.data.value?.plan.current_version, settings: settings() },
      })
      if (!current()) return
      await plan.refetch()
      if (!current()) return
    } else {
      const out = await post<'/api/v1/financial-plans', Schema<'FinancialPlanView'>>(
        '/api/v1/financial-plans',
        { name: name.value, settings: settings() },
      )
      if (!current()) return
      s.savePlan(out.plan.id)
      lookup.value = out.plan.id
    }
    showForm.value = false
    ui.notify('资金规划已保存，未执行资金操作')
  } catch (e) {
    if (!current()) return
    saveError.value = (e as Error).message
    ui.notify((e as Error).message)
  } finally {
    if (active) busy.value = false
  }
}
async function replan() {
  if (busy.value || !s.connected) return
  const current = currentContext(),
    planId = s.planId
  busy.value = true
  try {
    await request('/api/v1/financial-plans/' + planId + '/replan', { method: 'POST' })
    if (!current()) return
    await Promise.all([plan.refetch(), versions.refetch()])
    if (!current()) return
    ui.notify('服务器已重新计算资金方案')
  } catch (e) {
    if (!current()) return
    ui.notify((e as Error).message)
  } finally {
    if (active) busy.value = false
  }
}
const labels: Record<string, string> = {
  conservative: '保守',
  balanced: '平衡',
  aggressive: '积极',
}
const selectedOption = computed(() =>
  plan.data.value?.result.options.find((o) => o.name === selected.value),
)
watch(
  () => plan.data.value?.version.id,
  () => {
    const view = plan.data.value
    if (!view) return
    selected.value =
      view.decisions.find((d) => d.plan_version_id === view.version.id)?.option ||
      (view.result.options.some((o) => o.name === selected.value)
        ? selected.value
        : view.result.options[0]?.name || '')
  },
)
const allocationChart = computed(() => ({
  tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
  legend: { bottom: 0, icon: 'circle' },
  grid: { top: 12, bottom: 40, left: 8, right: 16, containLabel: true },
  xAxis: {
    type: 'value',
    axisLabel: { formatter: (n: number) => '¥' + n.toLocaleString('zh-CN') },
  },
  yAxis: {
    type: 'category',
    inverse: true,
    data: plan.data.value?.result.options.map((o) => (labels[o.name] || o.name) + '方案') || [],
  },
  series: [
    {
      name: '建议储蓄',
      type: 'bar',
      stack: 'funds',
      barWidth: 22,
      itemStyle: { color: '#0D9488' },
      data: plan.data.value?.result.options.map((o) => o.savings_minor / 100) || [],
    },
    {
      name: '保留资金',
      type: 'bar',
      stack: 'funds',
      barWidth: 22,
      itemStyle: { color: '#B9C9D8', borderRadius: [0, 4, 4, 0] },
      data: plan.data.value?.result.options.map((o) => o.retained_minor / 100) || [],
    },
  ],
}))
const previous = computed(() =>
  versions.data.value?.find((v) => v.version === plan.data.value?.version.previous_version),
)
function change(key: 'disposable_minor' | 'necessary_reserve_minor' | 'suggested_savings_minor') {
  return previous.value && plan.data.value
    ? plan.data.value.result[key] - previous.value.result[key]
    : undefined
}
function goalName(id: string) {
  return savings.data.value?.find((g) => g.id === id)?.name || '目标金 #' + id
}
const operations = useOperations()
const pending = computed(
  () =>
    operations.data.value?.filter(
      (o) =>
        ['prepared', 'pending_authorization', 'authorized', 'pending_approval'].includes(
          o.status,
        ) && plan.data.value?.plan.settings.account_ids.map(String).includes(o.source_account_id),
    ) || [],
)
const execution = ref<InstanceType<typeof ExecutionDialog>>()
const confirmOpen = ref(false),
  confirming = ref(false),
  confirmError = ref('')
const captured = ref<{
  planId: string
  version: number
  digest: string
  option: string
  amount: number
}>()
function openConfirmation() {
  const view = plan.data.value,
    option = selectedOption.value
  if (!view || !option?.feasible) return
  captured.value = {
    planId: view.plan.id,
    version: view.version.version,
    digest: view.version.data_digest,
    option: option.name,
    amount: option.savings_minor,
  }
  confirmError.value = ''
  confirmOpen.value = true
}
async function confirmAdvice() {
  if (confirming.value || !captured.value || !s.connected || captured.value.planId !== s.planId)
    return
  const current = currentContext()
  confirming.value = true
  confirmError.value = ''
  try {
    const v = captured.value
    await request<Schema<'FinancialPlanDecision'>>(
      `/api/v1/financial-plans/${v.planId}/decisions`,
      {
        method: 'POST',
        body: {
          expected_version: v.version,
          data_digest: v.digest,
          option: v.option,
          confirm_advice: true,
        },
      },
    )
    if (!current()) return
    await Promise.all([plan.refetch(), versions.refetch()])
    if (!current()) return
    confirmOpen.value = false
    ui.notify('规划建议已由服务器确认，未执行资金划转')
  } catch (e) {
    if (!current()) return
    confirmError.value = (e as Error).message
    await Promise.all([plan.refetch(), versions.refetch()])
  } finally {
    if (active) confirming.value = false
  }
}
</script>
<template>
  <div class="page funds-workbench">
    <header class="funds-heading">
      <div>
        <h1>动态资金规划</h1>
        <p>先保障必要开支，再安排目标储蓄。</p>
      </div>
      <div>
        <Button variant="outline" :disabled="busy || confirming" @click="showForm = !showForm"
          ><Plus :size="15" />{{ s.planId ? '调整规划约束' : '新建规划' }}</Button
        ><Button v-if="s.planId" :disabled="!s.connected || busy" @click="replan"
          ><RefreshCw :size="15" />{{ busy ? '计算中…' : '重新规划' }}</Button
        >
      </div>
    </header>
    <details class="plan-lookup">
      <summary>
        查询已有资金规划<span v-if="s.planId">当前 #{{ s.planId }}</span>
      </summary>
      <form @submit.prevent="lookupPlan">
        <label
          >规划编号<input
            v-model="lookup"
            :disabled="busy || confirming"
            placeholder="已有规划 ID"
            required
            pattern="[1-9][0-9]*" /></label
        ><Button variant="outline" type="submit" :disabled="!s.connected || busy || confirming">查询规划</Button>
      </form>
    </details>
    <section v-if="showForm" class="panel planning-form">
      <h2>资金规划约束</h2>
      <form @submit.prevent="save">
        <div class="form-columns">
          <label>计划名称<input v-model="name" required /></label
          ><label
            >正常生活开支预留 / 元<input
              v-model="living"
              required
              inputmode="decimal"
              placeholder="0.00" /></label
          ><label
            >必要支出预留 / 元<input
              v-model="essential"
              required
              inputmode="decimal"
              placeholder="0.00" /></label
          ><label
            >安全储备 / 元<Button
              aria-label="应用资金安全偏好"
              type="button"
              variant="outline"
              :disabled="busy || !s.connected"
              @click="applyProfileReserve"
              >应用资金安全偏好</Button
            ><input
              v-model="safety"
              aria-label="安全储备 / 元"
              required
              inputmode="decimal"
              placeholder="0.00" /></label
          ><label
            >现金缓冲 / 元<input
              v-model="buffer"
              required
              inputmode="decimal"
              placeholder="0.00" /></label
          ><label
            >基准月收入 / 元<input v-model="income" required inputmode="decimal" placeholder="0.00"
          /></label>
        </div>
        <fieldset class="planning-goal-selection">
          <legend>关联健康目标金 · 至少选择一项</legend>
          <p v-if="!savings.data.value?.length" class="form-hint">
            创建目标金后，可以将其加入储蓄规划。
          </p>
          <label v-for="goal in savings.data.value" :key="goal.id" class="checkbox-label"
            ><input type="checkbox" v-model="selectedSavings" :value="goal.id" />{{ goal.name }} ·
            目标 {{ money(goal.target_minor) }}</label
          >
        </fieldset>
        <p v-if="saveError" class="form-error" role="alert">{{ saveError }}</p>
        <div class="row-between">
          <small>使用顶部当前账户；预期收入不会算作现有可用余额。</small
          ><Button type="submit" :disabled="!s.connected || !s.accountId || busy">{{
            busy ? '计算中…' : '保存并计算方案'
          }}</Button>
        </div>
      </form>
    </section>

    <State
      :connected="s.connected"
      :loading="plan.isLoading.value"
      :error="plan.error.value"
      :empty="!s.planId"
      title="尚未选择资金规划"
      description="保存预算约束，或查询已有规划查看资金方案。"
      @retry="plan.refetch()"
    >
      <template v-if="plan.data.value">
        <section class="funds-summary">
          <div class="disposable-main">
            <div>
              <span>当前可支配资金</span
              ><span
                >{{ plan.data.value.result.period }} · v{{ plan.data.value.version.version }}</span
              >
            </div>
            <strong>{{ money(plan.data.value.result.disposable_minor) }}</strong>
            <p>
              账户资金 {{ money(plan.data.value.result.balance_minor) }} · 已扣除服务端预留与约束
            </p>
            <span
              v-if="change('disposable_minor') != null && change('disposable_minor') !== 0"
              class="funds-change"
              >较前版 {{ change('disposable_minor')! > 0 ? '+' : ''
              }}{{ money(change('disposable_minor')) }}</span
            >
          </div>
          <div class="funds-metrics">
            <div>
              <span>必要支出预留</span
              ><strong>{{ money(plan.data.value.result.necessary_reserve_minor) }}</strong
              ><small v-if="change('necessary_reserve_minor')"
                >较前版 {{ change('necessary_reserve_minor')! > 0 ? '+' : ''
                }}{{ money(change('necessary_reserve_minor')) }}</small
              ><small v-else>保障必要生活支出</small>
            </div>
            <div>
              <span>安全缓冲资金</span
              ><strong>{{
                money(
                  plan.data.value.result.safety_reserve_minor +
                    plan.data.value.result.cashflow_buffer_minor,
                )
              }}</strong
              ><small>安全储备与现金流缓冲之和</small>
            </div>
            <div>
              <span>建议储蓄金额</span
              ><strong class="teal">{{
                money(plan.data.value.result.suggested_savings_minor)
              }}</strong
              ><small v-if="change('suggested_savings_minor')"
                >较前版 {{ change('suggested_savings_minor')! > 0 ? '+' : ''
                }}{{ money(change('suggested_savings_minor')) }}</small
              ><small v-else>当前服务端规划结果</small>
            </div>
          </div>
        </section>
        <div class="latest-trigger">
          <div>
            <b>最新调整原因</b>
            <p>{{ plan.data.value.version.reason }}</p>
          </div>
          <span v-if="plan.data.value.version.event_id"
            >事件 #{{ plan.data.value.version.event_id }}</span
          ><span class="sync-label">每 5 秒同步</span>
        </div>
        <section class="funds-panel option-comparison">
          <header class="funds-section-heading">
            <div>
              <h2>资金分配方案</h2>
              <p>比较储蓄与保留资金，选择适合当前预算的方案。</p>
            </div>
            <span>单位 · 人民币</span>
          </header>
          <div class="allocation-overview">
            <div class="option-list">
              <button
                v-for="o in plan.data.value.result.options"
                :key="o.name"
                :class="{ selected: selected === o.name }"
                :aria-pressed="selected === o.name"
                @click="selected = o.name"
              >
                <div>
                  <b>{{ labels[o.name] || o.name }}方案</b
                  ><span>{{ o.feasible ? '可行' : '约束冲突' }}</span>
                </div>
                <strong>{{ money(o.savings_minor) }}</strong>
                <div class="option-retained">
                  <span>建议储蓄</span><span>保留 {{ money(o.retained_minor) }}</span>
                </div>
              </button>
            </div>
            <div class="allocation-chart">
              <h3>方案金额对比</h3>
              <Chart :option="allocationChart" label="服务端资金方案建议储蓄与保留资金对比" />
            </div>
          </div>
          <div v-if="selectedOption" class="selected-allocation">
            <div class="allocation-explanation">
              <h3>{{ labels[selectedOption.name] || selectedOption.name }}方案 · 资金安排</h3>
              <p>{{ selectedOption.explanation }}</p>
              <p v-if="!selectedOption.feasible" class="form-error">
                此方案存在约束冲突，暂不可确认。
              </p>
            </div>
            <div class="allocation-table">
              <table>
                <thead>
                  <tr>
                    <th>储蓄目标</th>
                    <th>本期分配</th>
                    <th>未满足金额</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="a in selectedOption.allocations" :key="a.goal_id">
                    <td>{{ goalName(a.goal_id) }}</td>
                    <td>{{ money(a.amount_minor) }}</td>
                    <td>{{ money(a.unmet_minor) }}</td>
                  </tr>
                </tbody>
              </table>
              <p v-if="!selectedOption.allocations.length">此方案未返回目标分配明细。</p>
            </div>
          </div>
          <div
            v-if="
              plan.data.value.result.review_notes.length || plan.data.value.result.blockers.length
            "
            class="planning-notes"
          >
            <p v-for="note in plan.data.value.result.review_notes" :key="note">{{ note }}</p>
            <p v-for="note in plan.data.value.result.blockers" :key="note" class="form-error">
              {{ note }}
            </p>
          </div>
          <details v-if="plan.data.value.constraints.length" class="constraint-details">
            <summary>本次规划预算约束 · {{ plan.data.value.constraints.length }} 项</summary>
            <div v-for="c in plan.data.value.constraints" :key="c.id">
              <span>{{ c.description }}</span
              ><b>{{ money(c.amount_minor) }}</b>
            </div>
          </details>
        </section>
        <section
          class="advice-confirmation"
          :class="{ awaiting: plan.data.value.version.needs_confirmation }"
        >
          <ShieldCheck :size="21" />
          <div>
            <h2>
              {{
                plan.data.value.version.needs_confirmation ? '待确认规划建议' : '当前版本规划建议'
              }}
            </h2>
            <p>
              {{
                plan.data.value.version.needs_confirmation
                  ? '预算变化后，请核对本次资金安排。确认仅记录方案选择。'
                  : '查看规划记录；实际目标金存入与转账仍需单独授权。'
              }}
            </p>
          </div>
          <Button
            v-if="plan.data.value.version.needs_confirmation"
            :disabled="!selectedOption?.feasible || confirming"
            @click="openConfirmation"
            >确认{{ labels[selected] || selected }}方案</Button
          ><RouterLink v-else to="/security" class="text-link"
            >金融执行中心<ArrowRight :size="14"
          /></RouterLink>
        </section>
        <section
          v-if="pending.length || operations.error.value"
          class="funds-panel pending-operations"
        >
          <header class="funds-section-heading">
            <div>
              <h2>关联账户待授权操作</h2>
              <p>以下操作独立于规划建议，需核对详情与风险后授权。</p>
            </div>
            <RouterLink to="/security" class="text-link"
              >全部操作<ArrowRight :size="14"
            /></RouterLink>
          </header>
          <State
            :connected="s.connected"
            :error="operations.error.value"
            @retry="operations.refetch()"
            ><button v-for="o in pending" :key="o.id" @click="execution?.inspect(o.id)">
              <div>
                <b>操作 #{{ o.id }}</b
                ><span>{{ o.kind }}</span>
              </div>
              <strong>{{ money(o.amount_minor) }}</strong
              ><Status :value="o.status" /><ArrowRight :size="15" /></button
          ></State>
        </section>
        <section class="funds-panel plan-history-panel">
          <header class="funds-section-heading">
            <div>
              <h2>计划版本与资金变化</h2>
              <p>查看触发原因、对应事件和新旧方案差异。</p>
            </div>
            <span>服务端版本记录</span>
          </header>
          <State
            :connected="s.connected"
            :loading="versions.isLoading.value"
            :error="versions.error.value"
            :empty="!versions.data.value?.length"
            @retry="versions.refetch()"
            ><PlanHistory :versions="versions.data.value || []"
          /></State>
        </section>
      </template>
    </State>
    <ExecutionDialog ref="execution" />
    <DialogRoot v-model:open="confirmOpen"
      ><DialogPortal
        ><DialogOverlay class="advice-overlay" /><DialogContent
          class="advice-dialog"
          @escape-key-down="confirming && $event.preventDefault()"
          @pointer-down-outside="confirming && $event.preventDefault()"
          ><DialogTitle class="advice-title">确认资金规划建议</DialogTitle
          ><DialogDescription class="advice-description"
            >请核对方案与版本。此操作记录规划选择，不划转账户资金。</DialogDescription
          >
          <dl v-if="captured">
            <div>
              <dt>规划编号 / 版本</dt>
              <dd>#{{ captured.planId }} · v{{ captured.version }}</dd>
            </div>
            <div>
              <dt>选择方案</dt>
              <dd>{{ labels[captured.option] || captured.option }}方案</dd>
            </div>
            <div>
              <dt>建议储蓄金额</dt>
              <dd>{{ money(captured.amount) }}</dd>
            </div>
          </dl>
          <p
            v-if="captured && captured.version !== plan.data.value?.version.version"
            class="form-error"
          >
            服务端已有新版本，请关闭弹窗并重新核对。
          </p>
          <p v-if="confirmError" class="form-error" role="alert">{{ confirmError }}</p>
          <div class="advice-actions">
            <DialogClose as-child
              ><Button variant="outline" :disabled="confirming">返回核对</Button></DialogClose
            ><Button
              :disabled="confirming || captured?.version !== plan.data.value?.version.version"
              @click="confirmAdvice"
              >{{ confirming ? '服务器确认中…' : '确认规划建议' }}</Button
            >
          </div>
          <DialogClose class="advice-close" aria-label="关闭确认窗口" :disabled="confirming"
            ><X :size="18" /></DialogClose></DialogContent></DialogPortal
    ></DialogRoot>
  </div>
</template>
<style scoped>
.funds-workbench {
  display: flex;
  flex-direction: column;
  gap: 24px;
  color: #172033;
}
.funds-heading {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  align-items: center;
}
.funds-heading h1 {
  font-size: 28px;
  letter-spacing: -0.6px;
  margin: 0;
}
.funds-heading p {
  font-size: 14px;
  color: #64748b;
  margin: 8px 0 0;
}
.funds-heading > div:last-child {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.plan-lookup {
  font-size: 14px;
  color: #475569;
}
.plan-lookup summary {
  cursor: pointer;
  display: flex;
  align-items: center;
  gap: 16px;
  list-style: revert;
}
.plan-lookup summary span {
  color: #64748b;
  font-size: 13px;
}
.plan-lookup form {
  display: flex;
  align-items: end;
  gap: 12px;
  margin-top: 16px;
}
.plan-lookup input {
  max-width: 360px;
}
.funds-summary {
  display: grid;
  grid-template-columns: minmax(280px, 0.95fr) minmax(0, 1.5fr);
  border: 1px solid #e7edf3;
  border-radius: 14px;
  overflow: hidden;
  background: white;
}
.disposable-main {
  background: #142d4e;
  color: white;
  padding: 28px;
}
.disposable-main > div {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  font-size: 14px;
  color: #d3dfe9;
}
.disposable-main > div > span:last-child {
  font-size: 12px;
}
.disposable-main > strong {
  display: block;
  font-size: 40px;
  font-weight: 600;
  letter-spacing: -1px;
  margin: 20px 0 12px;
  font-variant-numeric: tabular-nums;
}
.disposable-main p {
  font-size: 13px;
  line-height: 1.7;
  color: #cfdae5;
  margin: 0;
}
.funds-change {
  display: inline-block;
  font-size: 13px;
  padding: 4px 8px;
  background: #ffffff14;
  border-radius: 5px;
  margin-top: 12px;
}
.funds-metrics {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  align-items: center;
  padding: 24px 8px;
}
.funds-metrics > div {
  padding: 4px 20px;
  border-right: 1px solid #e7edf3;
  display: grid;
  gap: 12px;
}
.funds-metrics > div:last-child {
  border: 0;
}
.funds-metrics span {
  font-size: 14px;
  color: #475569;
}
.funds-metrics strong {
  font-size: 23px;
  font-weight: 600;
  letter-spacing: -0.5px;
  font-variant-numeric: tabular-nums;
}
.funds-metrics small {
  font-size: 13px;
  line-height: 1.6;
  color: #64748b;
}
.latest-trigger {
  display: flex;
  gap: 16px;
  align-items: center;
  padding: 16px 20px;
  background: #edf4f5;
  border-left: 3px solid #0d9488;
  border-radius: 0 8px 8px 0;
}
.latest-trigger > div {
  flex: 1;
  min-width: 0;
}
.latest-trigger b {
  font-size: 14px;
  font-weight: 600;
}
.latest-trigger p {
  font-size: 14px;
  line-height: 1.65;
  margin: 6px 0 0;
  color: #475569;
  overflow-wrap: anywhere;
}
.latest-trigger > span {
  font-size: 13px;
  color: #475569;
  white-space: nowrap;
}
.latest-trigger .sync-label {
  color: #64748b;
  font-size: 12px;
}
.funds-panel {
  background: white;
  border: 1px solid #e7edf3;
  border-radius: 14px;
  overflow: hidden;
}
.funds-section-heading {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  padding: 24px;
}
.funds-section-heading h2 {
  font-size: 17px;
  font-weight: 600;
  margin: 0;
}
.funds-section-heading p {
  font-size: 14px;
  color: #64748b;
  margin: 6px 0 0;
  line-height: 1.6;
}
.funds-section-heading > span {
  font-size: 13px;
  color: #64748b;
  white-space: nowrap;
}
.allocation-overview {
  display: grid;
  grid-template-columns: minmax(0, 1.3fr) minmax(0, 1fr);
  gap: 24px;
  padding: 0 24px 24px;
}
.option-list {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
}
.option-list button {
  background: #f8fafc;
  border: 1px solid #e7edf3;
  border-radius: 10px;
  text-align: left;
  padding: 16px;
  transition:
    background 0.18s,
    border-color 0.18s;
  cursor: pointer;
  min-width: 0;
}
.option-list button:hover {
  border-color: #94b6b3;
}
.option-list button.selected {
  border-color: #0d9488;
  background: #f0f9f6;
  box-shadow: inset 0 0 0 1px #0d9488;
}
.option-list button > div:first-child {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  justify-content: space-between;
}
.option-list b {
  font-size: 15px;
  font-weight: 600;
}
.option-list button > div:first-child span {
  font-size: 12px;
  color: #64748b;
}
.option-list strong {
  display: block;
  font-size: 23px;
  font-weight: 600;
  letter-spacing: -0.5px;
  font-variant-numeric: tabular-nums;
  margin: 24px 0 8px;
}
.option-retained {
  display: grid;
  gap: 18px;
  font-size: 13px;
  color: #475569;
}
.option-retained span:last-child {
  padding-top: 12px;
  border-top: 1px solid #dfe9eb;
}
.allocation-chart,
.option-list {
  min-width: 0;
}
.allocation-chart h3 {
  font-size: 14px;
  margin: 0 0 4px;
  font-weight: 500;
  color: #475569;
}
.allocation-chart :deep(.chart) {
  height: 194px;
}
.selected-allocation {
  display: grid;
  grid-template-columns: minmax(0, 0.85fr) minmax(0, 1.4fr);
  gap: 24px;
  padding: 24px;
  border-top: 1px solid #e7edf3;
}
.allocation-explanation h3 {
  font-size: 15px;
  margin: 0 0 10px;
  font-weight: 600;
}
.allocation-explanation p {
  font-size: 14px;
  line-height: 1.8;
  margin: 0;
  color: #475569;
}
.allocation-table {
  overflow: auto;
}
.allocation-table table {
  width: 100%;
  border-collapse: collapse;
  font-size: 14px;
}
.allocation-table th {
  text-align: right;
  background: #f7f9fb;
  color: #64748b;
  font-size: 13px;
  font-weight: 500;
  padding: 10px 12px;
  white-space: nowrap;
}
.allocation-table td {
  padding: 14px 12px;
  border-bottom: 1px solid #e7edf3;
  text-align: right;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.allocation-table th:first-child,
.allocation-table td:first-child {
  text-align: left;
  white-space: normal;
  min-width: 100px;
}
.allocation-table td:nth-child(2) {
  font-weight: 600;
  color: #0d766e;
}
.allocation-table > p {
  font-size: 14px;
  color: #64748b;
}
.planning-notes {
  padding: 16px 24px;
  background: #fafbf7;
  border-top: 1px solid #edf1e7;
}
.planning-notes p {
  font-size: 14px;
  margin: 4px 0;
  line-height: 1.7;
}
.constraint-details {
  padding: 16px 24px;
  font-size: 14px;
  border-top: 1px solid #e7edf3;
}
.constraint-details summary {
  cursor: pointer;
  color: #475569;
}
.constraint-details > div {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 0;
  border-bottom: 1px solid #e7edf3;
}
.constraint-details b {
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.advice-confirmation {
  display: flex;
  align-items: center;
  gap: 16px;
  border: 1px solid #e7edf3;
  border-radius: 12px;
  padding: 20px 24px;
  background: #fff;
}
.advice-confirmation.awaiting {
  border-color: #eddbb9;
  background: #fffcf5;
}
.advice-confirmation > svg {
  color: #0d766e;
  flex: none;
}
.advice-confirmation > div {
  flex: 1;
  min-width: 0;
}
.advice-confirmation h2 {
  font-size: 16px;
  margin: 0 0 6px;
}
.advice-confirmation p {
  font-size: 14px;
  line-height: 1.7;
  margin: 0;
  color: #475569;
}
.pending-operations :deep(.state) {
  padding: 24px;
}
.pending-operations button {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 16px;
  border: 0;
  background: white;
  padding: 16px 24px;
  border-top: 1px solid #e7edf3;
  text-align: left;
  cursor: pointer;
}
.pending-operations button:hover {
  background: #f8fafc;
}
.pending-operations button > div {
  display: grid;
  gap: 6px;
  flex: 1;
  min-width: 0;
}
.pending-operations b {
  font-size: 14px;
  font-weight: 500;
}
.pending-operations button > div > span {
  font-size: 13px;
  color: #64748b;
  overflow-wrap: anywhere;
}
.pending-operations strong {
  font-size: 16px;
  font-weight: 600;
}
.plan-history-panel > .funds-section-heading {
  border-bottom: 1px solid #e7edf3;
}
.planning-form {
  border: 1px solid #e7edf3;
  box-shadow: none;
}
.funds-workbench label {
  font-size: 14px;
}
.planning-form h2 {
  font-size: 17px;
  margin: 0 0 20px;
}
.planning-form small {
  font-size: 13px;
}
.planning-form fieldset {
  margin: 20px 0;
}
.planning-form .checkbox-label {
  font-size: 14px;
}
.planning-form :deep(input[type='checkbox']) {
  width: 16px;
  height: 16px;
}
.advice-overlay {
  position: fixed;
  inset: 0;
  background: #10243f66;
  z-index: 70;
  backdrop-filter: blur(2px);
}
.advice-dialog {
  position: fixed;
  left: 50%;
  top: 50%;
  transform: translate(-50%, -50%);
  z-index: 71;
  background: white;
  border: 1px solid #e7edf3;
  border-radius: 16px;
  box-shadow: 0 24px 80px #142d4e26;
  padding: 32px;
  width: calc(100vw - 32px);
  max-width: 480px;
  max-height: calc(100dvh - 32px);
  overflow: auto;
}
.advice-title {
  font-size: 21px;
  font-weight: 600;
  color: #172033;
  margin: 0 0 12px;
}
.advice-description {
  font-size: 14px;
  line-height: 1.8;
  color: #64748b;
  margin: 0 0 24px;
}
.advice-dialog dl {
  background: #f6f8fb;
  padding: 0 16px;
  border-radius: 10px;
}
.advice-dialog dl > div {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 16px 0;
  border-bottom: 1px solid #e7edf3;
  font-size: 14px;
}
.advice-dialog dl > div:last-child {
  border: 0;
}
.advice-dialog dt {
  color: #64748b;
}
.advice-dialog dd {
  margin: 0;
  font-weight: 600;
  color: #142d4e;
  font-variant-numeric: tabular-nums;
}
.advice-actions {
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 24px;
}
.advice-close {
  position: absolute;
  right: 16px;
  top: 16px;
  padding: 4px;
  border: 0;
  background: none;
  color: #64748b;
  cursor: pointer;
}
.option-list button:focus-visible {
  outline: 2px solid #0d9488;
  outline-offset: 3px;
}
@media (max-width: 1250px) {
  .funds-summary {
    grid-template-columns: minmax(260px, 0.9fr) minmax(0, 1.5fr);
  }
  .funds-metrics > div {
    padding: 4px 12px;
  }
  .funds-metrics strong {
    font-size: 20px;
  }
  .disposable-main {
    padding: 24px;
  }
  .disposable-main > strong {
    font-size: 36px;
  }
  .allocation-overview {
    grid-template-columns: minmax(0, 1fr);
  }
  .allocation-chart :deep(.chart) {
    height: 180px;
  }
  .option-list strong {
    font-size: 25px;
    margin-top: 16px;
  }
  .option-retained {
    gap: 10px;
  }
  .funds-section-heading {
    padding: 20px 24px;
  }
}
@media (max-width: 950px) {
  .funds-summary {
    grid-template-columns: 1fr;
  }
  .funds-metrics {
    padding: 20px 12px;
  }
  .disposable-main > strong {
    margin: 14px 0;
  }
  .selected-allocation {
    grid-template-columns: 1fr;
    gap: 16px;
  }
  .latest-trigger .sync-label {
    display: none;
  }
}
@media (max-width: 600px) {
  .funds-workbench {
    gap: 20px;
  }
  .funds-heading {
    align-items: flex-start;
    flex-direction: column;
  }
  .funds-heading h1 {
    font-size: 25px;
  }
  .funds-heading > div:last-child {
    width: 100%;
  }
  .funds-heading > div:last-child > button {
    flex: 1;
  }
  .funds-metrics {
    grid-template-columns: 1fr;
    padding: 8px 20px;
  }
  .funds-metrics > div {
    grid-template-columns: 1fr auto;
    gap: 6px;
    padding: 16px 0;
    border-right: 0;
    border-bottom: 1px solid #e7edf3;
  }
  .funds-metrics small {
    grid-column: 1/-1;
  }
  .funds-metrics strong {
    font-size: 21px;
  }
  .disposable-main {
    padding: 24px 20px;
  }
  .disposable-main > strong {
    font-size: 34px;
  }
  .disposable-main > div {
    flex-wrap: wrap;
  }
  .latest-trigger {
    align-items: flex-start;
    flex-direction: column;
    gap: 8px;
  }
  .funds-section-heading {
    padding: 20px 16px;
    align-items: flex-start;
  }
  .funds-section-heading > span {
    display: none;
  }
  .allocation-overview {
    padding: 0 16px 20px;
    gap: 20px;
  }
  .option-list {
    grid-template-columns: 1fr;
    gap: 8px;
  }
  .option-list button {
    display: grid;
    grid-template-columns: 1fr auto;
    padding: 14px 16px;
    gap: 6px 12px;
  }
  .option-list strong {
    margin: 0;
    font-size: 22px;
    grid-column: 2;
    grid-row: 1/3;
    align-self: center;
  }
  .option-list button > div:first-child {
    justify-content: flex-start;
  }
  .option-retained {
    gap: 5px;
  }
  .option-retained span:last-child {
    border: 0;
    padding: 0;
  }
  .option-retained span:first-child {
    display: none;
  }
  .selected-allocation {
    padding: 20px 16px;
  }
  .allocation-table table {
    min-width: 310px;
  }
  .planning-notes,
  .constraint-details {
    padding: 16px;
  }
  .advice-confirmation {
    flex-wrap: wrap;
    padding: 20px 16px;
    gap: 12px;
  }
  .advice-confirmation > button,
  .advice-confirmation > a {
    margin-left: 33px;
  }
  .pending-operations button {
    padding: 16px;
    gap: 10px;
    flex-wrap: wrap;
  }
  .pending-operations button > div {
    min-width: 100%;
  }
  .advice-dialog {
    padding: 28px 20px;
  }
  .advice-title {
    font-size: 20px;
  }
  .planning-form .row-between {
    flex-direction: column;
    align-items: stretch;
    gap: 16px;
  }
  .plan-lookup form {
    flex-wrap: wrap;
  }
  .plan-lookup label {
    width: 100%;
  }
  .plan-lookup summary {
    flex-wrap: wrap;
    gap: 8px;
  }
  .plan-history-panel > .funds-section-heading p {
    font-size: 13px;
  }
}
</style>
