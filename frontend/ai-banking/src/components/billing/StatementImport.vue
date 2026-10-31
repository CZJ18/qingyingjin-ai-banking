<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { request, type Schema } from '@/api/client'
import { useSession } from '@/lib/store'
import { money, date } from '@/lib/utils'
import Button from '@/components/ui/button/Button.vue'
import ExecutionDialog from '@/components/ExecutionDialog.vue'
import { statementFields, statementHeaders, suggestColumns, validateColumns } from '@/lib/statement-columns'
withDefaults(defineProps<{initialOpen?:boolean}>(),{initialOpen:false})
const emit = defineEmits<{ 'view-month': [month: string] }>()

const session = useSession()
const csv = ref(''), error = ref(''), busy = ref(false)
const columns = ref<Record<string, string>>({})
const advanced = ref(false), mapping = ref('{}')
const headerState = computed(() => {
  try { return { headers: csv.value ? statementHeaders(csv.value) : [], error: '' } }
  catch (e) { return { headers: [], error: (e as Error).message } }
})
watch(() => JSON.stringify(headerState.value.headers), () => {
  columns.value = suggestColumns(headerState.value.headers)
}, { immediate: true })
watch(advanced, value => { if (value) mapping.value = JSON.stringify(columns.value, null, 2) })
const preview = ref<Schema<'StatementPreview'>>()
const dialog = ref<InstanceType<typeof ExecutionDialog>>()
const completed = ref(new Set<string>())
const completedMonths = ref(new Set<string>())
const importedMonths = computed(() => [...completedMonths.value].sort().reverse())
let pendingImport: { generation: number; sessionEpoch: number; accountId: string; record: Schema<'StatementRecord'> } | undefined
const recordKey = (record: Schema<'StatementRecord'>) => JSON.stringify([
  record.external_transaction_id, record.source.file_sha256, record.source.row_sha256, record.source.row_number,
])
let generation = 0, fileGeneration = 0
function clearImportState() {
  generation++
  preview.value = undefined
  completed.value.clear()
  completedMonths.value.clear()
  pendingImport = undefined
  error.value = ''
}
watch([() => session.sessionEpoch, () => session.accountId, csv, columns, mapping, advanced], clearImportState, { deep: true })
function viewMonth(month: string) {
  if (session.connected && session.accountId && completedMonths.value.has(month)) emit('view-month', month)
}
function prepareRecord(record: Schema<'StatementRecord'>) {
  if (!session.connected || !session.accountId || completed.value.has(recordKey(record))) return
  pendingImport = { generation, sessionEpoch: session.sessionEpoch, accountId: session.accountId, record }
  void dialog.value?.prepare('import_transaction', { ...record, bank_account_id: session.accountId })
}
function onServerView(result: Schema<'ExecutionView'>) {
  const pending = pendingImport, op = result.operation
  if (!pending || !session.connected || pending.generation !== generation ||
      pending.sessionEpoch !== session.sessionEpoch || pending.accountId !== session.accountId ||
      op.kind !== 'import_transaction' || !['succeeded', 'executed'].includes(op.status)) return
  const params = op.parameters || {}, source = params.source as Partial<Schema<'StatementSource'>> | undefined
  if (params.bank_account_id !== pending.accountId || params.external_transaction_id !== pending.record.external_transaction_id ||
      params.effect_mode !== 'record_only' || source?.kind !== 'unverified_csv_upload' ||
      source.file_sha256 !== pending.record.source.file_sha256 || source.row_sha256 !== pending.record.source.row_sha256 ||
      source.row_number !== pending.record.source.row_number) return
  completed.value.add(recordKey(pending.record))
  const occurredAt = new Date(pending.record.occurred_at)
  if (!Number.isNaN(occurredAt.getTime())) {
    const month = new Intl.DateTimeFormat('sv-SE', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit' }).format(occurredAt)
    if (/^\d{4}-(0[1-9]|1[0-2])$/.test(month)) completedMonths.value.add(month)
  }
}
watch(() => session.sessionEpoch, () => { fileGeneration++; csv.value = ''; mapping.value = '{}'; advanced.value = false })
async function readFile(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) return
  clearImportState()
  const epoch = generation, fileVersion = ++fileGeneration, sessionEpoch = session.sessionEpoch, accountId = session.accountId
  if (file.size > 262144) {
    csv.value = ''; await nextTick()
    if (fileVersion === fileGeneration) error.value = 'CSV 不能超过 256 KiB'
    return
  }
  try {
    const text = new TextDecoder('utf-8', { fatal: true }).decode(await file.arrayBuffer())
    if (epoch === generation && fileVersion === fileGeneration) csv.value = text
  } catch {
    if (epoch !== generation || fileVersion !== fileGeneration) return
    csv.value = ''; await nextTick()
    if (fileVersion === fileGeneration && sessionEpoch === session.sessionEpoch && accountId === session.accountId)
      error.value = '无法按 UTF-8 读取 CSV，请在本机另存为 UTF-8 后重试'
  }
}
async function inspect() {
  if (busy.value || !session.connected || !session.accountId) return
  const epoch = generation
  busy.value = true
  error.value = ''
  preview.value = undefined
  try {
    if (new TextEncoder().encode(csv.value).length > 262144) throw new Error('CSV 不能超过 256 KiB')
    if (headerState.value.error) throw new Error(headerState.value.error)
    const chosen = advanced.value ? JSON.parse(mapping.value) : columns.value
    if (!chosen || typeof chosen !== 'object' || Array.isArray(chosen)) throw new Error('字段映射必须是对象')
    const mappingError = validateColumns(headerState.value.headers, chosen)
    if (mappingError) throw new Error(mappingError)
    const selected = Object.fromEntries(Object.entries(chosen).filter(([,name]) => name !== ''))
    const result = await request<Schema<'StatementPreview'>>('/api/v1/transactions/import/preview', {
      method: 'POST', body: { account_id: session.accountId, csv: csv.value, timezone: 'Asia/Shanghai', columns: selected },
    })
    if (epoch === generation) preview.value = result
  } catch (e) {
    if (epoch === generation) error.value = e instanceof SyntaxError ? '字段映射必须是有效 JSON 对象' : (e as Error).message
  } finally { busy.value = false }
}
</script>

