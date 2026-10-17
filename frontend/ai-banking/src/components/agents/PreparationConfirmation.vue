<script setup lang="ts">
import { computed, ref, watch, onMounted, onBeforeUnmount } from 'vue'
import { ShieldCheck } from 'lucide-vue-next'
import { record, preparationCurrent } from './task-view'
import { date, money } from '@/lib/utils'
import Button from '@/components/ui/button/Button.vue'
import JsonDetails from '@/components/JsonDetails.vue'
const props = defineProps<{ task: unknown; busy: boolean }>()
defineEmits<{ confirm: [decision: 'approve' | 'reject'] }>()
const task = computed(() => record(props.task)),
  challenge = computed(() => record(task.value.confirmation))
const spec = computed(() => record(challenge.value.parameters_view)),
  input = computed(() => record(spec.value.parameters))
const adviceOnly = computed(() => spec.value.execution_mode === 'user_confirmation')
const parameters = computed(() =>
  Object.keys(record(input.value.parameters)).length ? record(input.value.parameters) : input.value,
)
const ack = ref(false)
const now = ref(Date.now())
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  timer = setInterval(() => (now.value = Date.now()), 1000)
})
onBeforeUnmount(() => clearInterval(timer))
watch(
  () => `${task.value.task_id}:${task.value.version}:${challenge.value.digest}`,
  () => (ack.value = false),
)
const valid = computed(() => preparationCurrent(task.value, now.value))
const kind = computed(
  () =>
    ({
      transfer: '模拟银行转账',
      cancel_subscription: '取消模拟订阅',
      savings_deposit: '存入目标金',
      savings_withdrawal: '取出目标金',
    })[String(input.value.kind)] || String(spec.value.tool || '任务建议'),
)
</script>
<template>
  <section class="p6-preparation" aria-label="任务准备确认">
    <h3><ShieldCheck :size="18" />{{ adviceOnly ? '请确认任务建议' : '请确认准备范围' }}</h3>
    <p>
      {{
        adviceOnly
          ? '本次确认仅表示同意继续任务，不授予金融操作权限。'
          : '本次确认仅允许继续任务并生成操作意向。资金划转或订阅取消仍需在金融授权窗口单独核对并授权。'
      }}
    </p>
    <dl class="detail-grid">
      <dt>待准备操作</dt>
      <dd>{{ kind }}</dd>
      <template v-if="parameters.amount_minor !== undefined"
        ><dt>金额</dt>
        <dd>{{ money(parameters.amount_minor as number) }}</dd></template
      >
      <template v-if="parameters.source_account_id"
        ><dt>付款账户</dt>
        <dd>{{ parameters.source_account_id }}</dd></template
      >
      <template v-if="parameters.target_account_id"
        ><dt>收款账户</dt>
        <dd>{{ parameters.target_account_id }}</dd></template
      >
      <dt>确认有效期</dt>
      <dd>{{ date(Date.parse(String(challenge.expires_at))) }}</dd>
    </dl>
    <JsonDetails :value="spec" label="核对具体任务与操作参数" />
    <p v-if="!valid" role="alert">该确认已失效或不完整，请重新查询任务状态。</p>
    <label class="checkbox-label"
      ><input type="checkbox" v-model="ack" :disabled="busy || !valid" />{{
        adviceOnly
          ? '我已核对任务建议，理解金融执行仍需单独授权'
          : '我已核对准备范围，理解金融执行仍需单独授权'
      }}</label
    >
    <div class="p6-actions">
      <Button variant="outline" :disabled="busy || !valid" @click="$emit('confirm', 'reject')"
        >拒绝并结束任务</Button
      ><Button :disabled="busy || !valid || !ack" @click="$emit('confirm', 'approve')">{{
        adviceOnly ? '确认任务建议' : '确认准备意向'
      }}</Button>
    </div>
  </section>
</template>
