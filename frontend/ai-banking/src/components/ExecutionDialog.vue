<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import {
  DialogRoot,
  DialogPortal,
  DialogOverlay,
  DialogContent,
  DialogTitle,
  DialogDescription,
  DialogClose,
} from 'reka-ui'
import { ShieldCheck, AlertTriangle, X } from 'lucide-vue-next'
import { useQueryClient } from '@tanstack/vue-query'
import { api, request, sessionVersion, type Schema } from '@/api/client'
import { useUI, useSession } from '@/lib/store'
import { money, date } from '@/lib/utils'
import Button from './ui/button/Button.vue'
import Status from './Status.vue'
import JsonDetails from './JsonDetails.vue'
const props = defineProps<{ banking?: boolean }>()
const object = (v: unknown): Record<string, unknown> =>
  v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : {}
const sourceSnapshot = computed(() => object(view.value?.operation.snapshot?.source_account))
const targetSnapshot = computed(() => object(view.value?.operation.snapshot?.target_account))
const cardDetails = computed(() => {
  const parameters = object(view.value?.operation.parameters)
  const portfolio = object(view.value?.operation.snapshot).cards
  const saved = Array.isArray(portfolio) ? portfolio.map(object).find(card => card.id === parameters.card_id) : undefined
  return { ...saved, ...parameters }
})
const recipientDetails = computed(() => object(view.value?.operation.snapshot?.recipient))
const cardActionLabel: Record<string,string> = {bind:'绑定模拟卡',unbind:'解绑模拟卡',set_default:'设为默认卡'}
const open = ref(false),
  busy = ref(false),
  view = ref<Schema<'ExecutionView'>>(),
  error = ref(''),
  ack = ref(false),
  qc = useQueryClient(),
  ui = useUI(),
  session = useSession()
