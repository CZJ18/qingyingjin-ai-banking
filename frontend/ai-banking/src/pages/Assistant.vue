<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'
import {
  Plus,
  Send,
  ReceiptText,
  Repeat2,
  Target,
  ChevronDown,
  PanelRight,
  X,
  ShieldCheck,
  History,
  ArrowRight,
  RotateCw,
  Play,
  Square,
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
import { useSession, useUI } from '@/lib/store'
import { useAccounts, period, currentMonth } from '@/lib/queries'
import { useJob } from '@/lib/job'
import { useAgentJob } from '@/lib/agent-job'
import { agentApi, type AgentResume } from '@/api/agent-client'
import { sessionVersion } from '@/api/client'
import { money } from '@/lib/utils'
import {
  agentRows,
  agentTrace,
  roleNames,
  taskControls,
  type TaskRow,
} from '@/components/agents/task-view'
import Button from '@/components/ui/button/Button.vue'
import Status from '@/components/Status.vue'
import TaskDetails from '@/components/assistant/TaskDetails.vue'
import TaskResult from '@/components/assistant/TaskResult.vue'
import PreparationConfirmation from '@/components/agents/PreparationConfirmation.vue'
import ExecutionDialog from '@/components/ExecutionDialog.vue'
import '@/components/agents/p6-workbench.css'
const s = useSession(),
  ui = useUI(),
  accounts = useAccounts()
const historyKind = ref<'langgraph' | 'legacy'>('langgraph')
const legacy = useJob(),
  agent = useAgentJob(
    () => s.selectedAgentTask,
    () => s.connected && historyKind.value === 'langgraph',
  )
const selectedId = computed(() =>
  historyKind.value === 'langgraph' ? s.selectedAgentTask : s.selectedJob,
)
const historyIds = computed(() => (historyKind.value === 'langgraph' ? s.agentTaskIds : s.jobIds))
const currentTask = computed(() =>
  historyKind.value === 'langgraph' ? agent.task.data.value : undefined,
)
const tasks = computed<TaskRow[]>(() =>
  historyKind.value === 'langgraph'
    ? agentRows(currentTask.value, agent.events.data.value)
    : legacy.job.data.value?.tasks || [],
)
const job = {
  data: computed(() =>
    historyKind.value === 'langgraph'
      ? currentTask.value
        ? {
            job: { id: currentTask.value.task_id, status: currentTask.value.status },
            tasks: tasks.value,
          }
        : undefined
      : legacy.job.data.value,
  ),
  isPending: computed(() =>
    historyKind.value === 'langgraph' ? agent.task.isPending.value : legacy.job.isPending.value,
  ),
  error: computed(() =>
    historyKind.value === 'langgraph' ? agent.task.error.value : legacy.job.error.value,
  ),
  refetch: () => (historyKind.value === 'langgraph' ? agent.task.refetch() : legacy.job.refetch()),
}
const trace = {
  data: computed(() =>
    historyKind.value === 'langgraph'
      ? agentTrace(agent.events.data.value)
      : legacy.trace.data.value,
  ),
  error: computed(() =>
    historyKind.value === 'langgraph' ? agent.events.error.value : legacy.trace.error.value,
  ),
  refetch: () =>
    historyKind.value === 'langgraph' ? agent.events.refetch() : legacy.trace.refetch(),
}
const connection = computed(() =>
  historyKind.value === 'langgraph' ? agent.connection.value : legacy.connection.value,
)
const input = ref(''),
  busy = ref(false),
  error = ref(''),
  historyOpen = ref(false),
  inspectorOpen = ref(true),
  mobileDetails = ref(false)
const instructions = ref<Record<string, string>>({}),
  taskInput = ref<HTMLTextAreaElement>()
const executionDialog = ref<InstanceType<typeof ExecutionDialog>>()
const pendingSubmission = ref<{ serialized: string; key: string }>()
const shortcuts = [
  { title: '分析我的近期账单', instruction: '分析我的近期账单。', icon: ReceiptText },
  {
    title: '检查我的订阅支出',
    instruction: '检查我的订阅支出，找出可能重复的代扣。',
    icon: Repeat2,
  },
  {
    title: '制定健康目标金计划',
    instruction: '结合正常生活开支，制定三个月健康目标金储蓄计划。',
    icon: Target,
  },
]
const account = computed(() => accounts.data.value?.find((a) => a.id === s.accountId))
const completed = computed(() => tasks.value.filter((t) => t.status === 'succeeded').length)
const instruction = computed(
  () =>
    currentTask.value?.request.instruction ||
    instructions.value[selectedId.value] ||
    (tasks.value.find((t) => typeof t.parameters?.instruction === 'string')?.parameters
      .instruction as string) ||
    '金融任务记录',
)
const confirmation = computed(
  () => currentTask.value?.status === 'waiting_confirmation' && !!currentTask.value.confirmation,
)
const failed = computed(
  () => job.data.value?.job.status === 'failed' || tasks.value.some((t) => t.status === 'failed'),
)
const resultTasks = computed(() => tasks.value.filter((t) => Object.keys(t.output).length))
const stepNames = roleNames
const controls = computed(() => taskControls(currentTask.value?.status || ''))
const stage = computed(
  () =>
    ({
      waiting_confirmation: '需要你确认准备范围',
      interrupted: '任务已中断',
      failed: '任务未完成',
      succeeded: '任务已完成',
      cancelled: '任务已取消',
      queued: '任务等待执行',
      running: '正在执行金融任务',
    })[job.data.value?.job.status || ''] || '任务状态',
)
watch(selectedId, () => {
  inspectorOpen.value = true
  mobileDetails.value = false
  historyOpen.value = false
})
function newTask() {
  historyKind.value = 'langgraph'
  s.selectedAgentTask = ''
  input.value = ''
  error.value = ''
  pendingSubmission.value = undefined
  nextTick(() => taskInput.value?.focus())
}
function selectHistory(id: string) {
  if (historyKind.value === 'langgraph') s.selectedAgentTask = id
  else s.selectedJob = id
  historyOpen.value = false
}
function retryTask() {
  void job.refetch()
  void trace.refetch()
}
function choose(instruction: string) {
  input.value = instruction
  nextTick(() => taskInput.value?.focus())
}
function duration(id: string) {
  if (historyKind.value === 'langgraph') {
    const ms = tasks.value.find((t) => t.task_id === id)?.duration_ms
    return ms === undefined ? '' : ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} 秒`
  }
  const records = legacy.trace.data.value?.executions.filter((e) => e.task_id === id) || [],
    ms = records.reduce((total, e) => total + (e.duration_ms || 0), 0)
  return records.length ? (ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} 秒`) : ''
}
async function runTask() {
  if (!currentTask.value || busy.value || !controls.value.run) return
  const ownEpoch = sessionVersion(),
    ownTask = currentTask.value.task_id
  busy.value = true
  error.value = ''
  try {
    await agent.run(currentTask.value.version)
  } catch (e) {
    if (ownEpoch === sessionVersion() && ownTask === selectedId.value)
      error.value = (e as Error).message + '；请重新查询状态再决定下一步。'
  } finally {
    if (ownEpoch === sessionVersion()) busy.value = false
  }
}
async function cancelTask() {
  if (!currentTask.value || busy.value || !controls.value.cancel) return
  const ownEpoch = sessionVersion(),
    ownTask = currentTask.value.task_id
  busy.value = true
  error.value = ''
  try {
    await agent.cancel(currentTask.value.version)
  } catch (e) {
    if (ownEpoch === sessionVersion() && ownTask === selectedId.value)
      error.value = (e as Error).message
  } finally {
    if (ownEpoch === sessionVersion()) busy.value = false
  }
}
async function confirm(decision: 'approve' | 'reject') {
  const current = currentTask.value,
    challenge = current?.confirmation
  if (!current || !challenge || busy.value) return
  const body: AgentResume = {
    expected_version: current.version,
    node_key: challenge.node_key,
    digest: challenge.digest,
    nonce: challenge.nonce,
    decision,
  }
  const ownEpoch = sessionVersion(),
    ownTask = current.task_id
  busy.value = true
  error.value = ''
  try {
    await agent.resume(body)
    if (ownEpoch !== sessionVersion() || ownTask !== selectedId.value) return
    ui.notify(decision === 'approve' ? '准备确认已处理，金融意向仍需单独授权' : '已拒绝准备请求')
  } catch (e) {
    if (ownEpoch === sessionVersion() && ownTask === selectedId.value)
      error.value = (e as Error).message + '；请查询最终任务状态。'
  } finally {
    if (ownEpoch === sessionVersion()) busy.value = false
  }
}
async function submit() {
  if (busy.value || !input.value.trim()) return
  if (!s.connected) {
    ui.notify('请先通过右上角登录')
    return
  }
  const ownEpoch = sessionVersion()
  let submittedId = ''
  busy.value = true
  error.value = ''
  try {
    const p = period(currentMonth()),
      body = {
        instruction: input.value.trim(),
        bindings: {
          from: p.from,
          to: p.to,
          ...(s.accountId ? { account_id: s.accountId } : {}),
          ...(s.planId ? { financial_plan_id: s.planId } : {}),
          ...(s.lifestyleGoalId ? { lifestyle_goal_id: s.lifestyleGoalId } : {}),
          ...(s.lifestylePlanId ? { lifestyle_plan_id: s.lifestylePlanId } : {}),
          ...(s.billingReportId ? { billing_report_id: s.billingReportId } : {}),
        },
        timeout_ms: 180000,
      }
    const serialized = JSON.stringify(body)
    if (pendingSubmission.value?.serialized !== serialized)
      pendingSubmission.value = { serialized, key: crypto.randomUUID() }
    const result = await agentApi.submit(body, pendingSubmission.value!.key)
    if (ownEpoch !== sessionVersion()) return
    submittedId = result.task_id
    instructions.value[result.task_id] = input.value.trim()
    historyKind.value = 'langgraph'
    s.saveAgentTask(result.task_id)
    input.value = ''
    pendingSubmission.value = undefined
    ui.notify('任务已提交，专业 Agent 正在查询真实银行数据')
    await agent.run(result.version)
  } catch (e) {
    if (ownEpoch === sessionVersion() && (!submittedId || submittedId === selectedId.value))
      error.value = (e as Error).message + '；已提交任务请先查询状态。'
  } finally {
    if (ownEpoch === sessionVersion()) busy.value = false
  }
}
</script>
<template>
  <div class="page assistant-page">
    <div class="workbench-heading">
      <div>
        <h1>智能金融任务工作台</h1>
        <p>账单、订阅与目标金，围绕你的账户开展任务。</p>
      </div>
      <Button variant="outline" :disabled="busy" @click="newTask"><Plus :size="16" />新任务</Button>
    </div>
    <div class="workbench-tools">
      <button
        class="history-button"
        :aria-expanded="historyOpen"
        aria-controls="task-history"
        @click="historyOpen = !historyOpen"
      >
        <History :size="16" />任务历史<span>{{ historyIds.length }}</span
        ><ChevronDown :size="14" :class="{ rotated: historyOpen }" /></button
      ><span v-if="account" class="workbench-account"
        >{{ account.display_name }}<b>{{ money(account.balance_minor) }}</b></span
      ><span v-else class="workbench-account">{{
        s.connected ? '已登录 · 可提交金融任务' : '登录后开始金融任务'
      }}</span>
    </div>
    <div v-if="historyOpen" id="task-history" class="workbench-history">
      <div class="p6-task-tabs">
        <button :class="{ active: historyKind === 'langgraph' }" @click="historyKind = 'langgraph'">
          当前协同任务</button
        ><button :class="{ active: historyKind === 'legacy' }" @click="historyKind = 'legacy'">
          历史任务
        </button>
      </div>
      <p v-if="!historyIds.length">
        当前账户暂无任务记录。仅保存任务编号，业务结果从服务端重新查询。
      </p>
      <button
        v-for="id in historyIds"
        :key="id"
        :class="{ selected: id === selectedId }"
        @click="selectHistory(id)"
      >
        <History :size="15" /><span
          >{{ instructions[id] || '金融协同任务' }}<small>{{ id }}</small></span
        ><ArrowRight :size="15" />
      </button>
    </div>
    <div class="p6-task-bindings">
      <span
        >分析区间 <b>{{ currentMonth() }}</b></span
      ><span
        >生活目标 <b>{{ s.lifestyleGoalId ? '已关联' : '待选择' }}</b></span
      ><span
        >生活方案 <b>{{ s.lifestylePlanId ? '已关联' : '待生成' }}</b></span
      ><span
        >资金计划 <b>{{ s.planId ? '已关联' : '待生成' }}</b></span
      ><RouterLink to="/goals">管理生活目标与约束</RouterLink>
    </div>
    <div class="financial-workbench" :class="{ 'with-details': selectedId && inspectorOpen }">
      <section class="workbench-main" aria-label="金融任务工作区">
        <div class="workbench-scroll">
          <div v-if="!selectedId" class="workbench-empty">
            <span class="workspace-eyebrow">账户服务 / 智能任务</span>
            <h2>你想处理哪项金融任务？</h2>
            <p>选择常用任务，或在下方描述具体需求。</p>
            <div class="financial-shortcuts">
              <button v-for="t in shortcuts" :key="t.title" @click="choose(t.instruction)">
                <component :is="t.icon" :size="19" /><span>{{ t.title }}</span
                ><ArrowRight :size="15" />
              </button>
            </div>
            <div class="task-guidance">
              <h3>任务如何执行</h3>
              <ol>
                <li>
                  <span>1</span>
                  <div>
                    <b>明确需求</b>
                    <p>结合当前账户与分析区间解析任务。</p>
                  </div>
                </li>
                <li>
                  <span>2</span>
                  <div>
                    <b>返回业务结果</b>
                    <p>查看分析摘要、资金建议与数据来源。</p>
                  </div>
                </li>
                <li>
                  <span>3</span>
                  <div>
                    <b>核对后授权</b>
                    <p>涉及金融操作时，单独核对并确认。</p>
                  </div>
                </li>
              </ol>
            </div>
          </div>
          <div v-else class="task-content">
            <div class="task-command">
              <span class="workspace-eyebrow">任务指令</span>
              <h2>{{ instruction }}</h2>
              <div>
                <Status v-if="job.data.value" :value="job.data.value.job.status" /><span
                  class="task-id"
                  >{{ selectedId.slice(0, 16) }}</span
                ><button
                  class="details-toggle desktop-details"
                  :aria-expanded="inspectorOpen"
                  @click="inspectorOpen = !inspectorOpen"
                >
                  <PanelRight :size="15" />{{ inspectorOpen ? '收起详情' : '执行详情' }}</button
                ><button class="details-toggle mobile-details" @click="mobileDetails = true">
                  <PanelRight :size="15" />任务详情
                </button>
              </div>
            </div>
            <div v-if="!s.connected" class="task-feedback">
              <h3>登录以恢复任务</h3>
              <p>通过右上角登录后，将重新查询服务端状态和业务结果。</p>
            </div>
            <div
              v-else-if="job.isPending.value && !job.error.value"
              class="task-loading"
              aria-busy="true"
              aria-label="正在加载金融任务"
            >
              <div class="skeleton" />
              <div class="skeleton" />
              <div class="skeleton" />
            </div>
            <div v-else-if="job.error.value" class="task-feedback error-feedback" role="alert">
              <h3>无法读取任务状态</h3>
              <p>{{ job.error.value.message }}</p>
              <Button variant="outline" @click="retryTask"><RotateCw :size="14" />重新查询</Button>
            </div>
            <template v-else-if="job.data.value">
              <PreparationConfirmation
                v-if="confirmation"
                :task="currentTask"
                :busy="busy"
                @confirm="confirm"
              />
              <div v-if="currentTask && (controls.run || controls.cancel)" class="p6-actions">
                <Button v-if="controls.run" variant="outline" :disabled="busy" @click="runTask"
                  ><Play :size="14" />{{
                    currentTask.status === 'queued' ? '开始任务' : '恢复任务'
                  }}</Button
                ><Button
                  v-if="controls.cancel"
                  variant="outline"
                  :disabled="busy"
                  @click="cancelTask"
                  ><Square :size="14" />取消任务</Button
                >
              </div>
              <p v-if="currentTask?.status === 'interrupted'" class="p6-outcome-note">
                任务已中断；可查询状态或恢复任务。已完成结果会由服务端检查点保留。
              </p>
              <p v-if="currentTask?.error_code" class="form-error" role="alert">
                {{ currentTask.error_code }}
              </p>
              <div v-if="failed" class="task-feedback error-feedback" role="alert">
                <h3>任务未完成</h3>
                <p>请核对服务端返回的错误和执行事件，再决定是否恢复任务。</p>
                <button class="details-toggle mobile-details" @click="mobileDetails = true">
                  查看失败详情
                </button>
              </div>
              <details class="execution-plan" :open="job.data.value.job.status !== 'succeeded'">
                <summary class="plan-summary">
                  <div class="plan-heading">
                    <h3>{{ stage }}</h3>
                    <span>{{ completed }} / {{ tasks.length }} 步已完成</span>
                  </div>
                  <div
                    v-if="tasks.length"
                    class="task-progress"
                    role="progressbar"
                    :aria-valuenow="completed"
                    :aria-valuemin="0"
                    :aria-valuemax="tasks.length"
                    aria-label="已完成步骤"
                  >
                    <span :style="{ width: `${(completed / tasks.length) * 100}%` }" />
                  </div>
                  <p v-if="!tasks.length" class="plan-note">等待服务端返回执行计划。</p>
                </summary>
                <ol>
                  <li v-for="(t, index) in tasks" :key="t.task_id">
                    <span class="step-number" :class="t.status">{{ index + 1 }}</span>
                    <div>
                      <b>{{ stepNames[t.agent_type] || t.agent_type }}</b
                      ><span>{{ t.tool }}</span>
                      <p v-if="t.error_code" class="step-error">{{ t.error_code }}</p>
                    </div>
                    <span v-if="duration(t.task_id)" class="step-duration">{{
                      duration(t.task_id)
                    }}</span
                    ><Status :value="t.status" />
                  </li>
                </ol>
                <div class="plan-footer">
                  <span class="connection-state" :class="{ online: connection === '实时连接' }"
                    ><i />{{ connection }}</span
                  ><RouterLink to="/agents">查看完整执行轨迹<ArrowRight :size="14" /></RouterLink>
                </div>
              </details>
              <div v-if="trace.error.value" class="task-feedback error-feedback" role="alert">
                <p>执行轨迹暂不可用：{{ trace.error.value.message }}</p>
                <Button variant="outline" @click="trace.refetch()">重试轨迹查询</Button>
              </div>
              <div v-if="resultTasks.length" class="business-results">
                <div class="results-heading">
                  <h3>业务结果</h3>
                  <span>来自本次服务端工具执行</span>
                </div>
                <TaskResult
                  v-for="t in resultTasks"
                  :key="t.task_id"
                  :task="t"
                  @inspect="executionDialog?.inspect($event)"
                />
              </div>
            </template>
          </div>
        </div>
        <form class="workbench-composer composer" @submit.prevent="submit">
          <label for="task-input">{{ selectedId ? '发起新的金融任务' : '描述你的金融需求' }}</label
          ><textarea
            id="task-input"
            maxlength="8192"
            aria-label="金融任务指令"
            ref="taskInput"
            v-model="input"
            placeholder="例如：分析近期消费，在保留必要生活开支的前提下制定储蓄计划。"
            rows="3"
            :disabled="busy"
            @keydown.ctrl.enter.prevent="submit"
            @keydown.meta.enter.prevent="submit"
          />
          <div class="workbench-composer-bottom">
            <span
              ><ShieldCheck :size="14" />金融操作需单独授权<span class="keyboard-hint">
                · Ctrl / ⌘ + Enter</span
              ></span
            ><Button type="submit" :disabled="busy || !input.trim()"
              ><Send :size="15" />{{ busy ? '提交中' : '发送' }}</Button
            >
          </div>
          <p v-if="error" class="form-error" role="alert">{{ error }}</p>
        </form>
      </section>
      <aside
        v-if="selectedId && inspectorOpen"
        class="workbench-inspector"
        aria-label="本次任务执行详情"
      >
        <header>
          <h2>执行详情</h2>
          <button class="icon-button" @click="inspectorOpen = false" aria-label="收起执行详情">
            <X :size="17" />
          </button>
        </header>
        <TaskDetails
          :tasks="tasks"
          :trace="trace.data.value"
          :job-id="selectedId"
          :connection="connection"
        />
      </aside>
    </div>
    <DialogRoot v-model:open="mobileDetails"
      ><DialogPortal
        ><DialogOverlay class="dialog-overlay" /><DialogContent class="assistant-detail-drawer"
          ><DialogTitle>任务执行详情</DialogTitle
          ><DialogDescription class="sr-only"
            >本次参与的 Agent、工具输入与返回，以及服务端执行轨迹。</DialogDescription
          ><TaskDetails
            :tasks="tasks"
            :trace="trace.data.value"
            :job-id="selectedId"
            :connection="connection" /><DialogClose class="dialog-x" aria-label="关闭任务详情"
            ><X :size="18" /></DialogClose></DialogContent></DialogPortal
    ></DialogRoot>
    <ExecutionDialog ref="executionDialog" banking />
  </div>
