<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { ArrowRight, ArrowUpRight, FileCheck, AlertTriangle } from 'lucide-vue-next'
import { useSession, useUI } from '@/lib/store'
import { useAccounts, useOperations } from '@/lib/queries'
import { request, type Schema } from '@/api/client'
import { money, minor, date } from '@/lib/utils'
import Button from '@/components/ui/button/Button.vue'
import State from '@/components/State.vue'
import Status from '@/components/Status.vue'
import JsonDetails from '@/components/JsonDetails.vue'
import ExecutionDialog from '@/components/ExecutionDialog.vue'
import AccountManager from '@/components/AccountManager.vue'
import CardManager from '@/components/CardManager.vue'
import AABills from '@/components/AABills.vue'
import RecipientTransfer from '@/components/RecipientTransfer.vue'
import OperationInspector from '@/components/security/OperationInspector.vue'
const s = useSession(),
  ui = useUI(),
  accounts = useAccounts(),
  operations = useOperations(),
  dialog = ref<InstanceType<typeof ExecutionDialog>>(),
  target = ref(''),
  amount = ref(''),
  purpose = ref(''),
  auditId = ref(''),
  filter = ref('')
const rules = useQuery({
  queryKey: ['risk-rules'],
  enabled: computed(() => s.connected),
  queryFn: ({ signal }) =>
    request<Schema<'ExecutionRiskRule'>[]>('/api/v1/execution/risk-rules', { signal }),
})
const audits = useQuery({
  queryKey: computed(() => ['execution-audits', auditId.value]),
  enabled: computed(() => s.connected && !!auditId.value),
  queryFn: ({ signal }) =>
    request<Schema<'ExecutionAudit'>[]>(`/api/v1/execution/operations/${auditId.value}/audits`, {
      signal,
    }),
})
const filtered = computed(
  () => operations.data.value?.filter((o) => !filter.value || o.status === filter.value) || [],
)
async function prepare() {
  try {
    if (!s.accountId || s.accountId === target.value) throw new Error('请选择不同的付款和收款账户')
    await dialog.value?.prepare('transfer', {
      source_account_id: s.accountId,
      target_account_id: target.value,
      amount_minor: minor(amount.value),
      currency: 'CNY',
      description: purpose.value,
    })
  } catch (e) {
    ui.notify((e as Error).message)
  }
}

const selectedId = ref(''),
  search = ref(''),
  pageNumber = ref(1)
