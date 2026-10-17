<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { ArrowRight, Play, Square, RotateCw } from 'lucide-vue-next'
import { useSession, useUI } from '@/lib/store'
import { useJob } from '@/lib/job'
import { useAgentJob } from '@/lib/agent-job'
import { type AgentResume, type AgentTask } from '@/api/agent-client'
import { sessionVersion } from '@/api/client'
import { date } from '@/lib/utils'
import {
  agentRows,
  agentTrace,
  modeNames,
  record,
  roleNames,
  taskControls,
  type TaskRow,
} from '@/components/agents/task-view'
import Button from '@/components/ui/button/Button.vue'
import State from '@/components/State.vue'
import Status from '@/components/Status.vue'
import JsonDetails from '@/components/JsonDetails.vue'
import DependencyFlow from '@/components/agents/DependencyFlow.vue'
import TaskResult from '@/components/assistant/TaskResult.vue'
import PreparationConfirmation from '@/components/agents/PreparationConfirmation.vue'
import TransferPreparation from '@/components/agents/TransferPreparation.vue'
import ExecutionDialog from '@/components/ExecutionDialog.vue'
import '@/components/agents/p6-workbench.css'
const s = useSession(),
  ui = useUI(),
  legacy = useJob()
const historyKind = ref<'langgraph' | 'legacy'>('langgraph')
const agent = useAgentJob(
  () => s.selectedAgentTask,
  () => s.connected && historyKind.value === 'langgraph',
)
const lookup = ref(s.selectedAgentTask || s.selectedJob),
  selected = ref(''),
  busy = ref(false),
  error = ref('')