const executeKeys = new Map<string, string>()
let active = true
let pendingPreparation: { payload: string; key: string } | undefined
const currentSession = (epoch: number) => active && sessionVersion() === epoch
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
const expired = computed(
  () =>
    !!view.value &&
    view.value.operation.expires_at <= now.value &&
    ['pending_authorization', 'authorized'].includes(view.value.operation.status),
)
async function reject() {
  if (busy.value || !view.value || expired.value) return
  busy.value = true
  const epoch = sessionVersion()
  error.value = ''
  ack.value = false
  try {
    const op = view.value.operation
    const rejected = await request<Schema<'ExecutionView'>>(
      `/api/v1/execution/operations/${op.id}/authorize`,
      {
        method: 'POST',
        body: {
          decision: 'reject',
          intent_digest: op.intent_digest,
          confirmation_nonce: op.confirmation_nonce,
        },
      },
    )
    if (!currentSession(epoch)) return
    view.value = rejected
    ui.notify('已拒绝这项金融操作')
    await qc.invalidateQueries()
  } catch (e) {
    if (!currentSession(epoch)) return
    error.value = (e as Error).message + '。请查询最终状态。'
  } finally {
    busy.value = false
  }
}
async function prepare(
  kind: Schema<'ExecutionPrepareInput'>['kind'],
  parameters: Schema<'ExecutionPrepareInput'>['parameters'],
) {
  if (busy.value) return
  if (!session.connected) {
    ui.notify('请先登录')
    return
  }
  busy.value = true
  const epoch = sessionVersion()
  error.value = ''
  view.value = undefined
  ack.value = false
  open.value = true
  try {
    const payload = JSON.stringify({ kind, parameters })
    if (pendingPreparation?.payload !== payload)
      pendingPreparation = { payload, key: crypto.randomUUID() }
    const prepared = await api.prepare({ kind, parameters }, pendingPreparation.key)
    if (!currentSession(epoch)) return
    view.value = prepared
    pendingPreparation = undefined
  } catch (e) {
    if (!currentSession(epoch)) return
    error.value = (e as Error).message
  } finally {
    busy.value = false
  }
}
async function inspect(id: string) {
  if (busy.value || !session.connected) return
  const epoch = sessionVersion()
  busy.value = true
  error.value = ''
  view.value = undefined
  ack.value = false
  open.value = true
  try {
    const inspected = await request<Schema<'ExecutionView'>>(`/api/v1/execution/operations/${id}`)
    if (!currentSession(epoch)) return
    view.value = inspected
  } catch (e) {
    if (!currentSession(epoch)) return
    error.value = (e as Error).message
  } finally {
    busy.value = false
  }
}
async function refresh() {
  if (view.value) await inspect(view.value.operation.id)
}
async function execute() {
  if (busy.value || !view.value || !ack.value || expired.value) return
  const ownEpoch = sessionVersion()
  busy.value = true
  error.value = ''
  const id = view.value.operation.id
  try {
    let current = view.value
    if (current.operation.status !== 'authorized')
      current = await request<Schema<'ExecutionView'>>(
        `/api/v1/execution/operations/${id}/authorize`,
        {
          method: 'POST',
          body: {
            decision: 'approve',
            intent_digest: current.operation.intent_digest,
            confirmation_nonce: current.operation.confirmation_nonce,
          },
        },
      )
    if (!currentSession(ownEpoch)) return
    view.value = current
    if (current.operation.status !== 'authorized')
      throw new Error('服务器未授予有效执行授权：' + current.operation.status)
    if (!executeKeys.has(id)) executeKeys.set(id, crypto.randomUUID())
    const op = current.operation
    const executed = await request<Schema<'ExecutionView'>>(
      `/api/v1/execution/operations/${id}/execute`,
      {
        method: 'POST',
        body: {
          authorization_id: op.authorization_id,
          intent_digest: op.intent_digest,
          expected_version: op.version,
        },
        key: executeKeys.get(id),
      },
    )
    if (!currentSession(ownEpoch)) return
    view.value = executed
    ui.notify('服务器操作状态：' + view.value.operation.status)
    await qc.invalidateQueries()
  } catch (e) {
    if (!currentSession(ownEpoch)) return
    error.value = (e as Error).message + '。请查询最终状态后决定下一步。'
    try {
      view.value = await request<Schema<'ExecutionView'>>(`/api/v1/execution/operations/${id}`)
    } catch {
      /* Keep outcome unknown until an explicit server query succeeds. */
    }
  } finally {
    busy.value = false
  }
}
defineExpose({ prepare, inspect })
const executable = (status: string) => ['pending_authorization', 'authorized'].includes(status)
const kindLabel: Record<string, string> = {
  aa_payment: '支付本人AA分摊',
  recipient_transfer: '向已选收款人转账',
  card_manage: '管理模拟银行卡',
  transfer: '模拟银行转账',
  cancel_subscription: '取消模拟订阅',
  create_savings_goal: '创建目标金',
  savings_deposit: '存入目标金',
  savings_withdrawal: '取出目标金',
  budget_rule_update: '调整预算规则',
  account_status: '调整账户状态',
  open_account: '创建模拟账户',
  import_transaction: '导入模拟交易',
  create_budget: '创建预算',
  subscription_manage: '管理模拟订阅',
}
const riskLabel: Record<string, string> = {
  low: '低风险',
  medium: '中风险',
  high: '高风险',
  critical: '严重风险',
}
</script>
<template>
  <DialogRoot v-model:open="open"
    ><DialogPortal
      ><DialogOverlay class="dialog-overlay" /><DialogContent
        class="dialog-content execution-dialog"
        :class="{ 'banking-confirmation': props.banking }"
        @escape-key-down="busy && $event.preventDefault()"
        @pointer-down-outside="busy && $event.preventDefault()"
        ><DialogTitle><ShieldCheck :size="23" />可信金融操作确认</DialogTitle
        ><DialogDescription
          >这是模拟银行操作。请核对服务器生成的操作意向，确认后才会授权执行。</DialogDescription
        >
        <div v-if="props.banking" class="banking-simulation">比赛模拟环境 · 非真实资金交易</div>
        <div v-if="busy && !view" class="skeleton-group" aria-busy="true">
          <div class="skeleton" />
          <div class="skeleton" />
        </div>
        <template v-if="view"
          ><div class="operation-header">
            <b>{{ kindLabel[view.operation.kind] || view.operation.kind }}</b
            ><Status :value="view.operation.status" />
          </div>
          <dl v-if="view.operation.kind === 'card_manage'" class="detail-grid">
            <dt>卡片操作</dt><dd>{{ cardActionLabel[String(cardDetails.action)] }}</dd>
            <dt>卡片名称 / 发行机构</dt><dd>{{ cardDetails.name }} · {{ cardDetails.issuer }}</dd>
            <dt>模拟卡尾号</dt><dd>•••• {{ cardDetails.last_four }}</dd>
            <dt>关联模拟账户</dt><dd>{{ cardDetails.account_id }}</dd>
            <dt>资金影响</dt><dd>仅更改卡片管理信息，不扣款、不增加余额。</dd>
          </dl>
          <div v-else class="confirmation-amount">
            <span>本次操作金额 · 人民币</span
            ><strong>{{ money(view.operation.amount_minor) }}</strong>
          </div>
          <dl v-if="view.operation.kind === 'recipient_transfer'" class="detail-grid">
            <dt>收款人显示名</dt><dd>{{ recipientDetails.display_name }}</dd>
            <dt>收款人手机号</dt><dd>{{ recipientDetails.phone_masked }}</dd>
            <dt>验证性质</dt><dd>{{ recipientDetails.phone_verification_method === 'development' ? '开发模拟验证，不是真实短信或姓名实名认证' : '手机号已验证，不代表姓名实名认证' }}</dd>
          </dl>
          <dl v-if="view.operation.kind === 'aa_payment'" class="detail-grid">
            <dt>AA账单</dt><dd>{{ view.operation.snapshot?.bill_title }}</dd>
            <dt>本人参与位置</dt><dd>{{ view.operation.snapshot?.participant_name }}</dd>
            <dt>付款范围</dt><dd>只支付本人这一份分摊，不代扣其他参与人。</dd>
          </dl>
          <div v-if="props.banking" class="banking-account-verification">
            <div>
              <span>付款账户快照</span
              ><b>{{ sourceSnapshot.display_name || view.operation.source_account_id }}</b
              ><small>{{ sourceSnapshot.masked_account_number || '账户信息见下方核验快照' }}</small>
            </div>
            <div>
              <span>收款账户快照</span
              ><b>{{
                targetSnapshot.display_name ||
                (view.operation.target_account_id === '0'
                  ? view.operation.resource_id
                  : view.operation.target_account_id)
              }}</b
              ><small>{{ targetSnapshot.masked_account_number || '账户信息见下方核验快照' }}</small>
            </div>
          </div>
          <dl class="detail-grid">
            <dt>付款账户</dt>
            <dd>
              {{
                view.operation.source_account_id === '0'
                  ? '不适用'
                  : view.operation.source_account_id
              }}
            </dd>
            <dt>收款账户 / 关联资源</dt>
            <dd>
              {{
                view.operation.target_account_id !== '0'
                  ? view.operation.target_account_id
                  : view.operation.resource_id
              }}
            </dd>
            <dt>用途</dt>
            <dd>{{ view.operation.parameters?.description || view.operation.kind }}</dd>
            <dt>服务端风险等级</dt>
            <dd class="confirmation-risk" :data-risk="view.operation.risk_level">
              {{
                riskLabel[view.operation.risk_level] || view.operation.risk_level || '尚未返回评级'
              }}
            </dd>
            <dt>意向有效期</dt>
            <dd>{{ date(view.operation.expires_at) }}</dd>
          </dl>
          <div class="warning-note">
            <AlertTriangle :size="18" /><span
              >执行前会再次核验账户、余额和资源版本。取消订阅仅修改模拟银行状态，不代表商户已取消代扣。</span
            >
          </div>
          <div v-for="r in view.risk_events" :key="r.id" class="risk-row">
            {{ r.rule_name }} · {{ r.decision }} · {{ r.error_code }}
            <JsonDetails v-if="props.banking" :value="r.evidence" label="风险核验证据" />
          </div>
          <JsonDetails
            :value="view.operation.snapshot"
            label="查看账户核验与操作快照"
          /><JsonDetails :value="view.operation.parameters" label="查看完整操作参数" /><JsonDetails
            v-if="['succeeded', 'executed'].includes(view.operation.status)"
            :value="view.operation.result"
            label="查看服务器执行结果"
          />
          <p v-if="expired" class="form-error" role="alert">
            该金融操作意向已过期，请关闭后重新准备。
          </p>
          <label v-if="executable(view.operation.status)" class="checkbox-label"
            ><input
              type="checkbox"
              v-model="ack"
              :disabled="busy || expired"
            />我已核对账户、金额、用途和风险，授权执行此操作</label
          ></template
        >
        <p v-if="error" class="form-error" role="alert">{{ error }}</p>
        <div class="dialog-actions">
          <Button v-if="view" variant="outline" @click="refresh" :disabled="busy"
            >查询最终状态</Button
          ><Button
            v-if="view?.operation.status === 'pending_authorization'"
            variant="outline"
            :disabled="busy || expired"
            @click="reject"
            >拒绝此操作</Button
          ><Button
            v-if="view && executable(view.operation.status)"
            @click="execute"
            :disabled="busy || !ack || expired"
            >{{ busy ? '正在处理…' : '授权并执行' }}</Button
          ><DialogClose v-else as-child
            ><Button variant="outline" :disabled="busy">关闭</Button></DialogClose
          >
        </div>
        <DialogClose v-if="!busy" class="dialog-x" aria-label="关闭"
          ><X :size="18" /></DialogClose></DialogContent></DialogPortal
  ></DialogRoot>