const pending = computed(
  () =>
    operations.data.value?.filter((o) =>
      ['prepared', 'pending_authorization', 'authorized', 'pending_approval'].includes(o.status),
    ) || [],
)
const history = computed(() =>
  filtered.value
    .filter(
      (o) =>
        !search.value ||
        [o.id, o.kind, o.source_account_id, o.target_account_id].some((value) =>
          value?.includes(search.value),
        ),
    )
    .sort((a, b) => b.created_at - a.created_at),
)
const pageCount = computed(() => Math.max(1, Math.ceil(history.value.length / 8)))
const paged = computed(() => history.value.slice((pageNumber.value - 1) * 8, pageNumber.value * 8))
watch([filter, search], () => (pageNumber.value = 1))
watch(pageCount, (count) => (pageNumber.value = Math.min(pageNumber.value, count)))
watch(
  () => operations.data.value,
  (rows) => {
    if (rows?.length && !rows.some((o) => o.id === selectedId.value))
      selectedId.value = pending.value[0]?.id || rows[0]!.id
  },
  { immediate: true },
)
const detail = useQuery({
  queryKey: computed(() => ['execution-view', selectedId.value]),
  enabled: computed(() => s.connected && !!selectedId.value),
  queryFn: ({ signal }) =>
    request<Schema<'ExecutionView'>>(`/api/v1/execution/operations/${selectedId.value}`, {
      signal,
    }),
  refetchInterval: 5000,
})
function inspectRecord(id: string) {
  selectedId.value = id
  auditId.value = id
}
const kindLabel: Record<string, string> = {
  card_manage: '管理模拟银行卡',
  transfer: '模拟银行转账',
  aa_payment: 'AA分摊付款',
  recipient_transfer: '向用户转账',
  cancel_subscription: '取消模拟订阅',
  savings_deposit: '存入目标金',
  savings_withdrawal: '取出目标金',
  create_savings_goal: '创建目标金',
  open_account: '创建模拟账户',
}
const riskLabel: Record<string, string> = {
  low: '低风险',
  medium: '中风险',
  high: '高风险',
  critical: '严重风险',
}
</script>
<template>
  <div class="page execution-bank">
    <header class="execution-heading">
      <div>
        <h1>可信执行与安全中心</h1>
        <p>核对交易、明确授权，追踪服务器最终执行状态。</p>
      </div>
      <span class="simulation-badge">模拟账户 · 模拟资金</span>
    </header>
    <section class="execution-summary">
      <div>
        <span>待处理操作</span
        ><b>{{ s.connected && operations.data.value ? pending.length : '—' }}</b>
      </div>
      <div>
        <span>已执行操作</span
        ><b>{{
          s.connected && operations.data.value
            ? operations.data.value.filter((o) => ['executed', 'succeeded'].includes(o.status))
                .length
            : '—'
        }}</b>
      </div>
      <div>
        <span>失败或拒绝</span
        ><b>{{
          s.connected && operations.data.value
            ? operations.data.value.filter((o) => ['failed', 'rejected'].includes(o.status)).length
            : '—'
        }}</b>
      </div>
      <p>创建意向 → 核对风险 → 用户授权 → 查询执行结果</p>
    </section>
    <RecipientTransfer />
    <section class="execution-panel pending-queue">
      <header class="execution-section">
        <div>
          <h2>待确认与待处理操作</h2>
          <p>选择操作，查看账户、金额与风险详情。</p>
        </div>
        <Button variant="ghost" :disabled="!s.connected" @click="operations.refetch()"
          >刷新记录</Button
        >
      </header>
      <State
        :connected="s.connected"
        :loading="operations.isLoading.value"
        :error="operations.error.value"
        :empty="!pending.length"
        title="暂无待处理操作"
        description="新的银行操作意向会在这里等待核对。"
        @retry="operations.refetch()"
        ><div class="pending-records">
          <button
            v-for="o in pending"
            :key="o.id"
            :class="{ selected: selectedId === o.id }"
            :aria-pressed="selectedId === o.id"
            @click="inspectRecord(o.id)"
          >
            <div>
              <b>{{ kindLabel[o.kind] || o.kind }}</b
              ><span>#{{ o.id.slice(0, 12) }}</span>
            </div>
            <strong>{{ money(o.amount_minor) }}</strong
            ><Status :value="o.status" /><ArrowRight :size="14" />
          </button></div
      ></State>
    </section>
    <div class="security-layout execution-workspace">
      <section class="execution-panel transfer-composer">
        <header class="execution-section">
          <div>
            <h2>模拟银行转账</h2>
            <p>填写交易信息，下一步核对服务器生成的意向。</p>
          </div>
          <ArrowUpRight :size="18" />
        </header>
        <form @submit.prevent="prepare">
          <div class="form-columns transfer-accounts">
            <label
              >付款账户<select v-model="s.accountId" required>
                <option value="" disabled>选择付款账户</option>
                <option v-for="a in accounts.data.value" :key="a.id" :value="a.id">
                  {{ a.display_name }} · {{ a.masked_account_number }} ·
                  {{ money(a.balance_minor) }}
                </option>
              </select></label
            ><label
              >收款账户<select v-model="target" required>
                <option value="" disabled>选择收款账户</option>
                <option
                  v-for="a in accounts.data.value?.filter((a) => a.id !== s.accountId)"
                  :key="a.id"
                  :value="a.id"
                >
                  {{ a.display_name }} · {{ a.masked_account_number }}
                </option>
              </select></label
            >
          </div>
          <div class="form-columns">
            <label
              >转账金额 / 元<input
                v-model="amount"
                required
                inputmode="decimal"
                placeholder="0.00" /></label
            ><label
              >转账用途<input
                v-model="purpose"
                required
                maxlength="512"
                placeholder="例如：健康生活预算"
            /></label>
          </div>
          <div class="warning-note">
            <AlertTriangle :size="18" /><span
              >仅在模拟账户间执行。下一步创建操作意向并返回核验信息，不会立即转账。</span
            >
          </div>
          <Button type="submit" :disabled="!s.connected || !s.accountId" class="w-full"
            >创建转账意向<ArrowRight :size="15"
          /></Button>
        </form>

        <details class="rules-details">
          <summary>当前风险规则 · {{ rules.data.value?.length || 0 }}</summary>
          <State
            :connected="s.connected"
            :loading="rules.isLoading.value"
            :error="rules.error.value"
            :empty="!rules.data.value?.length"
            @retry="rules.refetch()"
            ><div v-for="r in rules.data.value" :key="r.name" class="risk-rule">
              <div class="row-between">
                <b>{{ r.name }}</b
                ><span>{{ r.enabled ? '已启用' : '未启用' }}</span>
              </div>
              <p>{{ r.description }}</p>
              <small
                >版本 {{ r.version }} · {{ riskLabel[r.risk_level] || r.risk_level }} · 限额
                {{ money(r.limit_minor) }}</small
              >
            </div></State
          >
        </details>
      </section>
      <section class="execution-panel transaction-inspector">
        <header class="execution-section">
          <div>
            <h2>交易详情与追踪</h2>
            <p>每 5 秒同步服务器记录。</p>
          </div>
          <Button variant="ghost" :disabled="!selectedId || !s.connected" @click="detail.refetch()"
            >刷新状态</Button
          >
        </header>
        <State
          :connected="s.connected"
          :loading="detail.isLoading.value"
          :error="detail.error.value"
          :empty="!selectedId"
          title="尚未选择交易"
          description="选择待处理操作或历史记录查看核验结果。"
          @retry="detail.refetch()"
          ><OperationInspector
            v-if="detail.data.value"
            :view="detail.data.value"
            @confirm="dialog?.inspect(selectedId)"
        /></State>
      </section>
    </div>
    <section class="execution-panel transaction-table">
      <header class="execution-section">
        <div>
          <h2>历史执行记录</h2>
          <p>筛选当前已加载的服务端操作记录。</p>
        </div>
        <div class="history-filters">
          <input
            v-model="search"
            aria-label="搜索已加载操作"
            placeholder="编号、类型或账户"
          /><select v-model="filter" aria-label="操作状态筛选">
            <option value="">全部状态</option>
            <option value="pending_authorization">待授权</option>
            <option value="authorized">已授权</option>
            <option value="executed">已执行</option>
            <option value="succeeded">成功</option>
            <option value="failed">失败</option>
            <option value="rejected">已拒绝</option>
            <option value="expired">过期</option>
          </select>
        </div>
      </header>
      <State
        :connected="s.connected"
        :loading="operations.isLoading.value"
        :error="operations.error.value"
        :empty="!history.length"
        title="暂无匹配操作"
        @retry="operations.refetch()"
        ><div class="table-scroll">
          <table>
            <thead>
              <tr>
                <th>操作类型 / 编号</th>
                <th>金额</th>
                <th>风险等级</th>
                <th>创建时间</th>
                <th>服务器状态</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="o in paged" :key="o.id" :class="{ selected: selectedId === o.id }">
                <td>
                  <b>{{ kindLabel[o.kind] || o.kind }}</b
                  ><small>{{ o.id.slice(0, 12) }}</small>
                </td>
                <td class="execution-amount">{{ money(o.amount_minor) }}</td>
                <td>{{ riskLabel[o.risk_level] || o.risk_level || '未返回' }}</td>
                <td>{{ date(o.created_at) }}</td>
                <td><Status :value="o.status" /></td>
                <td>
                  <div class="button-group">
                    <Button variant="ghost" @click="inspectRecord(o.id)">详情</Button
                    ><Button variant="ghost" @click="dialog?.inspect(o.id)">查看 / 确认</Button
                    ><Button variant="ghost" @click="auditId = o.id">审计</Button>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <footer class="operation-pagination">
          <span>{{ history.length }} 条已加载记录 · 第 {{ pageNumber }} / {{ pageCount }} 页</span>
          <div>
            <Button variant="outline" :disabled="pageNumber <= 1" @click="pageNumber--"
              >上一页</Button
            ><Button variant="outline" :disabled="pageNumber >= pageCount" @click="pageNumber++"
              >下一页</Button
            >
          </div>
        </footer></State
      >
    </section>
    <section v-if="auditId" class="execution-panel transaction-detail">
      <header class="execution-section">
        <div>
          <h2>操作审计日志</h2>
          <p>#{{ auditId }}</p>
        </div>
        <Button variant="ghost" @click="auditId = ''">关闭详情</Button>
      </header>
      <State
        :connected="s.connected"
        :loading="audits.isLoading.value"
        :error="audits.error.value"
        :empty="!audits.data.value?.length"
        title="暂无审计日志"
        @retry="audits.refetch()"
        ><div class="audit-records">
          <div v-for="a in audits.data.value" :key="a.id" class="bank-audit-row">
            <FileCheck :size="17" />
            <div>
              <div>
                <b>{{ a.permission }}</b
                ><Status :value="a.state" />
              </div>
              <p>{{ date(a.created_at) }} · {{ a.error_code || '未返回错误' }}</p>
              <small>Trace {{ a.trace_id || '未返回' }}</small
              ><JsonDetails :value="a" label="审计详情" />
            </div>
          </div></div
      ></State>
    </section>
    <AABills />
    <CardManager />
    <details class="account-administration">
      <summary>模拟账户管理</summary>
      <AccountManager />
    </details>
    <ExecutionDialog ref="dialog" banking />
  </div>
