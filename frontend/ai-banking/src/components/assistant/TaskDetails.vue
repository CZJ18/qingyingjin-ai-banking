<script setup lang="ts">
import { computed } from 'vue'
import { Clock3, ExternalLink } from 'lucide-vue-next'
import type { TaskRow, TaskTrace } from '@/components/agents/task-view'
import Status from '@/components/Status.vue'
import JsonDetails from '@/components/JsonDetails.vue'
import { date } from '@/lib/utils'
const props = defineProps<{
  tasks: TaskRow[]
  trace?: TaskTrace
  jobId: string
  connection: string
}>()
const names: Record<string, string> = {
  supervisor: '任务统筹',
  billing: '账单分析',
  subscription: '订阅管理',
  lifestyle: '生活目标',
  savings: '资金规划',
}
const agents = computed(() =>
  [...new Set(props.tasks.map((t) => t.agent_type))].map((name) => {
    const rows = props.tasks.filter((t) => t.agent_type === name)
    const status =
      [
        'failed',
        'blocked',
        'awaiting_confirmation',
        'waiting_confirmation',
        'pending_authorization',
        'pending_approval',
        'running',
        'queued',
        'pending',
        'paused',
        'cancelled',
      ].find((s) => rows.some((t) => t.status === s)) ||
      rows[0]?.status ||
      'queued'
    return { name, status, count: rows.length }
  }),
)
const duration = (ms?: number) =>
  ms == null ? '耗时尚未返回' : ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} 秒`
</script>
<template>
  <div class="detail-body">
    <div class="detail-section">
      <h3>本次参与 Agent</h3>
      <p v-if="!agents.length">执行计划尚未返回。</p>
      <div v-for="a in agents" :key="a.name" class="detail-agent">
        <div>
          <b>{{ names[a.name] || a.name }}</b
          ><span>{{ a.name }} · {{ a.count }} 个步骤</span>
        </div>
        <Status :value="a.status" />
      </div>
    </div>
    <div class="detail-section">
      <h3>
        工具执行记录 <span>{{ trace?.invocations?.length || 0 }}</span>
      </h3>
      <p v-if="!trace?.invocations?.length">专业节点返回后，可在下方核对工具、版本和数据来源。</p>
      <div v-for="call in trace?.invocations" :key="call.id" class="detail-call">
        <b>{{ call.tool }}</b>
        <div>
          <Status :value="call.status" /><span
            ><Clock3 :size="13" />{{ duration(call.duration_ms) }}</span
          >
        </div>
        <p v-if="call.error_code" class="detail-error">{{ call.error_code }}</p>
      </div>
    </div>
    <div class="detail-section">
      <h3>步骤输入与返回</h3>
      <details v-for="t in tasks" :key="t.task_id" class="detail-step">
        <summary>{{ t.tool }}<Status :value="t.status" /></summary>
        <p v-if="t.skill_id">{{ t.skill_id }} · v{{ t.skill_version ?? '尚未返回' }}</p>
        <JsonDetails
          v-if="Object.keys(t.parameters).length"
          :value="t.parameters"
          label="工具输入"
        /><JsonDetails :value="t.output" label="工具返回" />
        <p v-if="t.error_code" class="detail-error">{{ t.error_code }}</p>
      </details>
    </div>
    <div class="detail-section">
      <h3>执行轨迹</h3>
      <p v-if="!trace?.events?.length">尚无执行事件。</p>
      <div v-for="ev in trace?.events?.slice(-12)" :key="ev.id" class="detail-event">
        <i />
        <div>
          <b>{{ ev.event }}</b
          ><span>{{ date(ev.created_at) }} · {{ ev.status }}</span
          ><span v-if="ev.error_code" class="detail-error">{{ ev.error_code }}</span>
        </div>
      </div>
    </div>
    <div class="detail-footer">
      <span>{{ connection }}</span
      ><RouterLink to="/agents">完整协同记录<ExternalLink :size="14" /></RouterLink
      ><small>任务编号 {{ jobId }}</small>
    </div>
  </div>
</template>
<style scoped>
.detail-body {
  font-size: 14px;
  color: #172033;
  overflow-wrap: anywhere;
}
.detail-section {
  padding: 20px;
  border-bottom: 1px solid #e7edf3;
}
.detail-section h3 {
  font-size: 14px;
  margin: 0 0 16px;
  display: flex;
  justify-content: space-between;
}
.detail-section h3 span,
.detail-section p,
.detail-agent span,
.detail-event span {
  color: #64748b;
  font-size: 14px;
}
.detail-section p {
  line-height: 1.7;
  margin: 8px 0;
}
.detail-agent {
  display: flex;
  gap: 8px;
  align-items: center;
  justify-content: space-between;
  padding: 12px 0;
}
.detail-agent div {
  display: grid;
  gap: 6px;
}
.detail-agent b {
  font-size: 14px;
  font-weight: 600;
}
.detail-agent span {
  font-size: 12px;
}
.detail-call {
  padding: 12px 0;
  border-top: 1px solid #edf1f5;
}
.detail-call > b {
  font-size: 13px;
  font-weight: 500;
}
.detail-call > div {
  display: flex;
  justify-content: space-between;
  margin-top: 8px;
  align-items: center;
}
.detail-call div > span {
  display: flex;
  gap: 4px;
  align-items: center;
  font-size: 12px;
  color: #64748b;
}
.detail-step {
  padding: 12px 0;
  border-top: 1px solid #edf1f5;
}
.detail-step > summary {
  cursor: pointer;
  display: flex;
  gap: 8px;
  justify-content: space-between;
  font-size: 13px;
}
.detail-step :deep(pre) {
  max-width: 100%;
  white-space: pre-wrap;
}
.detail-event {
  display: flex;
  gap: 10px;
  margin: 14px 0;
}
.detail-event > i {
  flex: none;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #0d9488;
  margin-top: 6px;
}
.detail-event div {
  display: grid;
  gap: 5px;
}
.detail-event b {
  font-size: 13px;
  font-weight: 500;
}
.detail-event span {
  font-size: 12px;
}
.detail-footer {
  padding: 20px;
  display: grid;
  gap: 12px;
  color: #64748b;
  font-size: 13px;
}
.detail-footer a {
  display: flex;
  align-items: center;
  gap: 8px;
  color: #142d4e;
  font-weight: 600;
}
.detail-footer small {
  font-size: 12px;
}
.detail-error {
  color: #b91c1c !important;
}
</style>