</template>

<style scoped>
.banking-confirmation {
  max-width: 620px;
  border: 1px solid #e7edf3;
  box-shadow: 0 24px 80px #142d4e26;
}
.banking-confirmation :deep(h2) {
  font-size: 22px;
}
.banking-simulation {
  font-size: 13px;
  color: #946519;
  background: #fffbf2;
  border: 1px solid #eadbbd;
  border-radius: 6px;
  padding: 9px 12px;
  margin-top: 20px;
}
.banking-confirmation .operation-header {
  margin-top: 20px;
}
.banking-confirmation .confirmation-amount {
  background: #f6f8fb;
  border-radius: 10px;
  padding: 20px;
  margin: 20px 0;
}
.banking-confirmation .confirmation-amount > span {
  font-size: 14px;
}
.banking-confirmation .confirmation-amount > strong {
  font-size: 36px;
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.8px;
}
.banking-account-verification {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 20px;
  margin: 20px 0;
}
.banking-account-verification > div {
  display: grid;
  gap: 8px;
  min-width: 0;
}
.banking-account-verification span {
  font-size: 13px;
  color: #64748b;
}
.banking-account-verification b {
  font-size: 15px;
  font-weight: 500;
  color: #172033;
  overflow-wrap: anywhere;
}
.banking-account-verification small {
  font-size: 13px;
  color: #64748b;
  overflow-wrap: anywhere;
}
.banking-confirmation .detail-grid {
  font-size: 14px;
  line-height: 1.7;
}
.banking-confirmation .detail-grid dt {
  color: #64748b;
}
.banking-confirmation .detail-grid dd {
  overflow-wrap: anywhere;
}
.banking-confirmation .warning-note {
  font-size: 14px;
  line-height: 1.8;
}
.banking-confirmation .checkbox-label {
  font-size: 14px;
  line-height: 1.8;
  padding: 16px;
  border: 1px solid #dbe7e2;
  border-radius: 8px;
  background: #f5faf8;
  align-items: flex-start;
  flex-direction: row;
}
.banking-confirmation .checkbox-label input {
  width: 16px;
  height: 16px;
  flex: none;
  margin-top: 4px;
}
.banking-confirmation :deep(pre) {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.banking-confirmation .dialog-actions {
  gap: 12px;
  flex-wrap: wrap;
}
@media (max-width: 600px) {
  .banking-account-verification {
    grid-template-columns: 1fr;
    gap: 16px;
  }
  .banking-confirmation .confirmation-amount > strong {
    font-size: 32px;
  }
  .banking-confirmation .dialog-actions {
    display: grid;
    grid-template-columns: 1fr;
  }
  .banking-confirmation .dialog-actions > button {
    width: 100%;
  }
}
</style>
