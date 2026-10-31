<script setup lang="ts">
import { computed } from 'vue'
import Button from '@/components/ui/button/Button.vue'
import { modelAdmissionState, type ModelAdmission } from '@/lib/model-admission'

const props = defineProps<{ snapshot: ModelAdmission | null; loading: boolean; error: string; reason?: string; completedTask?: unknown }>()
defineEmits<{ refresh: []; goal: [] }>()
const state = computed(() => modelAdmissionState(props.snapshot))
const simulation = computed(() => props.snapshot?.approval_limits.simulation === true)
const savedInterpretation = computed(() => {
  const task = props.completedTask as {status?:string;result?:{outputs?:Array<Record<string,unknown>>}} | null
  return task?.status === 'succeeded' && Array.isArray(task.result?.outputs) && task.result.outputs.some(output =>
    typeof output.workflow_execute_id === 'string' && /^(?:direct_provider:[A-Za-z0-9_-]{1,128}|[1-9][0-9]{0,19})$/.test(output.workflow_execute_id) &&
    typeof output.explanation === 'string' && !!output.explanation.trim() && Array.isArray(output.explanation_evidence_ids) && output.explanation_evidence_ids.length > 0)
})
const headline = computed(() => props.loading ? '正在检查 AI 服务' : state.value.available ? '可以开始分析' : savedInterpretation.value ? '已有模型解读可查看 · 新分析暂不可用' : props.reason==='MODEL_ADMISSION_NOT_CONFIGURED' ? '当前入口尚未启用AI分析' : 'AI 分析暂不可用')
const message = computed(() => props.loading ? '正在查询连接状态，不会发起模型调用。' : state.value.available ? '可输入分析需求；金融操作仍需另行核对和授权。' : simulation.value ? '模拟服务当前没有可用额度，仍可查看已有结果和账单。' : props.error || state.value.reason)
</script>

<template>
  <section class="model-admission panel" aria-label="AI 连接状态">
    <div class="admission-heading">
      <div role="status"><strong>{{ headline }}</strong><p>{{ message }}</p></div>
      <Button variant="outline" type="button" :disabled="loading" @click="$emit('refresh')">{{ loading ? '检查中…' : '重新检查' }}</Button>
    </div>
    <p v-if="simulation" class="admission-mode">当前为模拟模式 · 非真实模型</p>
    <p v-else-if="snapshot" class="admission-mode">真实模型连接 · {{ state.available ? '费用准入可用，提交时仍会校验任务范围' : '当前不能发起新分析' }}</p>
    <dl v-if="snapshot && !loading" class="admission-metrics" aria-label="当前可用模型额度">
      <div><dt>剩余模型请求</dt><dd>{{ snapshot.approval_window.remaining_requests }}<span> / {{ snapshot.approval_limits.max_requests }} 次</span></dd></div>
      <div><dt>剩余预算</dt><dd>{{ snapshot.approval_window.remaining_approval_budget }}<span> {{ snapshot.currency }}</span></dd></div>
    </dl>
    <p v-if="snapshot && !loading" class="admission-hint">一次分析可能包含规划、解读等多次模型请求。查询状态和查看已有结果不消耗模型次数。</p>
    <div v-if="!state.available" class="admission-links">
      <Button variant="outline" type="button" @click="$emit('goal')">试算节省目标</Button>
      <a href="#scenario-queries">查询账单、订阅或卡片 →</a>
      <RouterLink to="/billing">查看已有账单与统计 →</RouterLink>
      <p>以上业务入口不调用模型，不会把查询结果当成新的AI回答；你的原输入不会因此自动提交。</p>
    </div>
    <details class="admission-details">
      <summary>查看连接与费用详情</summary>
      <p v-if="simulation">此页面当前连接模拟服务。真实模型审批和额度不会自动切换到这里，需要完成真实接入并核对运行配置。</p>
      <p v-if="error || !state.available">{{ error || state.reason }}</p>
      <template v-if="snapshot">
        <p>单次最大风险 {{ snapshot.approval_window.next_request_maximum_cost }} {{ snapshot.currency }}；剩余预算 {{ snapshot.approval_window.remaining_approval_budget }} {{ snapshot.currency }}</p>
        <p>剩余请求 {{ snapshot.approval_window.remaining_requests }} / {{ snapshot.approval_limits.max_requests }}；并发空位 {{ snapshot.approval_window.remaining_concurrent_slots }} / {{ snapshot.approval_limits.max_concurrent }}</p>
      </template>
      <p>这是当前用户的只读额度快照。每次分析仍由服务端校验任务、账户范围和费用限额；检查状态不授予执行权限。</p>
      <p>费用审批只覆盖明确的任务与期限，不自动续费。已保存结果与新请求额度分别判断；reported、预占或估算都不代表正式结算。</p>
    </details>
  </section>
</template>

<style scoped>
.model-admission { margin-bottom: 20px; padding: 16px 20px; overflow-wrap: anywhere; }
.admission-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
.admission-heading strong { color: #142d4e; }
.admission-heading p { margin: 5px 0 0; color: #52657b; font-size: 14px; line-height: 1.6; }
.admission-heading :deep(button) { flex-shrink: 0; }
.admission-mode { margin: 8px 0; font-size: 13px; color: #52657b; }
.admission-links { font-size: 13px; display:flex; align-items:center; flex-wrap:wrap; gap:10px 16px; }
.admission-links p { flex-basis:100%; margin:0; color:#52657b; line-height:1.7; }
.admission-metrics { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; margin: 12px 0 6px; }
.admission-metrics > div { background: #f5f8fb; border-radius: 8px; padding: 10px 12px; }
.admission-metrics dt { font-size: 12px; color: #52657b; }
.admission-metrics dd { margin: 4px 0 0; font-weight: 600; font-variant-numeric: tabular-nums; color: #142d4e; overflow-wrap: anywhere; }
.admission-metrics dd span { font-size: 12px; font-weight: 400; color: #52657b; }
.admission-hint { color: #52657b; font-size: 12px; line-height: 1.6; margin: 8px 0 10px; }
.admission-details { margin-top: 10px; font-size: 13px; line-height: 1.7; color: #52657b; }
.admission-details summary { cursor: pointer; }
.admission-details p { margin: 8px 0; }
@media(max-width:640px) { .model-admission { padding: 12px 14px; margin-bottom: 12px; } .admission-heading p { font-size: 13px; } }
</style>