</template>
<style scoped>
.execution-bank {
  display: flex;
  flex-direction: column;
  gap: 24px;
  color: #172033;
}
.execution-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.execution-heading h1 {
  font-size: 28px;
  letter-spacing: -0.5px;
  margin: 0;
}
.execution-heading p {
  font-size: 14px;
  color: #64748b;
  margin: 8px 0 0;
}
.simulation-badge {
  border: 1px solid #e7d6af;
  border-radius: 6px;
  background: #fffbf2;
  color: #946519;
  font-size: 13px;
  padding: 7px 10px;
  white-space: nowrap;
}
.execution-summary {
  display: flex;
  align-items: center;
  background: #fff;
  border: 1px solid #e7edf3;
  border-radius: 12px;
  padding: 20px 24px;
  gap: 32px;
}
.execution-summary > div {
  display: flex;
  align-items: center;
  gap: 16px;
}
.execution-summary span {
  font-size: 14px;
  color: #64748b;
}
.execution-summary b {
  font-size: 24px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.execution-summary p {
  font-size: 13px;
  color: #64748b;
  margin: 0 0 0 auto;
}
.execution-panel {
  background: white;
  border: 1px solid #e7edf3;
  border-radius: 12px;
  overflow: hidden;
  min-width: 0;
}
.execution-section {
  padding: 22px 24px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  border-bottom: 1px solid #e7edf3;
}
.execution-section h2 {
  font-size: 17px;
  margin: 0;
}
.execution-section p {
  font-size: 14px;
  color: #64748b;
  margin: 8px 0 0;
  line-height: 1.6;
  overflow-wrap: anywhere;
}
.pending-records {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 420px), 1fr));
  gap: 0 24px;
  padding: 0 24px;
}
.pending-records button {
  display: flex;
  align-items: center;
  gap: 14px;
  background: white;
  border: 0;
  border-bottom: 1px solid #e7edf3;
  padding: 18px 0;
  text-align: left;
  cursor: pointer;
  min-width: 0;
}
.pending-records button:hover,
.pending-records button.selected {
  background: #f3f9f7;
}
.pending-records button > div {
  flex: 1;
  display: grid;
  gap: 7px;
  min-width: 0;
}
.pending-records b {
  font-size: 14px;
  font-weight: 500;
  overflow-wrap: anywhere;
}
.pending-records span {
  font-size: 12px;
  color: #64748b;
}
.pending-records strong {
  font-size: 18px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.execution-workspace {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.12fr);
  gap: 24px;
  align-items: start;
}
.transfer-composer > form {
  padding: 8px 24px 24px;
}
.execution-bank label {
  font-size: 14px;
}
.execution-bank .form-columns {
  gap: 16px;
}
.execution-bank select {
  max-width: 100%;
}
.transfer-composer .form-columns.transfer-accounts {
  grid-template-columns: minmax(0, 1fr);
}
.transfer-composer .form-columns {
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
}
.transfer-composer .warning-note {
  font-size: 14px;
  line-height: 1.8;
  margin: 20px 0;
}
.rules-details {
  border-top: 1px solid #e7edf3;
  padding: 20px 24px;
  font-size: 14px;
}
.rules-details summary {
  cursor: pointer;
  color: #475569;
}
.rules-details .risk-rule {
  padding: 16px 0;
}
.rules-details .risk-rule p {
  font-size: 14px;
  line-height: 1.7;
}
.rules-details .risk-rule small {
  font-size: 13px;
}
.rules-details .risk-rule b {
  font-size: 14px;
}
.rules-details .row-between > span {
  font-size: 13px;
  color: #64748b;
}
.transaction-inspector :deep(.operation-inspection) {
  padding: 24px;
}
.history-filters {
  display: flex;
  gap: 12px;
  max-width: 450px;
  min-width: 0;
}
.history-filters input {
  min-width: 0;
}
.history-filters select {
  width: auto;
  flex: none;
}
.transaction-table .table-scroll table {
  min-width: 850px;
  width: 100%;
}
.transaction-table table td {
  font-size: 14px;
}
.transaction-table table th {
  font-size: 13px;
  font-weight: 500;
  background: #f7f9fb;
}
.transaction-table table td b {
  font-size: 14px;
  font-weight: 500;
}
.transaction-table table td small {
  font-size: 12px;
}
.transaction-table table .execution-amount {
  font-size: 16px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.transaction-table tr.selected {
  background: #f4faf8;
}
.operation-pagination {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 16px 24px;
  border-top: 1px solid #e7edf3;
}
.operation-pagination > span {
  font-size: 13px;
  color: #64748b;
}
.operation-pagination > div {
  display: flex;
  gap: 8px;
}
.audit-records {
  padding: 8px 24px 24px;
}
.bank-audit-row {
  display: flex;
  gap: 14px;
  padding: 18px 0;
  border-bottom: 1px solid #e7edf3;
}
.bank-audit-row > svg {
  color: #0d9488;
  flex: none;
  margin-top: 2px;
}
.bank-audit-row > div {
  flex: 1;
  min-width: 0;
}
.bank-audit-row > div > div {
  display: flex;
  gap: 12px;
  align-items: center;
  flex-wrap: wrap;
}
.bank-audit-row b {
  font-size: 14px;
  font-weight: 500;
  overflow-wrap: anywhere;
}
.bank-audit-row p {
  font-size: 13px;
  color: #475569;
  margin: 8px 0;
}
.bank-audit-row small {
  font-size: 12px;
  color: #64748b;
  overflow-wrap: anywhere;
}
.account-administration {
  font-size: 14px;
}
.account-administration > summary {
  cursor: pointer;
  color: #475569;
  padding: 8px 0;
}
.account-administration :deep(.panel) {
  margin-top: 16px;
  box-shadow: none;
}
.execution-bank :deep(pre) {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.pending-records button:focus-visible {
  outline: 2px solid #0d9488;
  outline-offset: 2px;
}
@media (min-width: 901px) {
  .transaction-inspector :deep(.operation-inspection) {
    max-height: 600px;
    overflow-y: auto;
    scrollbar-gutter: stable;
  }
}
@media (max-width: 1200px) {
  .execution-summary {
    gap: 24px;
    flex-wrap: wrap;
  }
  .execution-summary p {
    width: 100%;
    margin: 0;
  }
  .transfer-composer .transfer-accounts {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 900px) {
  .execution-workspace {
    grid-template-columns: 1fr;
  }
  .pending-records {
    grid-template-columns: 1fr;
  }
  .execution-summary p {
    line-height: 1.7;
  }
  .history-filters {
    max-width: 100%;
  }
  .transaction-table .execution-section {
    align-items: flex-start;
    flex-wrap: wrap;
  }
}
@media (max-width: 600px) {
  .execution-bank {
    gap: 20px;
  }
  .execution-heading {
    align-items: flex-start;
    flex-direction: column;
  }
  .execution-heading h1 {
    font-size: 25px;
    line-height: 1.4;
  }
  .execution-heading p {
    line-height: 1.7;
  }
  .execution-summary {
    padding: 16px;
    gap: 16px;
  }
  .execution-summary > div {
    gap: 8px;
  }
  .execution-summary b {
    font-size: 21px;
  }
  .execution-summary span {
    font-size: 13px;
  }
  .execution-section {
    padding: 20px 16px;
    gap: 12px;
  }
  .execution-section h2 {
    font-size: 16px;
  }
  .execution-section p {
    font-size: 13px;
  }
  .pending-records {
    padding: 0 16px;
  }
  .pending-records button {
    gap: 10px;
    flex-wrap: wrap;
  }
  .pending-records button > div {
    min-width: 100%;
    gap: 5px;
  }
  .pending-records strong {
    margin-right: auto;
  }
  .transfer-composer > form {
    padding: 4px 16px 20px;
  }
  .transfer-composer .form-columns {
    grid-template-columns: 1fr;
  }
  .rules-details {
    padding: 20px 16px;
  }
  .transaction-inspector :deep(.operation-inspection) {
    padding: 20px 16px;
  }
  .history-filters {
    width: 100%;
    flex-wrap: wrap;
  }
  .history-filters input {
    flex: 1 1 100%;
  }
  .history-filters select {
    width: 100%;
  }
  .operation-pagination {
    padding: 16px;
    align-items: flex-start;
    flex-direction: column;
  }
  .audit-records {
    padding: 8px 16px 20px;
  }
}
</style>