<template>
  <details class="panel statement-import" :open="initialOpen">
    <summary>导入 CSV 历史账单</summary>
    <p>可导入本人从银行或支付平台导出的 CSV 账单。用户上传记录未经银行核验；仅补录分析数据，不改变账户余额。每笔导入均需单独核对并授权，上传与预览本身不调用模型。</p>
    <p>建议新建专用的零余额模拟账户用于账单分析，避免演示流水混入；该账户不代表真实银行账户或余额。</p>
    <p>金额为正数十进制元（如 15.00），不接受千分位或货币符号。方向为 income / expense 或 收入 / 支出；状态为 posted / pending / reversed / failed 或 已入账 / 待入账 / 已冲正 / 失败；币种为 CNY / 人民币。时间为 RFC3339 或 YYYY-MM-DD HH:mm:ss（本地时间按上海时区）；仅补录已入账的人民币外部交易，最多 1000 行。</p>
    <label>选择 CSV 文件<input type="file" accept=".csv,text/csv" aria-label="选择 CSV 账单" @change="readFile" /></label>
    <label>CSV 内容<textarea v-model="csv" rows="5" aria-label="CSV 账单内容" placeholder="id,occurred_at,amount,direction,currency,merchant,category,status" /></label>
    <fieldset v-if="headerState.headers.length && !advanced" class="column-mapping">
      <legend>匹配账单列</legend>
      <p>已自动识别常见列名，请核对后预览。标有「必填」的项目都需要选择。</p>
      <label v-for="field in statementFields" :key="field.key">{{ field.label }} · {{ field.required ? '必填' : '选填' }}
        <select v-model="columns[field.key]" :aria-label="'账单列：' + field.label">
          <option value="">{{ field.required ? '请选择对应列' : '不使用此字段' }}</option>
          <option v-for="header in headerState.headers" :key="header" :value="header">{{ header }}</option>
        </select>
      </label>
    </fieldset>
    <p v-if="headerState.error" role="alert">{{ headerState.error }}</p>
    <details class="advanced-mapping">
      <summary>高级字段映射（可选）</summary>
      <label><input v-model="advanced" type="checkbox" />使用 JSON 自定义映射</label>
      <label v-if="advanced">字段映射 JSON<textarea v-model="mapping" rows="5" aria-label="CSV 字段映射" /></label>
    </details>
    <Button :disabled="busy || !session.connected || !session.accountId || !csv" @click="inspect">{{ busy ? '检查中…' : '预览历史账单' }}</Button>
    <p v-if="!session.accountId">请先选择属于你的模拟账户。</p>
    <p v-if="error" role="alert" class="form-error">{{ error }}</p>
    <template v-if="preview">
      <p v-for="warning in preview.warnings" :key="warning">{{ warning }}</p>
      <p>可导入 {{ preview.records.length }} 笔，跳过 {{ preview.skipped }} 笔。文件摘要 {{ preview.file_sha256 }}</p>
      <div v-for="record in preview.records" :key="record.external_transaction_id" class="import-row">
        <span>{{ record.merchant_name || record.external_transaction_id }} · {{ date(record.occurred_at) }} · {{ record.direction === 'expense' ? '支出' : '收入' }} {{ money(record.amount_minor) }}</span>
        <Button variant="outline" :disabled="completed.has(recordKey(record))" @click="prepareRecord(record)">{{ completed.has(recordKey(record)) ? '已补录' : '核对并授权补录' }}</Button>
      </div>
      <p v-if="!preview.records.length">没有可导入的已入账记录，请检查状态和字段映射。</p>
    </template>
    <div v-if="importedMonths.length" class="import-months" aria-label="本次已补录月份">
      <span>本次已补录，可查看对应月份：</span>
      <Button v-for="month in importedMonths" :key="month" variant="outline" @click="viewMonth(month)">查看{{ month }}月账单</Button>
    </div>
    <ExecutionDialog ref="dialog" @server-view="onServerView" />
  </details>
</template>

<style scoped>
.statement-import { margin-bottom: 20px; overflow-wrap: anywhere; }
.statement-import summary { cursor: pointer; font-weight: 600; }
.statement-import p { margin: 12px 0; }
.statement-import label { display: grid; gap: 6px; margin: 12px 0; }
.statement-import textarea { width: 100%; padding: 10px; border: 1px solid var(--border, #d6dce4); border-radius: 8px; }
.column-mapping { border: 1px solid #d6dce4; border-radius: 8px; padding: 12px; display: grid; grid-template-columns: repeat(auto-fit,minmax(min(100%,210px),1fr)); gap: 8px 16px; }
.column-mapping p { grid-column: 1 / -1; }
.column-mapping select { width: 100%; min-width: 0; padding: 8px; border: 1px solid #d6dce4; border-radius: 6px; background: white; }
.advanced-mapping { margin: 12px 0; }
.import-row { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 12px; padding: 10px 0; }
.import-months { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin: 12px 0; }
</style>
