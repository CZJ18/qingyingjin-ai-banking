<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ShieldCheck } from 'lucide-vue-next'
import {
  monthlyConfirmationBinding, monthlyPlanCurrent, monthlyPlanView, monthlySuggestionRows,
  type MonthlyConfirmationBinding,
} from '@/lib/monthly-goal'
import { modeNames, record, roleNames } from './task-view'
import { date, money } from '@/lib/utils'
import Button from '@/components/ui/button/Button.vue'
import JsonDetails from '@/components/JsonDetails.vue'
import TaskResult from '@/components/assistant/TaskResult.vue'
const props = defineProps<{ task: unknown; busy: boolean; epoch: number }>()
const emit = defineEmits<{ confirm: [decision: 'approve' | 'reject', binding: MonthlyConfirmationBinding]; edit: [] }>()
const view = computed(() => monthlyPlanView(props.task))
const challenge = computed(() => record(record(props.task).confirmation))
const entries = computed(() => monthlySuggestionRows(props.task).map((row) => ({
  row, parameters: Object.keys(record(row.parameters.parameters)).length
    ? record(row.parameters.parameters) : row.parameters,
})))
const ack = ref(false), now = ref(Date.now())
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => { timer = setInterval(() => (now.value = Date.now()), 1000) })
onBeforeUnmount(() => clearInterval(timer))
watch([
  () => record(props.task).task_id,
  () => record(props.task).version,
  () => challenge.value.digest,
  () => challenge.value.nonce,
  () => props.epoch,
], () => (ack.value = false), { flush: 'sync' })
const valid = computed(() => monthlyPlanCurrent(props.task, now.value))
function decide(decision: 'approve' | 'reject') {
  const binding = monthlyConfirmationBinding(props.task, props.epoch)
  if (props.busy || !binding || !monthlyPlanCurrent(props.task, Date.now()) || (decision === 'approve' && !ack.value)) return
  emit('confirm', decision, binding)
}
</script>
<template>
  <section class="monthly-plan p6-preparation" aria-label="本月方案最终确认">
    <h3><ShieldCheck :size="18" />请核对本月完整方案</h3>
    <p>AI 已整理本次建议。确认方案表示接受这些建议；涉及转账、目标金存取或订阅取消时，仍需单独核对并授权。</p>
    <template v-if="view">
      <dl class="detail-grid">
        <dt>目标月份</dt><dd>{{ view.monthly_goal.period }} · 人民币 · 北京时间</dd>
        <dt>你的目标</dt><dd class="goal-text">{{ view.goal_text }}</dd>
        <dt>确认有效期</dt><dd>{{ date(Date.parse(String(challenge.expires_at))) }}</dd>
      </dl>
      <div class="monthly-suggestions">
        <article v-for="({ row, parameters }, index) in entries" :key="row.key" class="monthly-suggestion">
          <h4>{{ index + 1 }}. {{ roleNames[row.agent_type] || '任务建议' }} · {{ modeNames[row.execution_mode] || '待核对任务' }}</h4>
          <dl v-if="parameters.amount_minor !== undefined || parameters.account_id || parameters.source_account_id || parameters.target_account_id" class="detail-grid">
            <template v-if="parameters.amount_minor !== undefined"><dt>金额</dt><dd>{{ money(parameters.amount_minor as number) }}</dd></template>
            <template v-if="parameters.account_id"><dt>关联账户</dt><dd>{{ parameters.account_id }}</dd></template>
            <template v-if="parameters.source_account_id"><dt>付款账户</dt><dd>{{ parameters.source_account_id }}</dd></template>
            <template v-if="parameters.target_account_id"><dt>收款账户</dt><dd>{{ parameters.target_account_id }}</dd></template>
          </dl>
          <TaskResult v-if="Object.keys(row.output).length" :task="{ ...row, tool: roleNames[row.agent_type] || '任务建议' }" />
          <p v-else>该项尚未执行。接受方案后，如需准备金融操作，将继续单独核对。</p>
          <JsonDetails :value="row.parameters" label="核对完整参数、账户与约束" />
        </article>
      </div>
    </template>
    <p v-if="!valid" role="alert">该方案确认已失效或内容不完整，请重新查询任务状态。</p>
    <label class="checkbox-label"><input type="checkbox" v-model="ack" :disabled="busy || !valid" />我已核对本月目标与全部建议，理解金融操作仍需单独授权</label>
    <p>也可在下方输入“确认方案”或“拒绝方案”。修改目标会发起新任务，需要重新确认新方案。</p>
    <div class="p6-actions">
      <Button variant="outline" :disabled="busy || !valid" @click="decide('reject')">拒绝方案</Button>
      <Button :disabled="busy || !valid || !ack" @click="decide('approve')">确认方案</Button>
      <button type="button" class="monthly-edit" :disabled="busy" @click="$emit('edit')">修改本月目标</button>
    </div>
  </section>
</template>
<style scoped>
.monthly-plan { margin-bottom: 20px; }
.goal-text { white-space: pre-wrap; overflow-wrap: anywhere; }
.monthly-suggestions { display: grid; gap: 16px; margin: 20px 0; }
.monthly-suggestion { padding: 16px; border: 1px solid #dce8e3; border-radius: 10px; background: #fff; min-width: 0; }
.monthly-suggestion h4 { margin: 0 0 16px; font-size: 15px; }
.monthly-suggestion :deep(pre) { white-space: pre-wrap; overflow-wrap: anywhere; }
.monthly-suggestion :deep(.business-result) { padding: 16px !important; }
.monthly-edit { border: 0; padding: 8px; color: #0d766e; background: transparent; cursor: pointer; }
.monthly-edit:disabled { opacity: .5; cursor: default; }
</style>
