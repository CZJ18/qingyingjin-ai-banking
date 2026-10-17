<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useAccounts } from '@/lib/queries'
import { useSession } from '@/lib/store'
import { agentApi, type AgentTask } from '@/api/agent-client'
import { sessionVersion } from '@/api/client'
import { money } from '@/lib/utils'
import { transferPreparation } from './task-view'
import Button from '@/components/ui/button/Button.vue'
defineProps<{ busy: boolean }>()
const emit = defineEmits<{ created: [task: AgentTask]; close: [] }>()
const s = useSession(),
  accounts = useAccounts(),
  source = ref(s.accountId),
  target = ref(''),
  amount = ref(''),
  description = ref(''),
  submitting = ref(false),
  error = ref('')
const checkingAccounts = computed(
  () =>
    accounts.data.value?.filter((a) => a.account_type === 'checking' && a.status === 'active') ||
    [],
)
watch(
  checkingAccounts,
  (rows) => {
    if (!source.value && rows.length) source.value = rows[0]!.id
  },
  { immediate: true },
)
let pending: { digest: string; key: string } | undefined
async function submit() {
  if (submitting.value || !s.connected) return
  const ownEpoch = sessionVersion()
  submitting.value = true
  error.value = ''
  try {
    if (
      ![source.value, target.value].every((id) => checkingAccounts.value.some((a) => a.id === id))
    )
      throw new Error('请选择当前用户的两个可用模拟账户')
    const body = transferPreparation(source.value, target.value, amount.value, description.value),
      digest = JSON.stringify(body)
    if (pending?.digest !== digest) pending = { digest, key: crypto.randomUUID() }
    const task = await agentApi.submit(body, pending!.key)
    if (ownEpoch !== sessionVersion()) return
    pending = undefined
    emit('created', task)
  } catch (e) {
    if (ownEpoch === sessionVersion()) error.value = (e as Error).message
  } finally {
    if (ownEpoch === sessionVersion()) submitting.value = false
  }
}
</script>
<template>
  <section class="coord-panel p6-transfer-form">
    <header class="coord-section">
      <div>
        <h2>准备模拟转账</h2>
        <p>先创建协同任务并确认准备范围；金融执行另行授权。</p>
      </div>
      <Button variant="outline" :disabled="busy || submitting" @click="$emit('close')">关闭</Button>
    </header>
    <p v-if="checkingAccounts.length < 2" class="result-note">
      需要两个当前用户的正常模拟活期账户。可在账户管理中创建账户后继续。
    </p>
    <form @submit.prevent="submit">
      <div class="form-grid">
        <label
          >付款账户<select
            aria-label="付款账户"
            v-model="source"
            required
            :disabled="busy || submitting"
          >
            <option value="">选择账户</option>
            <option v-for="a in checkingAccounts" :key="a.id" :value="a.id">
              {{ a.display_name }} · {{ money(a.balance_minor) }}
            </option>
          </select></label
        ><label
          >收款账户<select
            aria-label="收款账户"
            v-model="target"
            required
            :disabled="busy || submitting"
          >
            <option value="">选择账户</option>
            <option
              v-for="a in checkingAccounts.filter((a) => a.id !== source)"
              :key="a.id"
              :value="a.id"
            >
              {{ a.display_name }}
            </option>
          </select></label
        ><label
          >转账金额（元）<input
            v-model="amount"
            inputmode="decimal"
            pattern="[0-9]+(\.[0-9]{1,2})?"
            required
            placeholder="例如 10.00"
            :disabled="busy || submitting" /></label
        ><label
          >用途<input
            v-model="description"
            maxlength="200"
            placeholder="模拟转账用途"
            :disabled="busy || submitting"
        /></label>
      </div>
      <p v-if="error" class="form-error" role="alert">{{ error }}</p>
      <Button
        type="submit"
        :disabled="
          busy || submitting || !source || !target || !amount || checkingAccounts.length < 2
        "
        >{{ submitting ? '提交准备请求…' : '创建待确认准备任务' }}</Button
      >
    </form>
  </section>
</template>
