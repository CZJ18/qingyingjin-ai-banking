<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { Plus, ArrowRight, ShieldCheck } from 'lucide-vue-next'
import { api, request, post, type Schema } from '@/api/client'
import { useSession, useUI } from '@/lib/store'
import { useSavings } from '@/lib/queries'
import { money, minor, date } from '@/lib/utils'
import Button from '@/components/ui/button/Button.vue'
import State from '@/components/State.vue'
import Status from '@/components/Status.vue'
import JsonDetails from '@/components/JsonDetails.vue'
import ExecutionDialog from '@/components/ExecutionDialog.vue'
import GoalSettings from '@/components/GoalSettings.vue'
import LifestyleGoals from '@/components/LifestyleGoals.vue'
function editLife() {
  text.value = life.data.value?.version.text || ''
  edit.value = true
  showLifeEditor.value = true
}
const s = useSession(),
  ui = useUI(),
  savings = useSavings(),
  dialog = ref<InstanceType<typeof ExecutionDialog>>(),
  text = ref(''),
  busy = ref(false),
  lifeId = ref(s.goalIds[0] || ''),
  ledgerId = ref(''),
  name = ref(''),
  target = ref(''),
  monthly = ref(''),
  ends = ref(''),
  edit = ref(false),
  transferAmount = ref('')
const life = useQuery({
  queryKey: computed(() => ['life-goal', lifeId.value]),
  enabled: computed(() => s.connected && !!lifeId.value),
  queryFn: ({ signal }) =>
    request<Schema<'LifeGoalView'>>('/api/v1/goals/' + lifeId.value, { signal }),
})
const ledger = useQuery({
  queryKey: computed(() => ['savings-ledger', ledgerId.value]),
  enabled: computed(() => s.connected && !!ledgerId.value),
  queryFn: ({ signal }) =>
    request<Schema<'SavingsLedger'>[]>(`/api/v1/savings/goals/${ledgerId.value}/ledger?limit=100`, {
      signal,
    }),
})
async function createLife() {
  if (busy.value) return
  busy.value = true
  try {
    if (edit.value && life.data.value) {
      await request(`/api/v1/goals/${lifeId.value}`, {
        method: 'PUT',
        body: { text: text.value, expected_version: life.data.value.goal.current_version },
      })
    } else {
      const out = await post<'/api/v1/goals', Schema<'LifeGoalView'>>(
        '/api/v1/goals',
        { text: text.value },
        crypto.randomUUID(),
      )
      s.saveGoal(out.goal.id)
      lifeId.value = out.goal.id
    }
    text.value = ''
    edit.value = false
    await life.refetch()
    ui.notify('生活目标已保存，可继续解析并查看约束')
  } catch (e) {
    ui.notify((e as Error).message)
  } finally {
    busy.value = false
  }
}
async function parse() {
  if (!life.data.value || busy.value) return
  busy.value = true
  try {
    await request(`/api/v1/goals/${lifeId.value}/parse`, {
      method: 'POST',
      timeoutMs: 90000,
      body: { expected_version: life.data.value.goal.current_version },
    })
    await life.refetch()
    ui.notify('目标解析请求已完成')
  } catch (e) {
    ui.notify((e as Error).message)
  } finally {
    busy.value = false
  }
}
async function createFund() {
  try {
    const end = Date.parse(ends.value + 'T23:59:59+08:00')
    if (end <= Date.now()) throw new Error('截止日期必须晚于今天')
    await dialog.value?.prepare('create_savings_goal', {
      name: name.value,
      target_minor: minor(target.value),
      monthly_minor: minor(monthly.value),
      starts_at: Date.now(),
      ends_at: end,
      currency: 'CNY',
    })
  } catch (e) {
    ui.notify((e as Error).message)
  }
}
async function fundMove(kind: Schema<'ExecutionPrepareInput'>['kind']) {
  try {
    if (!s.accountId) throw new Error('请选择用于目标金划转的当前账户')
    await dialog.value?.prepare(kind, {
      goal_id: ledgerId.value,
      input: {
        bank_account_id: s.accountId,
        amount_minor: minor(transferAmount.value),
        description: '健康生活目标金划转',
      },
    })
  } catch (e) {
    ui.notify((e as Error).message)
  }
}