const executionDialog = ref<InstanceType<typeof ExecutionDialog>>()
const transferOpen = ref(false)
const taskId = computed(() =>
  historyKind.value === 'langgraph' ? s.selectedAgentTask : s.selectedJob,
)
const current = computed(() =>
  historyKind.value === 'langgraph' ? agent.task.data.value : undefined,
)
const rows = computed<TaskRow[]>(() =>
  historyKind.value === 'langgraph'
    ? agentRows(current.value, agent.events.data.value)
    : legacy.job.data.value?.tasks || [],
)
const status = computed(() => current.value?.status || legacy.job.data.value?.job.status || '')
const viewReady = computed(() =>
  historyKind.value === 'langgraph' ? !!current.value : !!legacy.job.data.value,
)
const loading = computed(() =>
  historyKind.value === 'langgraph' ? agent.task.isLoading.value : legacy.job.isLoading.value,
)
const queryError = computed(() =>
  historyKind.value === 'langgraph' ? agent.task.error.value : legacy.job.error.value,
)
const connection = computed(() =>
  historyKind.value === 'langgraph' ? agent.connection.value : legacy.connection.value,
)
const version = computed(() => current.value?.version || legacy.job.data.value?.job.version)
const createdAt = computed(() =>
  current.value ? Date.parse(current.value.created_at) : legacy.job.data.value?.job.created_at,
)
const task = computed(() => rows.value.find((t) => t.task_id === selected.value))
const completed = computed(() => rows.value.filter((t) => t.status === 'succeeded'))
const resultTask = computed(
  () => completed.value.find((t) => t.task_id === selected.value) || completed.value[0],
)
const evidence = computed(() =>
  Array.isArray(task.value?.output.evidence) ? task.value!.output.evidence.map(record) : [],
)
const events = computed(() =>
  historyKind.value === 'langgraph'
    ? agentTrace(agent.events.data.value).events || []
    : legacy.trace.data.value?.events || [],
)
const traceLoading = computed(() =>
  historyKind.value === 'langgraph' ? agent.events.isLoading.value : legacy.trace.isLoading.value,
)
const traceError = computed(() =>
  historyKind.value === 'langgraph' ? agent.events.error.value : legacy.trace.error.value,
)
const controls = computed(() => taskControls(current.value?.status || ''))
const dependencies = computed(() =>
  historyKind.value === 'langgraph'
    ? rows.value.flatMap((t) =>
        (t.dependencies || []).map((key) => ({
          id: `${t.task_id}:${key}`,
          task_id: t.task_id,
          depends_on: `${taskId.value}.${key}`,
        })),
      )
    : legacy.job.data.value?.dependencies || [],
)
const dependenciesKnown = computed(
  () => historyKind.value === 'legacy' || !!current.value?.request.tasks?.length,
)
const workflowId = computed(() => task.value?.output.workflow_execute_id)
watch(
  rows,
  (values) => {
    if (values.length && !values.some((t) => t.task_id === selected.value))
      selected.value = values[0]!.task_id
  },
  { immediate: true },
)
function lookupTask() {
  const id = lookup.value.trim()
  if (/^lg-[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(id)) {
    historyKind.value = 'langgraph'
    s.saveAgentTask(id)
  } else if (/^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(id)) {
    historyKind.value = 'legacy'
    s.saveJob(id)
  } else error.value = '请输入完整的任务编号'
}
function query() {
  return historyKind.value === 'langgraph' ? agent.task.refetch() : legacy.job.refetch()
}
function queryEvents() {
  return historyKind.value === 'langgraph' ? agent.events.refetch() : legacy.trace.refetch()
}
async function control(action: 'run' | 'cancel') {
  if (!current.value || busy.value) return
  const ownEpoch = sessionVersion(),
    ownTask = current.value.task_id
  busy.value = true
  error.value = ''
  try {
    if (action === 'run') await agent.run(current.value.version)
    else await agent.cancel(current.value.version)
    if (ownEpoch !== sessionVersion() || ownTask !== taskId.value) return
    ui.notify('任务控制请求已由服务器处理')
  } catch (e) {
    if (ownEpoch === sessionVersion() && ownTask === taskId.value)
      error.value = (e as Error).message + '；请查询最终状态。'
  } finally {
    if (ownEpoch === sessionVersion()) busy.value = false
  }
}
async function confirm(decision: 'approve' | 'reject') {
  const value = current.value,
    challenge = value?.confirmation
  if (!value || !challenge || busy.value) return
  const body: AgentResume = {
    expected_version: value.version,
    node_key: challenge.node_key,
    digest: challenge.digest,
    nonce: challenge.nonce,
    decision,
  }
  const ownEpoch = sessionVersion(),
    ownTask = value.task_id
  busy.value = true
  error.value = ''
  try {
    await agent.resume(body)
    if (ownEpoch !== sessionVersion() || ownTask !== taskId.value) return
    ui.notify(decision === 'approve' ? '准备确认已处理，资金执行需另行授权' : '已拒绝准备请求')
  } catch (e) {
    if (ownEpoch === sessionVersion() && ownTask === taskId.value)
      error.value = (e as Error).message + '；请查询最终状态。'
  } finally {
    if (ownEpoch === sessionVersion()) busy.value = false
  }
}
async function startPreparation(task: AgentTask) {
  const ownEpoch = sessionVersion()
  s.saveAgentTask(task.task_id)
  historyKind.value = 'langgraph'
  lookup.value = task.task_id
  transferOpen.value = false
  busy.value = true
  error.value = ''
  try {
    await agent.run(task.version)
  } catch (e) {
    if (ownEpoch === sessionVersion() && task.task_id === taskId.value)
      error.value = (e as Error).message + '；请查询最终任务状态。'
  } finally {
    if (ownEpoch === sessionVersion()) busy.value = false
  }
}
</script>
<template>
  <div class="page coordination-workbench">
    <header class="coord-heading">
      <div>
        <h1>多智能体协同中心</h1>
        <p>围绕生活目标，查看专业分析、任务依赖和执行证据。</p>
      </div>
      <div class="p6-actions">
        <Button
          variant="outline"
          :disabled="!s.connected || busy"
          @click="transferOpen = !transferOpen"
          >准备模拟转账</Button
        ><Button @click="$router.push('/assistant')">创建协同任务<ArrowRight :size="15" /></Button>
      </div>
    </header>
    <TransferPreparation
      v-if="transferOpen"
      :busy="busy"
      @created="startPreparation"
      @close="transferOpen = false"
    />
    <div class="p6-task-tabs">
      <button :class="{ active: historyKind === 'langgraph' }" @click="historyKind = 'langgraph'">
        当前协同任务</button
      ><button :class="{ active: historyKind === 'legacy' }" @click="historyKind = 'legacy'">
        历史任务
      </button>
    </div>
    <form class="task-lookup" @submit.prevent="lookupTask">
      <label
        >任务编号<input
          v-model="lookup"
          placeholder="lg-任务编号 或历史 UUID"
          required
          pattern="(lg-)?[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}" /></label
      ><Button type="submit" variant="outline" :disabled="!s.connected">查询任务</Button
      ><span>{{ connection }}</span>
    </form>
    <p v-if="error" class="form-error" role="alert">{{ error }}</p>
    <State
      :connected="s.connected"
      :loading="loading"
      :error="queryError"
      :empty="!taskId"
      title="尚未选择协同任务"
      description="创建任务，或查询已有编号查看实际执行轨迹。"
      @retry="query"
    >
      <template v-if="viewReady">
        <section class="job-summary">
          <div class="job-identity">
            <span>{{ historyKind === 'langgraph' ? '当前协同任务' : '历史协同任务' }}</span>
            <h2>{{ taskId }}</h2>
            <Status :value="status" />
          </div>
          <div class="job-facts">
            <div>
              <span>已返回专业节点</span><b>{{ completed.length }} / {{ rows.length }} 已完成</b>
            </div>
            <div>
              <span>创建时间</span><b>{{ date(createdAt) }}</b>
            </div>
            <div>
              <span>任务版本</span><b>v{{ version }}</b>
            </div>
            <div><span>执行范围</span><b>分析、建议与操作准备</b></div>
          </div>
          <div v-if="current" class="job-controls">
            <Button v-if="controls.run" variant="outline" :disabled="busy" @click="control('run')"
              ><Play :size="14" />{{
                current.status === 'queued' ? '开始任务' : '恢复任务'
              }}</Button
            ><Button
              v-if="controls.cancel"
              variant="outline"
              :disabled="busy"
              @click="control('cancel')"
              ><Square :size="14" />取消任务</Button
            ><Button variant="outline" @click="query"><RotateCw :size="14" />查询状态</Button>
          </div>
          <p v-if="current?.error_code" class="form-error" role="alert">{{ current.error_code }}</p>
          <p v-if="current?.status === 'interrupted'" class="p6-outcome-note">
            任务已中断，可查询状态或恢复。已完成的操作不会因重放而重新执行。
          </p>
        </section>
        <PreparationConfirmation
          v-if="current?.status === 'waiting_confirmation' && current.confirmation"
          :task="current"
          :busy="busy"
          @confirm="confirm"
        />
        <div class="coordination-layout">
          <main class="coord-main">
            <DependencyFlow
              :tasks="rows"
              :dependencies="dependencies"
              :selected="selected"
              strategy="parallel_read"
              :dependencies-known="dependenciesKnown"
              @select="selected = $event"
            />
            <section class="coord-panel">
              <header class="coord-section">
                <div>
                  <h2>任务执行时间线</h2>
                  <p>查看服务器记录的状态、时间与关联追踪编号。</p>
                </div>
                <span>{{ events.length }} 条事件</span>
              </header>
              <State
                :connected="s.connected"
                :loading="traceLoading"
                :error="traceError"
                :empty="!events.length"
                title="尚无执行事件"
                @retry="queryEvents"
                ><ol class="execution-timeline">
                  <li v-for="ev in events" :key="ev.id">
                    <time>{{ date(ev.created_at) }}</time
                    ><i />
                    <div>
                      <div>
                        <b>{{ ev.event }}</b
                        ><Status :value="ev.status" />
                      </div>
                      <button
                        v-if="ev.task_id"
                        @click="
                          selected =
                            rows.find((t) => t.key === ev.task_id || t.task_id === ev.task_id)
                              ?.task_id || selected
                        "
                      >
                        {{ ev.task_id }}
                      </button>
                      <p v-if="ev.error_code" class="form-error">{{ ev.error_code }}</p>
                      <small v-if="ev.trace_id">Trace {{ ev.trace_id }}</small>
                    </div>
                  </li>
                </ol></State
              >
            </section>
          </main>
          <aside class="coord-panel node-inspector">
            <header class="coord-section">
              <h2>执行详情</h2>
              <span>选中专业节点</span>
            </header>
            <template v-if="task"
              ><div class="node-overview">
                <div>
                  <h3>{{ roleNames[task.agent_type] || task.agent_type || '等待专业节点' }}</h3>
                  <Status :value="task.status" />
                </div>
                <p>{{ task.key }}</p>
                <dl>
                  <div>
                    <dt>调用工具</dt>
                    <dd>{{ task.tool || '尚未返回' }}</dd>
                  </div>
                  <div>
                    <dt>Skill 版本</dt>
                    <dd>
                      {{ task.skill_id || '尚未返回'
                      }}<template v-if="task.skill_version"> · v{{ task.skill_version }}</template>
                    </dd>
                  </div>
                  <div>
                    <dt>执行范围</dt>
                    <dd>{{ modeNames[task.execution_mode] || '以任务返回为准' }}</dd>
                  </div>
                  <div>
                    <dt>失败原因</dt>
                    <dd>{{ task.error_code || '未返回错误' }}</dd>
                  </div>
                </dl>
                <JsonDetails
                  v-if="Object.keys(task.parameters).length"
                  :value="task.parameters"
                  label="真实工具输入"
                /><JsonDetails :value="task.output" label="真实工具返回结果" />
              </div>
              <div class="node-trace">
                <h3>调用来源</h3>
                <p v-if="workflowId">Coze 工作流执行 #{{ workflowId }}</p>
                <p v-else-if="Object.keys(task.output).length">
                  专业节点通过 Banking Tool 返回业务服务结果。
                </p>
                <p v-else class="inspector-empty">等待本节点返回实际调用结果。</p>
                <h3>数据来源</h3>
                <p v-if="!evidence.length" class="inspector-empty">本节点尚未返回证据引用。</p>
                <div v-for="(e, i) in evidence" :key="i" class="evidence-row">
                  <span>{{ e.type }} #{{ e.id }}</span
                  ><small v-if="e.audit_id">审计 #{{ e.audit_id }}</small>
                </div>
              </div></template
            >
            <div v-else class="inspector-empty">
              点击专业节点查看执行详情。任务理解期间可先查看事件状态。
            </div>
          </aside>
        </div>
        <section v-if="completed.length" class="coord-panel final-results">
          <header class="coord-section">
            <div>
              <h2>{{ status === 'succeeded' ? '最终任务结果' : '已完成节点结果' }}</h2>
              <p>金融金额来自业务服务计算；涉及金融操作时仍需单独授权。</p>
            </div>
            <RouterLink to="/security" class="text-link"
              >金融授权中心<ArrowRight :size="14"
            /></RouterLink>
          </header>
          <div class="result-switcher" aria-label="选择专业任务结果">
            <button
              v-for="t in completed"
              :key="t.task_id"
              :class="{ active: resultTask?.task_id === t.task_id }"
              :aria-pressed="resultTask?.task_id === t.task_id"
              @click="selected = t.task_id"
            >
              {{ roleNames[t.agent_type] || t.agent_type }} · {{ t.key }}
            </button>
          </div>
          <div v-if="resultTask" class="results-list">
            <div><TaskResult :task="resultTask" @inspect="executionDialog?.inspect($event)" /></div>
          </div>
        </section>
      </template>
    </State>
    <ExecutionDialog ref="executionDialog" banking />
  </div>
</template>
<style scoped>
.coordination-workbench {
  display: flex;
  flex-direction: column;
  gap: 24px;
  color: #172033;
}
.coord-heading {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
}
.coord-heading h1 {
  font-size: 28px;
  margin: 0;
  letter-spacing: -0.5px;
}
.coord-heading p {
  font-size: 14px;
  color: #64748b;
  margin: 8px 0 0;
}
.task-lookup {
  display: flex;
  align-items: end;
  gap: 12px;
}
.task-lookup label {
  flex: 1;
  max-width: 540px;
  font-size: 14px;
  margin: 0;
}
.task-lookup > span {
  font-size: 13px;
  color: #64748b;
  align-self: center;
}
.job-summary {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 24px;
  border: 1px solid #e7edf3;
  background: #fff;
  padding: 24px;
  border-radius: 12px;
}
.job-identity {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.job-identity > span {
  font-size: 13px;
  color: #64748b;
}
.job-identity h2 {
  font-size: 16px;
  font-weight: 600;
  margin: 0;
  overflow-wrap: anywhere;
}
.job-facts {
  grid-row: 2;
  display: flex;
  gap: 32px;
  flex-wrap: wrap;
}
.job-facts > div {
  display: grid;
  gap: 8px;
}
.job-facts span {
  font-size: 13px;
  color: #64748b;
}
.job-facts b {
  font-size: 14px;
  font-weight: 500;
}
.job-controls {
  grid-column: 2;
  grid-row: 1/3;
  display: flex;
  gap: 8px;
  align-items: center;
}
.coordination-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 344px;
  gap: 24px;
  align-items: start;
}
.coord-main {
  min-width: 0;
  display: grid;
  gap: 24px;
}
.coord-panel {
  border: 1px solid #e7edf3;
  border-radius: 12px;
  background: #fff;
  overflow: hidden;
  min-width: 0;
}
.coord-section {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 22px 24px;
  border-bottom: 1px solid #e7edf3;
}
.coord-section h2 {
  font-size: 17px;
  margin: 0;
}
.coord-section p {
  font-size: 14px;
  color: #64748b;
  margin: 8px 0 0;
}
.coord-section > span {
  font-size: 13px;
  color: #64748b;
}
.execution-timeline {
  list-style: none;
  margin: 0;
  padding: 12px 24px 24px;
  max-height: 530px;
  overflow: auto;
}
.execution-timeline li {
  display: grid;
  grid-template-columns: 90px 12px minmax(0, 1fr);
  gap: 12px;
  padding: 16px 0;
  position: relative;
}
.execution-timeline time {
  font-size: 13px;
  color: #64748b;
  margin-top: 3px;
}
.execution-timeline i {
  width: 7px;
  height: 7px;
  background: #0d9488;
  border-radius: 50%;
  margin-top: 7px;
}
.execution-timeline li > div > div {
  display: flex;
  gap: 12px;
  align-items: center;
  flex-wrap: wrap;
}
.execution-timeline b {
  font-size: 14px;
  font-weight: 500;
  overflow-wrap: anywhere;
}
.execution-timeline button {
  padding: 0;
  border: 0;
  background: none;
  color: #0d766e;
  font-size: 13px;
  text-align: left;
  margin: 8px 0;
  cursor: pointer;
  overflow-wrap: anywhere;
}
.execution-timeline small {
  display: block;
  font-size: 12px;
  color: #64748b;
  overflow-wrap: anywhere;
}
.node-overview,
.node-trace {
  padding: 20px 24px;
}
.node-overview > div {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.node-inspector h3 {
  font-size: 15px;
  margin: 0 0 12px;
  font-weight: 600;
  text-transform: capitalize;
}
.node-overview h3 {
  margin: 0;
}
.node-overview > p {
  font-size: 14px;
  color: #64748b;
  overflow-wrap: anywhere;
}
.node-overview dl {
  margin: 20px 0;
}
.node-overview dl > div {
  display: grid;
  grid-template-columns: 80px minmax(0, 1fr);
  gap: 10px;
  font-size: 14px;
  line-height: 1.6;
  margin: 12px 0;
}
.node-overview dt {
  color: #64748b;
}
.node-overview dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.node-trace {
  border-top: 1px solid #e7edf3;
}
.tool-record {
  padding: 14px 0;
  border-bottom: 1px solid #e7edf3;
}
.tool-record > div {
  display: flex;
  gap: 8px;
  align-items: center;
  justify-content: space-between;
}
.tool-record b {
  font-size: 14px;
  font-weight: 500;
  overflow-wrap: anywhere;
}
.tool-record p {
  font-size: 13px;
  line-height: 1.7;
  color: #475569;
  overflow-wrap: anywhere;
  margin: 8px 0;
}
.tool-record small,
.evidence-row small {
  font-size: 12px;
  color: #64748b;
  overflow-wrap: anywhere;
}
.node-trace > h3 {
  margin-top: 20px;
}
.inspector-empty {
  font-size: 14px;
  color: #64748b;
  line-height: 1.8;
  padding: 12px 0;
}
.evidence-row {
  display: grid;
  gap: 6px;
  font-size: 14px;
  padding: 12px 0;
  overflow-wrap: anywhere;
}
.node-confirmation {
  padding: 20px 24px;
  background: #fffcf4;
  border-top: 1px solid #eadbbb;
}
.node-confirmation p {
  font-size: 14px;
  line-height: 1.8;
}
.node-confirmation > div {
  display: flex;
  gap: 8px;
  margin-top: 16px;
}
.result-switcher {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding: 20px 24px 0;
}
.result-switcher button {
  padding: 9px 12px;
  border: 1px solid #e7edf3;
  border-radius: 7px;
  background: #f8fafc;
  color: #475569;
  font-size: 14px;
  cursor: pointer;
  transition: background 0.18s;
}
.result-switcher button.active {
  background: #edf6f3;
  border-color: #9bc8bf;
  color: #0d766e;
}
.result-switcher button:focus-visible {
  outline: 2px solid #0d9488;
  outline-offset: 2px;
}
.results-list {
  padding: 24px;
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 24px;
}
.results-list h3 {
  font-size: 15px;
  margin: 0 0 16px;
}
.results-list > div {
  min-width: 0;
}
.coordination-workbench :deep(pre) {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.final-results :deep(.result-card) {
  box-shadow: none;
}
.final-results :deep(.result-card h3) {
  font-size: 15px;
}
@media (max-width: 1200px) {
  .coordination-layout {
    grid-template-columns: minmax(0, 1fr) 310px;
    gap: 20px;
  }
  .job-controls {
    grid-column: 1/-1;
    grid-row: 3;
  }
  .coord-section,
  .node-overview,
  .node-trace {
    padding: 20px;
  }
  .job-facts {
    gap: 24px;
  }
}
@media (max-width: 900px) {
  .coordination-layout {
    grid-template-columns: 1fr;
  }
  .results-list {
    grid-template-columns: 1fr;
  }
  .node-inspector {
    width: 100%;
  }
}
@media (max-width: 600px) {
  .coordination-workbench {
    gap: 20px;
  }
  .coord-heading {
    flex-direction: column;
    align-items: flex-start;
  }
  .coord-heading h1 {
    font-size: 25px;
  }
  .task-lookup {
    flex-wrap: wrap;
  }
  .task-lookup label {
    flex: 1 1 100%;
    max-width: none;
  }
  .job-summary {
    padding: 20px;
    gap: 20px;
  }
  .job-facts {
    gap: 20px;
  }
  .job-identity h2 {
    font-size: 14px;
  }
  .execution-timeline {
    padding: 12px 16px 20px;
  }
  .execution-timeline li {
    grid-template-columns: 74px 8px minmax(0, 1fr);
    gap: 8px;
  }
  .execution-timeline time {
    font-size: 12px;
  }
  .coord-section {
    padding: 20px 16px;
  }
  .results-list {
    padding: 20px 16px;
  }
  .node-confirmation {
    padding: 20px;
  }
  .job-controls {
    flex-wrap: wrap;
  }
}
</style>