</template>
<style scoped>
.assistant-page {
  padding: 24px 28px !important;
  max-width: 1600px;
  margin: auto;
  font-size: 14px;
  color: #172033;
}
.workbench-heading {
  display: flex;
  gap: 16px;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20px;
}
.workbench-heading h1 {
  font-size: 26px;
  letter-spacing: -0.5px;
  font-weight: 650;
  margin: 0 0 8px;
}
.workbench-heading p {
  margin: 0;
  color: #64748b;
  font-size: 14px;
}
.workbench-tools {
  display: flex;
  gap: 16px;
  align-items: center;
  justify-content: space-between;
  padding: 0 0 16px;
}
.history-button,
.details-toggle {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  background: transparent;
  border: 0;
  padding: 6px 0;
  cursor: pointer;
  color: #475569;
  font-size: 14px;
}
.history-button > span {
  background: #e7edf3;
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 12px;
}
.history-button svg {
  transition: transform 0.18s;
}
.rotated {
  transform: rotate(180deg);
}
.workbench-account {
  display: flex;
  gap: 12px;
  align-items: center;
  font-size: 14px;
  color: #64748b;
}
.workbench-account b {
  color: #172033;
  font-size: 16px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.workbench-history {
  border: 1px solid #e7edf3;
  background: #fff;
  border-radius: 10px;
  margin-bottom: 16px;
  max-height: 220px;
  overflow: auto;
  padding: 8px;
}
.workbench-history > p {
  padding: 8px;
  color: #64748b;
  margin: 0;
  font-size: 14px;
}
.workbench-history button {
  display: flex;
  gap: 12px;
  align-items: center;
  padding: 12px;
  border: 0;
  background: transparent;
  width: 100%;
  text-align: left;
  color: #475569;
  border-radius: 6px;
  cursor: pointer;
}
.workbench-history button.selected,
.workbench-history button:hover {
  background: #f0f7f6;
  color: #0d766e;
}
.workbench-history button span {
  flex: 1;
  font-size: 14px;
  min-width: 0;
  overflow-wrap: anywhere;
}
.workbench-history small {
  display: block;
  font-size: 12px;
  margin-top: 5px;
}
.financial-workbench {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  height: calc(100svh - 300px);
  min-height: 500px;
  max-height: 1000px;
  background: #fff;
  border: 1px solid #e7edf3;
  border-radius: 12px;
  overflow: hidden;
}
.financial-workbench.with-details {
  grid-template-columns: minmax(0, 1fr) 300px;
}
.workbench-main {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}
.workbench-scroll {
  flex: 1;
  min-height: 0;
  overflow: auto;
  overscroll-behavior: contain;
}
.workbench-empty {
  width: 100%;
  max-width: 880px;
  margin: 0 auto;
  padding: 40px 40px 24px;
}
.workspace-eyebrow {
  font-size: 13px;
  color: #64748b;
  display: block;
  margin-bottom: 12px;
}
.workbench-empty h2 {
  font-size: 24px;
  letter-spacing: -0.5px;
  font-weight: 600;
  margin: 0 0 12px;
}
.workbench-empty > p {
  font-size: 15px;
  line-height: 1.7;
  color: #64748b;
  margin: 0 0 24px;
}
.financial-shortcuts {
  display: flex;
  flex-direction: column;
  border-top: 1px solid #e7edf3;
  border-bottom: 1px solid #e7edf3;
}
.financial-shortcuts button {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 17px 8px;
  background: transparent;
  border: 0;
  cursor: pointer;
  text-align: left;
  font-size: 15px;
  color: #142d4e;
  transition:
    background 0.18s,
    padding 0.18s;
}
.financial-shortcuts button + button {
  border-top: 1px solid #edf1f5;
}
.financial-shortcuts button span {
  flex: 1;
}
.financial-shortcuts button > svg:first-child {
  color: #0d9488;
}
.financial-shortcuts button:hover {
  background: #f3f8f8;
  padding-left: 14px;
}
.task-guidance {
  padding-top: 24px;
}
.task-guidance h3 {
  font-size: 14px;
  font-weight: 600;
  margin: 0 0 16px;
  color: #475569;
}
.task-guidance ol {
  display: flex;
  gap: 24px;
  list-style: none;
  padding: 0;
  margin: 0;
}
.task-guidance li {
  display: flex;
  gap: 10px;
  flex: 1;
  min-width: 0;
}
.task-guidance li > span {
  flex: none;
  font-size: 12px;
  width: 24px;
  height: 24px;
  display: grid;
  place-items: center;
  background: #f1f5f9;
  color: #64748b;
  border-radius: 50%;
}
.task-guidance b {
  font-size: 14px;
  font-weight: 500;
}
.task-guidance p {
  font-size: 14px;
  line-height: 1.7;
  color: #64748b;
  margin: 6px 0 0;
}
.task-content {
  max-width: 1000px;
  margin: 0 auto;
  padding: 28px;
  display: grid;
  gap: 20px;
}
.task-command h2 {
  font-size: 19px;
  line-height: 1.65;
  margin: 0 0 14px;
  font-weight: 600;
  overflow-wrap: anywhere;
}
.task-command > div {
  display: flex;
  gap: 12px;
  align-items: center;
  flex-wrap: wrap;
}
.task-id {
  font-size: 12px;
  color: #64748b;
  font-variant-numeric: tabular-nums;
}
.details-toggle {
  font-size: 13px;
  color: #0d766e;
}
.desktop-details {
  margin-left: auto;
}
.mobile-details {
  display: none;
}
.execution-plan {
  background: #f7f9fb;
  padding: 20px;
  border: 1px solid #e7edf3;
  border-radius: 10px;
}
.plan-heading {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  align-items: center;
}
.plan-heading h3 {
  font-size: 15px;
  font-weight: 600;
  margin: 0;
}
.plan-heading > span {
  font-size: 13px;
  color: #64748b;
}
.task-progress {
  height: 4px;
  background: #e2e8f0;
  border-radius: 4px;
  overflow: hidden;
  margin: 16px 0;
}
.task-progress span {
  display: block;
  height: 100%;
  background: #0d9488;
  transition: width 0.2s;
}
.plan-summary {
  list-style: none;
  cursor: pointer;
}
.plan-summary::-webkit-details-marker {
  display: none;
}
.plan-summary .plan-heading h3:after {
  content: '展开步骤';
  font-size: 12px;
  font-weight: 400;
  color: #64748b;
  margin-left: 12px;
}
.execution-plan[open] .plan-summary .plan-heading h3:after {
  content: '收起步骤';
}
.execution-plan:not([open]) .task-progress {
  margin-bottom: 0;
}
.execution-plan ol {
  list-style: none;
  margin: 0;
  padding: 0;
}
.execution-plan li {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 13px 0;
}
.execution-plan li + li {
  border-top: 1px solid #e7edf3;
}
.step-number {
  flex: none;
  width: 26px;
  height: 26px;
  background: #e7edf3;
  border-radius: 50%;
  display: grid;
  place-items: center;
  font-size: 12px;
  color: #64748b;
}
.step-number.succeeded {
  background: #dcf3ea;
  color: #047857;
}
.step-number.running {
  background: #d9edeb;
  color: #0d766e;
}
.step-number.failed {
  background: #fee2e2;
  color: #b91c1c;
}
.execution-plan li > div {
  flex: 1;
  min-width: 0;
  display: grid;
  gap: 5px;
}
.execution-plan li b {
  font-size: 14px;
  font-weight: 500;
}
.execution-plan li div > span {
  font-size: 12px;
  color: #64748b;
  overflow-wrap: anywhere;
}
.step-duration {
  font-size: 12px;
  color: #64748b;
  white-space: nowrap;
}
.step-error {
  font-size: 13px;
  margin: 0;
  color: #b91c1c;
  overflow-wrap: anywhere;
}
.plan-footer {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 10px;
  border-top: 1px solid #e7edf3;
  padding-top: 16px;
  margin-top: 8px;
}
.plan-footer > a {
  font-size: 13px;
  display: flex;
  align-items: center;
  gap: 6px;
  color: #0d766e;
}
.plan-note {
  font-size: 14px;
  color: #64748b;
  line-height: 1.7;
}
.results-heading {
  display: flex;
  gap: 8px;
  align-items: center;
  justify-content: space-between;
}
.results-heading h3 {
  font-size: 16px;
  margin: 0;
}
.results-heading span {
  color: #64748b;
  font-size: 13px;
}
.business-results {
  display: grid;
  gap: 16px;
}
.task-feedback {
  padding: 20px;
  border: 1px solid #e7edf3;
  background: #f7f9fb;
  border-radius: 10px;
}
.task-feedback h3 {
  font-size: 15px;
  margin: 0 0 8px;
  font-weight: 600;
}
.task-feedback p {
  font-size: 14px;
  line-height: 1.7;
  margin: 0 0 12px;
  overflow-wrap: anywhere;
}
.error-feedback {
  color: #991b1b;
  background: #fff7f7;
  border-color: #f4dcdc;
}
.confirmation-feedback {
  color: #854d0e;
  background: #fffaf0;
  border-color: #f2e4c9;
}
.task-loading {
  display: grid;
  gap: 16px;
  padding: 24px;
}
.task-loading .skeleton {
  height: 44px;
}
.task-loading .skeleton:last-child {
  height: 160px;
}
.workbench-composer.composer {
  flex: none;
  position: relative;
  margin: 0 !important;
  border: 0 !important;
  border-top: 1px solid #e7edf3 !important;
  border-radius: 0 !important;
  padding: 20px 28px !important;
  box-shadow: none !important;
  background: #fff;
}
.workbench-composer label {
  display: block;
  font-size: 14px;
  font-weight: 600;
  color: #172033;
  margin-bottom: 12px;
}
.workbench-composer textarea {
  display: block;
  resize: vertical;
  min-height: 80px;
  max-height: 180px;
  line-height: 1.7;
  width: 100%;
  font: inherit;
  font-size: 15px;
  border: 0 !important;
  background: #fff !important;
  box-shadow: none !important;
  padding: 0 !important;
  border-radius: 0 !important;
  color: #172033;
}
.workbench-composer textarea:focus {
  outline: 0 !important;
}
.workbench-composer:focus-within {
  box-shadow: inset 0 3px 0 #0d9488 !important;
}
.workbench-composer textarea::placeholder {
  color: #718096;
}
.workbench-composer-bottom {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: center;
  padding-top: 12px;
}
.workbench-composer-bottom > span {
  display: flex;
  align-items: center;
  gap: 5px;
  color: #64748b;
  font-size: 13px;
}
.workbench-composer .form-error {
  font-size: 14px;
  margin-bottom: 0;
}
.workbench-inspector {
  border-left: 1px solid #e7edf3;
  background: #fafbfd;
  overflow: auto;
  min-height: 0;
}
.workbench-inspector > header {
  padding: 18px 20px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  position: sticky;
  top: 0;
  background: #fafbfd;
  border-bottom: 1px solid #e7edf3;
  z-index: 1;
}
.workbench-inspector h2 {
  font-size: 15px;
  margin: 0;
  font-weight: 600;
}
.assistant-detail-drawer {
  position: fixed;
  right: 0;
  top: 0;
  bottom: 0;
  width: min(360px, 100%);
  background: #fff;
  overflow: auto;
  z-index: 60;
  box-shadow: -16px 0 48px #142d4e20;
}
.assistant-detail-drawer > h2 {
  font-size: 18px;
  margin: 0;
  padding: 24px 20px;
  border-bottom: 1px solid #e7edf3;
}
.assistant-page button:focus-visible,
.assistant-page a:focus-visible {
  outline: 2px solid #0d9488;
  outline-offset: 4px;
}
.assistant-page :deep(.status) {
  white-space: nowrap;
  font-size: 12px;
}
.assistant-page :deep(.json-details pre) {
  max-width: 100%;
  overflow-wrap: anywhere;
}
@media (max-width: 1100px) {
  .assistant-page {
    padding: 24px !important;
  }
  .task-content {
    padding: 20px;
  }
  .financial-workbench.with-details {
    grid-template-columns: minmax(0, 1fr) 280px;
  }
  .task-guidance ol {
    gap: 16px;
  }
  .workbench-empty {
    padding: 32px;
  }
  .keyboard-hint {
    display: none;
  }
}
@media (max-width: 900px) {
  .financial-workbench.with-details {
    grid-template-columns: minmax(0, 1fr);
  }
  .workbench-inspector,
  .desktop-details {
    display: none;
  }
  .mobile-details {
    display: inline-flex;
  }
  .workbench-empty {
    max-width: 800px;
  }
  .financial-workbench {
    height: calc(100svh - 300px);
    min-height: 500px;
  }
  .task-guidance ol {
    gap: 20px;
  }
}
@media (max-width: 600px) {
  .assistant-page {
    padding: 20px 16px !important;
  }
  .workbench-heading {
    align-items: flex-start;
    gap: 12px;
    margin-bottom: 16px;
  }
  .workbench-heading h1 {
    font-size: 21px;
    line-height: 1.4;
    margin-bottom: 8px;
  }
  .workbench-heading p {
    font-size: 14px;
    line-height: 1.65;
  }
  .workbench-heading :deep(.btn) {
    padding: 8px 10px;
    white-space: nowrap;
    font-size: 13px;
  }
  .workbench-tools {
    align-items: flex-start;
    gap: 12px;
  }
  .workbench-account {
    flex-direction: column;
    align-items: flex-end;
    gap: 4px;
    font-size: 12px;
    text-align: right;
    max-width: 56%;
  }
  .workbench-account b {
    font-size: 14px;
  }
  .financial-workbench {
    height: calc(100svh - 300px);
    min-height: 480px;
    border-radius: 10px;
  }
  .workbench-empty {
    padding: 24px 20px;
  }
  .workbench-empty h2 {
    font-size: 21px;
    line-height: 1.5;
  }
  .workbench-empty > p {
    font-size: 14px;
    margin-bottom: 20px;
  }
  .financial-shortcuts button {
    font-size: 14px;
    padding: 16px 0;
    gap: 10px;
  }
  .task-guidance {
    padding-top: 20px;
  }
  .task-guidance ol {
    flex-direction: column;
    gap: 16px;
  }
  .task-guidance p {
    margin-top: 4px;
  }
  .task-content {
    padding: 20px 16px;
    gap: 16px;
  }
  .task-command h2 {
    font-size: 17px;
  }
  .execution-plan {
    padding: 16px 12px;
  }
  .execution-plan li {
    gap: 8px;
    flex-wrap: wrap;
  }
  .execution-plan li > div {
    min-width: 100px;
  }
  .step-duration {
    display: none;
  }
  .plan-heading {
    align-items: flex-start;
  }
  .plan-heading h3 {
    font-size: 14px;
  }
  .plan-heading > span {
    white-space: nowrap;
    font-size: 12px;
  }
  .results-heading {
    align-items: flex-start;
    flex-direction: column;
  }
  .workbench-composer.composer {
    padding: 16px !important;
  }
  .workbench-composer textarea {
    font-size: 14px;
    min-height: 84px;
  }
  .workbench-composer-bottom > span {
    font-size: 12px;
  }
  .workbench-composer-bottom :deep(.btn) {
    padding: 10px 14px;
  }
  .task-feedback {
    padding: 16px;
  }
  .workspace-eyebrow {
    font-size: 12px;
  }
  .history-button {
    font-size: 13px;
  }
  .task-id {
    font-size: 11px;
  }
}
</style>