function toggleLifeEditor() {
  if (!showLifeEditor.value) {
    edit.value = false
    text.value = ''
  }
  showLifeEditor.value = !showLifeEditor.value
}
const showCreateFund = ref(false),
  showLifeEditor = ref(false),
  ledgerFilter = ref('')
const activeGoal = computed(() => savings.data.value?.find((g) => g.id === ledgerId.value))
const totalFunds = computed(() =>
  savings.data.value?.reduce((total, g) => total + g.current_minor, 0),
)
watch(
  () => savings.data.value,
  (rows) => {
    if (rows?.length && !rows.some((g) => g.id === ledgerId.value)) ledgerId.value = rows[0]!.id
  },
  { immediate: true },
)
const goalPlan = useQuery({
  queryKey: computed(() => ['plan', s.planId]),
  enabled: computed(() => s.connected && !!s.planId),
  queryFn: ({ signal }) =>
    request<Schema<'FinancialPlanView'>>('/api/v1/financial-plans/' + s.planId, { signal }),
})
const recommendedAmount = computed(() => {
  const view = goalPlan.data.value
  if (!view || !activeGoal.value) return undefined
  const choice =
    view.decisions.find((d) => d.plan_version_id === view.version.id)?.option || 'balanced'
  return view.result.options
    .find((o) => o.name === choice)
    ?.allocations.find((a) => a.goal_id === activeGoal.value!.id)?.amount_minor
})
const filteredLedger = computed(
  () =>
    ledger.data.value?.filter(
      (e) =>
        !ledgerFilter.value ||
        (ledgerFilter.value === 'deposit' ? e.delta_minor > 0 : e.delta_minor < 0),
    ) || [],
)
function endDate(value: number) {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(value)
}
</script>
<template>
  <div class="page goals-bank">
    <header class="bank-heading">
      <div>
        <h1>健康目标与目标金</h1>
        <p>管理目标储蓄、期限与必要生活预算。</p>
      </div>
      <Button variant="outline" @click="showCreateFund = !showCreateFund"
        ><Plus :size="16" />{{ showCreateFund ? '收起创建表单' : '新建目标金' }}</Button
      >
    </header>
    <LifestyleGoals />
    <section v-if="showCreateFund" class="bank-panel create-fund-panel">
      <header class="bank-section-heading">
        <h2>设立健康目标金</h2>
        <p>填写储蓄计划，资金操作仍需单独授权。</p>
      </header>
      <form @submit.prevent="createFund">
        <label
          >目标名称<input v-model="name" required maxlength="128" placeholder="我的健康生活目标金"
        /></label>
        <div class="form-columns">
          <label
            >储蓄目标 / 元<input
              v-model="target"
              inputmode="decimal"
              required
              placeholder="0.00" /></label
          ><label
            >每月计划 / 元<input v-model="monthly" inputmode="decimal" required placeholder="0.00"
          /></label>
        </div>
        <label>目标截止日期<input v-model="ends" type="date" required /></label>
        <p class="form-hint">目标金创建需要授权。后续存入、取出也会分别生成操作意向。</p>
        <Button type="submit" :disabled="!s.connected" class="w-full"
          ><Plus :size="15" />创建目标金意向</Button
        >
      </form>
    </section>
    <div class="goal-selection">
      <label
        >当前目标金<select v-model="ledgerId" aria-label="当前目标金">
          <option v-if="!savings.data.value?.length" value="">暂无目标金</option>
          <option v-for="g in savings.data.value" :key="g.id" :value="g.id">{{ g.name }}</option>
        </select></label
      ><span
        >全部目标金余额 <b>{{ s.connected ? money(totalFunds) : '—' }}</b></span
      >
    </div>
    <State
      :connected="s.connected"
      :loading="savings.isLoading.value"
      :error="savings.error.value"
      :empty="!activeGoal"
      title="尚未设立目标金"
      description="新建目标金，设置储蓄金额与期限后跟踪资金进度。"
      @retry="savings.refetch()"
    >
      <section v-if="activeGoal" class="goal-balance-hero">
        <div class="goal-balance-main">
          <div class="goal-title">
            <span>当前目标金余额</span><Status :value="activeGoal.status" />
          </div>
          <h2>{{ activeGoal.name }}</h2>
          <strong>{{ money(activeGoal.current_minor) }}</strong>
          <div class="goal-progress-heading">
            <span>已完成 {{ (activeGoal.progress_basis_points / 100).toFixed(1) }}%</span
            ><b>储蓄目标 {{ money(activeGoal.target_minor) }}</b>
          </div>
          <div
            class="goal-balance-progress"
            role="progressbar"
            :aria-valuenow="Math.min(100, Math.max(0, activeGoal.progress_basis_points / 100))"
            :aria-valuemin="0"
            :aria-valuemax="100"
            aria-label="目标储蓄完成进度"
          >
            <i
              :style="{
                width: Math.min(100, Math.max(0, activeGoal.progress_basis_points / 100)) + '%',
              }"
            />
          </div>
        </div>
        <div class="goal-balance-context">
          <div>
            <span>目标期限</span><strong>{{ endDate(activeGoal.ends_at) }}</strong
            ><small>始于 {{ endDate(activeGoal.starts_at) }}</small>
          </div>
          <div>
            <span>本期建议储蓄</span><strong>{{ money(recommendedAmount) }}</strong
            ><small>{{
              recommendedAmount == null ? '尚无关联规划建议' : '来自当前资金规划方案'
            }}</small
            ><RouterLink to="/planning">查看资金规划<ArrowRight :size="13" /></RouterLink>
          </div>
          <div class="monthly-plan">
            <span>每月储蓄计划</span><b>{{ money(activeGoal.plan.monthly_minor) }}</b>
          </div>
        </div>
      </section>
      <State
        v-if="goalPlan.error.value"
        :connected="s.connected"
        :error="goalPlan.error.value"
        @retry="goalPlan.refetch()"
      />
      <div class="goal-workspace">
        <section class="bank-panel goal-ledger">
          <header class="bank-section-heading">
            <div>
              <h2>历史储蓄流水</h2>
              <p>目标金账户的实际存入与取出。</p>
            </div>
            <select v-model="ledgerFilter" aria-label="筛选目标金流水">
              <option value="">全部流水</option>
              <option value="deposit">存入</option>
              <option value="withdraw">取出</option>
            </select>
          </header>
          <form class="goal-transfer-form" @submit.prevent="fundMove('savings_deposit')">
            <label
              >划转金额 / 元<input
                v-model="transferAmount"
                inputmode="decimal"
                placeholder="0.00"
                required /></label
            ><Button type="submit" :disabled="!s.connected">存入目标金</Button
            ><Button
              variant="outline"
              :disabled="!s.connected || !transferAmount"
              @click="fundMove('savings_withdrawal')"
              >取出目标金</Button
            >
          </form>
          <p class="goal-transfer-note">
            <ShieldCheck :size="14" />使用顶部当前账户划转；核对意向并授权后才会执行。
          </p>
          <State
            :connected="s.connected"
            :loading="ledger.isLoading.value"
            :error="ledger.error.value"
            :empty="!filteredLedger.length"
            title="暂无匹配资金流水"
            description="已完成的目标金划转将在这里展示。"
            @retry="ledger.refetch()"
            ><div class="goal-table-wrap">
              <table class="goal-table">
                <thead>
                  <tr>
                    <th>时间</th>
                    <th>资金变动</th>
                    <th>转账编号</th>
                    <th>记账凭证</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="entry in filteredLedger" :key="entry.id">
                    <td>{{ date(entry.created_at) }}</td>
                    <td :class="{ incoming: entry.delta_minor > 0 }">
                      {{ entry.delta_minor > 0 ? '+' : '' }}{{ money(entry.delta_minor) }}
                    </td>
                    <td>{{ entry.transfer_id }}</td>
                    <td class="journal-cell">{{ entry.journal_id }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div class="ledger-details">
              <JsonDetails :value="ledger.data.value" label="查看目标金完整资金流水" /></div
          ></State>
          <div v-if="activeGoal" class="goal-settings-area">
            <GoalSettings :goal="activeGoal" />
          </div>
        </section>
        <section class="bank-panel goal-constraints">
          <header class="bank-section-heading">
            <div>
              <h2>目标与预算约束</h2>
              <p>合理生活开支优先。</p>
            </div>
          </header>
          <div v-if="activeGoal" class="goal-constraint-summary">
            <div>
              <span>关联目标金</span><b>{{ activeGoal.name }}</b>
            </div>
            <div>
              <span>目标金额</span><b>{{ money(activeGoal.target_minor) }}</b>
            </div>
            <div>
              <span>已设定月计划</span><b>{{ money(activeGoal.plan.monthly_minor) }}</b>
            </div>
          </div>
          <State
            v-if="lifeId"
            :connected="s.connected"
            :loading="life.isLoading.value"
            :error="life.error.value"
            @retry="life.refetch()"
            ><div class="life-goal-content">
              <h3>健康生活目标</h3>
              <p>{{ life.data.value?.version.text }}</p>
              <div class="life-actions">
                <Button variant="ghost" @click="editLife">修改</Button
                ><Button variant="outline" :disabled="busy" @click="parse">解析目标</Button>
              </div>
              <template v-if="life.data.value?.version.parsed"
                ><div class="parsed-summary">
                  <span>解析储蓄目标</span
                  ><b>{{ money(life.data.value.version.parsed.savings_target_minor) }}</b>
                </div>
                <div
                  v-for="(constraint, i) in life.data.value.version.parsed.constraints"
                  :key="i"
                  class="constraint-note"
                >
                  {{ constraint.description }}
                </div>
                <p
                  v-for="missing in life.data.value.version.parsed.missing_information"
                  :key="missing"
                  class="goal-missing"
                >
                  需补充：{{ missing }}
                </p></template
              ><JsonDetails
                :value="life.data.value?.version.parsed"
                label="查看目标解析结果与约束"
              /><JsonDetails
                :value="life.data.value?.task_plan"
                label="查看编译后的金融任务计划"
              /></div
          ></State>
          <div v-else class="no-life-goal">尚未关联健康生活目标，可在下方保存或查询已有目标。</div>
          <p class="goal-principle-note">
            目标金用于健康生活预算，不根据体重、少吃饭或过量运动给予资金奖惩。
          </p>
        </section>
      </div>
    </State>
    <section class="bank-panel life-goal-management">
      <header class="bank-section-heading">
        <div>
          <h2>生活目标管理</h2>
          <p>保存、查询与修改健康生活目标。</p>
        </div>
        <Button variant="outline" @click="toggleLifeEditor">{{
          showLifeEditor ? '收起编辑' : '新建生活目标'
        }}</Button>
      </header>
      <div class="life-goal-query">
        <label
          >已保存目标 / 查询目标 ID<input v-model="lifeId" placeholder="输入已有生活目标 ID"
        /></label>
        <div>
          <button
            v-for="id in s.goalIds"
            :key="id"
            :class="{ selected: lifeId === id }"
            @click="lifeId = id"
          >
            目标 #{{ id }}
          </button>
        </div>
      </div>
      <State
        v-if="!activeGoal && lifeId"
        :connected="s.connected"
        :loading="life.isLoading.value"
        :error="life.error.value"
        @retry="life.refetch()"
        ><div class="life-goal-content">
          <p>{{ life.data.value?.version.text }}</p>
          <div class="life-actions">
            <Button variant="ghost" @click="editLife">修改</Button
            ><Button variant="outline" :disabled="busy" @click="parse">解析目标</Button>
          </div>
          <div
            v-for="(constraint, i) in life.data.value?.version.parsed?.constraints"
            :key="i"
            class="constraint-note"
          >
            {{ constraint.description }}
          </div>
          <JsonDetails
            :value="life.data.value?.version.parsed"
            label="查看目标解析结果与约束"
          /><JsonDetails
            :value="life.data.value?.task_plan"
            label="查看编译后的金融任务计划"
          /></div
      ></State>
      <div v-if="showLifeEditor" class="life-goal-editor">
        <h3>{{ edit ? '修改生活目标' : '创建生活目标' }}</h3>
        <form @submit.prevent="createLife">
          <label
            >用自然语言描述你的健康生活计划<textarea
              v-model="text"
              rows="4"
              required
              maxlength="8000"
              placeholder="例如：未来三个月每周安排规律运动，保持正常生活开支，为健康生活储蓄…"
            />
          </label>
          <div class="row-between">
            <small>目标解析通过后端 Coze 工作流完成</small
            ><Button type="submit" :disabled="!s.connected || busy || !text.trim()"
              >{{ busy ? '处理中…' : '保存生活目标' }}<ArrowRight :size="14"
            /></Button>
          </div>
        </form>
      </div>
    </section>
    <ExecutionDialog ref="dialog" />
  </div>
</template>
<style scoped>
.goals-bank {
  padding: 24px 28px !important;
  max-width: 1600px;
  margin: auto;
  font-size: 14px;
  color: #172033;
}
.goals-bank label {
  font-size: 14px;
}
.bank-heading {
  display: flex;
  justify-content: space-between;
  gap: 20px;
  align-items: center;
  margin-bottom: 24px;
}
.bank-heading h1 {
  font-size: 28px;
  letter-spacing: -0.5px;
  margin: 0 0 9px;
  font-weight: 650;
}
.bank-heading p {
  font-size: 14px;
  margin: 0;
  color: #64748b;
}
.bank-panel {
  border: 1px solid #e7edf3;
  border-radius: 12px;
  background: #fff;
  min-width: 0;
  overflow: hidden;
}
.bank-section-heading {
  padding: 22px 24px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.bank-section-heading h2 {
  font-size: 17px;
  font-weight: 600;
  margin: 0 0 7px;
}
.bank-section-heading p {
  font-size: 13px;
  color: #64748b;
  line-height: 1.7;
  margin: 0;
}
.create-fund-panel {
  margin-bottom: 24px;
}
.create-fund-panel > form {
  padding: 0 24px 24px;
  max-width: 820px;
}
.goal-selection {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  margin-bottom: 16px;
}
.goals-bank .goal-selection > label {
  display: flex;
  flex-direction: row;
  align-items: center;
  gap: 12px;
  font-size: 14px;
}
.goal-selection select {
  max-width: 320px;
  border: 1px solid #e7edf3;
  border-radius: 7px;
  background: #fff;
  padding: 10px 12px;
  font-size: 14px;
}
.goal-selection > span {
  font-size: 13px;
  color: #64748b;
}
.goal-selection b {
  font-size: 17px;
  color: #142d4e;
  margin-left: 10px;
  font-variant-numeric: tabular-nums;
}
.goal-balance-hero {
  display: grid;
  grid-template-columns: minmax(0, 1.65fr) minmax(260px, 1fr);
  background: #fff;
  border: 1px solid #e7edf3;
  border-radius: 12px;
  overflow: hidden;
  margin-bottom: 24px;
}
.goal-balance-main {
  padding: 30px 32px;
  background: #142d4e;
  color: #fff;
}
.goal-title {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  color: #c6d5e4;
  font-size: 14px;
}
.goal-balance-main h2 {
  font-size: 18px;
  line-height: 1.6;
  font-weight: 500;
  margin: 20px 0 8px;
}
.goal-balance-main > strong {
  display: block;
  font-size: 44px;
  line-height: 1.3;
  letter-spacing: -1.2px;
  font-weight: 550;
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
}
.goal-progress-heading {
  display: flex;
  gap: 16px;
  justify-content: space-between;
  margin-top: 26px;
  font-size: 14px;
  color: #d8e4ef;
}
.goal-progress-heading b {
  font-weight: 500;
}
.goal-balance-progress {
  height: 7px;
  background: #ffffff25;
  border-radius: 6px;
  overflow: hidden;
  margin-top: 14px;
}
.goal-balance-progress i {
  display: block;
  height: 100%;
  background: #40c4ad;
  border-radius: 6px;
  transition: width 0.2s;
}
.goal-balance-context {
  padding: 24px 28px;
  display: grid;
  gap: 22px;
}
.goal-balance-context > div {
  display: grid;
  gap: 8px;
}
.goal-balance-context span {
  font-size: 14px;
  color: #64748b;
}
.goal-balance-context strong {
  font-size: 24px;
  font-weight: 600;
  color: #142d4e;
  font-variant-numeric: tabular-nums;
}
.goal-balance-context small {
  font-size: 13px;
  color: #64748b;
}
.goal-balance-context a {
  display: flex;
  gap: 6px;
  align-items: center;
  color: #0d766e;
  font-size: 13px;
}
.goal-balance-context .monthly-plan {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  border-top: 1px solid #e7edf3;
  padding-top: 16px;
}
.monthly-plan b {
  font-size: 17px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.goal-workspace {
  display: grid;
  grid-template-columns: minmax(0, 1.65fr) minmax(280px, 1fr);
  gap: 24px;
  align-items: start;
  margin-bottom: 24px;
}
.goal-ledger .bank-section-heading select {
  padding: 8px 10px;
  border: 1px solid #e7edf3;
  background: #fff;
  border-radius: 6px;
  font-size: 14px;
}
.goal-transfer-form {
  display: flex;
  align-items: flex-end;
  gap: 10px;
  padding: 0 24px 12px;
}
.goals-bank .goal-transfer-form > label {
  display: grid;
  gap: 8px;
  margin: 0;
  flex: 1;
  min-width: 0;
  font-size: 14px;
}
.goal-transfer-form input {
  height: 40px;
  width: 100%;
  font-size: 14px;
}
.goal-transfer-form :deep(.btn) {
  white-space: nowrap;
}
.goal-transfer-note {
  font-size: 13px;
  line-height: 1.7;
  color: #64748b;
  display: flex;
  align-items: flex-start;
  gap: 6px;
  margin: 0;
  padding: 0 24px 18px;
}
.goal-transfer-note svg {
  flex: none;
  margin-top: 3px;
}
.goal-table-wrap {
  overflow-x: auto;
}
.goal-table {
  border-collapse: collapse;
  width: 100%;
  font-size: 14px;
  table-layout: fixed;
}
.goal-table th {
  padding: 12px 20px;
  background: #f8fafc;
  border-block: 1px solid #e7edf3;
  font-size: 13px;
  color: #64748b;
  font-weight: 500;
  text-align: left;
}
.goal-table td {
  padding: 15px 20px;
  border-bottom: 1px solid #edf1f5;
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
}
.goal-table th:nth-child(1) {
  width: 24%;
}
.goal-table th:nth-child(2) {
  width: 26%;
}
.goal-table th:nth-child(3) {
  width: 20%;
}
.goal-table th:nth-child(4) {
  width: 30%;
}
.goal-table td:nth-child(2) {
  font-weight: 600;
  white-space: nowrap;
  color: #142d4e;
}
.goal-table td.incoming {
  color: #0d766e;
}
.journal-cell {
  font-size: 12px;
  color: #64748b;
}
.ledger-details {
  padding: 12px 24px;
}
.goal-settings-area {
  padding: 20px 24px;
  border-top: 1px solid #e7edf3;
}
.goal-constraint-summary {
  padding: 0 24px 20px;
  display: grid;
  gap: 14px;
}
.goal-constraint-summary > div {
  display: flex;
  gap: 16px;
  justify-content: space-between;
  font-size: 14px;
}
.goal-constraint-summary span {
  color: #64748b;
  white-space: nowrap;
}
.goal-constraint-summary b {
  font-weight: 500;
  text-align: right;
}
.life-goal-content {
  padding: 20px 24px;
  border-top: 1px solid #e7edf3;
}
.life-goal-content h3 {
  font-size: 15px;
  font-weight: 600;
  margin: 0 0 10px;
}
.life-goal-content > p {
  font-size: 14px;
  line-height: 1.8;
  color: #475569;
  margin: 0 0 14px;
}
.life-actions {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
  margin-bottom: 18px;
}
.parsed-summary {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  padding: 14px 0;
  font-size: 14px;
}
.parsed-summary span {
  color: #64748b;
}
.parsed-summary b {
  font-weight: 600;
}
.constraint-note {
  padding: 12px 14px;
  background: #f6f8fb;
  border-radius: 8px;
  font-size: 14px;
  line-height: 1.7;
  margin-bottom: 10px;
}
.goal-missing {
  color: #a16207 !important;
  background: #fffaf0;
  padding: 10px;
  border-radius: 6px;
}
.goal-principle-note {
  padding: 16px 24px;
  margin: 0;
  background: #f8fafc;
  font-size: 13px;
  line-height: 1.8;
  color: #64748b;
  border-top: 1px solid #e7edf3;
}
.no-life-goal {
  padding: 20px 24px;
  color: #64748b;
  font-size: 14px;
  line-height: 1.7;
  border-top: 1px solid #e7edf3;
}
.life-goal-query {
  padding: 0 24px 20px;
  display: flex;
  align-items: flex-end;
  gap: 16px;
  flex-wrap: wrap;
}
.life-goal-query > label {
  font-size: 14px;
  width: 320px;
  max-width: 100%;
  margin: 0;
}
.life-goal-query > div {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.life-goal-query button {
  border: 1px solid #e7edf3;
  background: #fff;
  border-radius: 6px;
  padding: 10px 12px;
  font-size: 13px;
  color: #475569;
  cursor: pointer;
}
.life-goal-query button.selected {
  color: #0d766e;
  background: #edf7f4;
  border-color: #d4e8e1;
}
.life-goal-editor {
  padding: 20px 24px;
  border-top: 1px solid #e7edf3;
}
.life-goal-editor h3 {
  font-size: 16px;
  margin: 0 0 16px;
}
.life-goal-editor form {
  max-width: 880px;
}
.goals-bank :deep(.empty-state) {
  min-height: 190px;
  padding: 24px;
}
.goals-bank :deep(.empty-state p),
.goals-bank :deep(.empty-state strong) {
  font-size: 14px;
}
.goals-bank :deep(.json-details pre) {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.goals-bank :deep(.status) {
  font-size: 12px;
  white-space: nowrap;
}
.goals-bank :deep(.goal-settings summary) {
  font-size: 14px;
  color: #0d766e;
}
.goals-bank :deep(.goal-settings .form-columns) {
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
}
.goals-bank button:focus-visible,
.goals-bank a:focus-visible {
  outline: 2px solid #0d9488;
  outline-offset: 3px;
}
.goals-bank input,
.goals-bank textarea {
  font-size: 14px !important;
}
.goals-bank .form-hint,
.goals-bank form small {
  font-size: 13px;
}
@media (max-width: 1100px) {
  .goals-bank {
    padding: 24px !important;
  }
  .goal-balance-main {
    padding: 28px;
  }
  .goal-balance-main > strong {
    font-size: 38px;
  }
  .goal-workspace {
    grid-template-columns: minmax(0, 1.4fr) minmax(260px, 1fr);
    gap: 20px;
  }
  .goal-transfer-form {
    flex-wrap: wrap;
  }
  .goal-transfer-form > label {
    flex-basis: 100% !important;
  }
  .goal-transfer-form :deep(.btn) {
    flex: 1;
  }
  .goal-table th,
  .goal-table td {
    padding: 12px 10px;
  }
  .goal-settings-area :deep(.goal-settings .form-columns) {
    grid-template-columns: 1fr;
  }
  .goal-progress-heading {
    flex-wrap: wrap;
    gap: 8px;
  }
}
@media (max-width: 750px) {
  .goals-bank {
    padding: 20px 16px !important;
  }
  .bank-heading {
    align-items: flex-start;
    gap: 16px;
  }
  .bank-heading h1 {
    font-size: 23px;
    line-height: 1.4;
  }
  .bank-heading p {
    line-height: 1.7;
  }
  .bank-heading :deep(.btn) {
    font-size: 13px;
    white-space: nowrap;
    padding: 9px 10px;
  }
  .goal-selection {
    align-items: flex-start;
    flex-direction: column;
    gap: 12px;
  }
  .goals-bank .goal-selection > label {
    width: 100%;
    justify-content: space-between;
  }
  .goal-selection select {
    max-width: 70%;
  }
  .goal-balance-hero,
  .goal-workspace {
    grid-template-columns: 1fr;
  }
  .goal-balance-main {
    padding: 24px;
  }
  .goal-balance-main > strong {
    font-size: 36px;
  }
  .goal-balance-context {
    padding: 20px 24px;
    gap: 18px;
  }
  .goal-balance-context > div {
    gap: 7px;
  }
  .goal-balance-context strong {
    font-size: 22px;
  }
  .bank-section-heading {
    padding: 20px;
    gap: 12px;
  }
  .bank-section-heading h2 {
    font-size: 16px;
  }
  .goal-transfer-form {
    padding-inline: 20px;
  }
  .goal-transfer-note {
    padding-inline: 20px;
  }
  .goal-table {
    min-width: 520px;
  }
  .life-goal-query,
  .life-goal-editor {
    padding-inline: 20px;
  }
  .life-goal-query > label {
    width: 100%;
  }
  .life-goal-editor .row-between {
    flex-wrap: wrap;
    gap: 12px;
  }
  .create-fund-panel > form {
    padding: 0 20px 20px;
  }
}
</style>
