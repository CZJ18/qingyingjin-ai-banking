<script setup lang="ts">
import { computed } from 'vue'
import type { Schema } from '@/api/client'
import { money, date } from '@/lib/utils'
import Status from '@/components/Status.vue'
import JsonDetails from '@/components/JsonDetails.vue'
import Button from '@/components/ui/button/Button.vue'
const props = defineProps<{ view: Schema<'ExecutionView'> }>()
defineEmits<{ confirm: [] }>()
const op = computed(() => props.view.operation)
const risk: Record<string, string> = {
  low: '低风险',
  medium: '中风险',
  high: '高风险',
  critical: '严重风险',
}
const kinds: Record<string, string> = {
  transfer: '模拟银行转账',
  savings_deposit: '存入目标金',
  savings_withdrawal: '取出目标金',
  cancel_subscription: '取消模拟订阅',
  create_savings_goal: '创建目标金',
  open_account: '创建模拟账户',
}
const decisions: Record<string, string> = {
  allow: '允许通过',
  deny: '拒绝',
  reject: '拒绝',
  block: '拦截',
  require_confirmation: '需要用户确认',
  approve: '同意授权',
}
const record = (v: unknown): Record<string, unknown> =>
  v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : {}
const source = computed(() => record(op.value.snapshot?.source_account)),
  target = computed(() => record(op.value.snapshot?.target_account))
const purpose = computed(
  () =>
    op.value.parameters.description ||
    record(op.value.parameters.input).description ||
    kinds[op.value.kind] ||
    op.value.kind,
)
</script>
<template>
  <div class="operation-inspection" tabindex="0" aria-label="交易核对与追踪，可滚动查看">
    <header>
      <div>
        <span>{{ kinds[op.kind] || op.kind }} · 模拟资金</span>
        <h3>{{ money(op.amount_minor) }}</h3>
      </div>
      <Status :value="op.status" />
    </header>
    <div class="account-route">
      <div>
        <span>付款账户</span
        ><b>{{
          source.display_name || (op.source_account_id === '0' ? '不适用' : op.source_account_id)
        }}</b
        ><small>{{ source.masked_account_number || '#' + op.source_account_id }}</small>
      </div>
      <span class="route-arrow">→</span>
      <div>
        <span>收款账户 / 关联资源</span
        ><b>{{
          target.display_name ||
          (op.target_account_id === '0' ? op.resource_id : op.target_account_id)
        }}</b
        ><small>{{
          target.masked_account_number ||
          '#' + (op.target_account_id === '0' ? op.resource_id : op.target_account_id)
        }}</small>
      </div>
    </div>
    <dl>
      <div>
        <dt>用途</dt>
        <dd>{{ purpose }}</dd>
      </div>
      <div>
        <dt>操作编号</dt>
        <dd>{{ op.id }}</dd>
      </div>
      <div>
        <dt>风险等级 / 版本</dt>
        <dd>{{ risk[op.risk_level] || op.risk_level || '尚未返回' }} · v{{ op.version }}</dd>
      </div>
      <div>
        <dt>意向有效期</dt>
        <dd>{{ date(op.expires_at) }}</dd>
      </div>
    </dl>
    <p v-if="op.error_code" class="operation-error" role="alert">服务端错误：{{ op.error_code }}</p>
    <section>
      <h4>风险检查结果</h4>
      <p v-if="!view.risk_events?.length" class="inspection-empty">
        尚无风险检查记录，不能据此认定交易安全。
      </p>
      <div
        v-for="event in view.risk_events"
        :key="event.id"
        class="risk-inspection"
        :data-risk="event.risk_level"
      >
        <div>
          <b>{{ event.rule_name }}</b
          ><span
            >{{ decisions[event.decision] || event.decision }} ·
            {{ risk[event.risk_level] || event.risk_level }}</span
          >
        </div>
        <p v-if="event.error_code">{{ event.error_code }}</p>
        <JsonDetails :value="event.evidence" label="查看核验证据" />
      </div>
    </section>
    <section>
      <h4>授权与执行追踪</h4>
      <ol class="operation-tracking">
        <li>
          <i />
          <div>
            <b>操作意向已创建</b><time>{{ date(op.created_at) }}</time>
          </div>
        </li>
        <li v-for="auth in view.authorizations" :key="auth.id">
          <i />
          <div>
            <b>用户授权 · {{ decisions[auth.decision] || auth.decision }}</b
            ><time>{{ date(auth.created_at) }}</time
            ><small>{{
              auth.consumed_at ? '授权已被使用 · ' + date(auth.consumed_at) : '授权尚未使用'
            }}</small>
          </div>
        </li>
        <li class="current">
          <i />
          <div>
            <b>当前服务器状态</b><Status :value="op.status" /><time>{{ date(op.updated_at) }}</time>
          </div>
        </li>
      </ol>
      <p v-if="!view.authorizations?.length" class="inspection-empty">尚无用户授权记录。</p>
    </section>
    <JsonDetails :value="op.snapshot" label="账户核验与资源版本快照" /><JsonDetails
      :value="op.parameters"
      label="完整操作参数"
    /><JsonDetails
      v-if="['executed', 'succeeded'].includes(op.status)"
      :value="op.result"
      label="服务器最终执行结果"
    />
    <footer>
      <span>仅使用模拟账户与模拟资金。</span
      ><Button @click="$emit('confirm')">{{
        ['pending_authorization', 'authorized'].includes(op.status) ? '核对并授权' : '查看最终状态'
      }}</Button>
    </footer>
  </div>
</template>
<style scoped>
.operation-inspection {
  font-size: 14px;
  color: #172033;
}
.operation-inspection > header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}
.operation-inspection header span {
  font-size: 13px;
  color: #64748b;
}
.operation-inspection h3 {
  font-size: 32px;
  font-weight: 600;
  letter-spacing: -0.8px;
  margin: 12px 0 24px;
  font-variant-numeric: tabular-nums;
}
.account-route {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr);
  gap: 16px;
  background: #f6f8fb;
  padding: 18px;
  border-radius: 10px;
}
.account-route > div {
  display: grid;
  gap: 8px;
}
.account-route span {
  font-size: 13px;
  color: #64748b;
}
.account-route b {
  font-size: 14px;
  font-weight: 500;
  overflow-wrap: anywhere;
}
.account-route small {
  font-size: 13px;
  color: #64748b;
  overflow-wrap: anywhere;
}
.route-arrow {
  align-self: center;
}
.operation-inspection dl {
  margin: 20px 0;
}
.operation-inspection dl > div {
  display: grid;
  grid-template-columns: 120px minmax(0, 1fr);
  gap: 12px;
  padding: 12px 0;
  border-bottom: 1px solid #edf1f5;
  line-height: 1.6;
}
.operation-inspection dt {
  color: #64748b;
}
.operation-inspection dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.operation-inspection section {
  margin-top: 24px;
}
.operation-inspection h4 {
  font-size: 15px;
  font-weight: 600;
  margin: 0 0 14px;
}
.risk-inspection {
  padding: 14px 0;
  border-top: 1px solid #e7edf3;
}
.risk-inspection > div {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.risk-inspection b {
  font-weight: 500;
}
.risk-inspection span {
  color: #64748b;
  font-size: 13px;
}
.risk-inspection[data-risk='high'] span {
  color: #b45309;
}
.risk-inspection[data-risk='critical'] span {
  color: #b91c1c;
}
.risk-inspection p,
.operation-error {
  color: #b91c1c;
  line-height: 1.7;
  font-size: 14px;
}
.inspection-empty {
  font-size: 14px;
  line-height: 1.7;
  color: #64748b;
}
.operation-tracking {
  padding: 0;
  list-style: none;
  margin: 0;
}
.operation-tracking li {
  display: flex;
  gap: 12px;
  padding: 12px 0;
}
.operation-tracking i {
  width: 7px;
  height: 7px;
  background: #94a3b8;
  border-radius: 50%;
  flex: none;
  margin-top: 6px;
}
.operation-tracking .current i {
  background: #0d9488;
}
.operation-tracking li > div {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  min-width: 0;
}
.operation-tracking b {
  font-weight: 500;
}
.operation-tracking time,
.operation-tracking small {
  font-size: 13px;
  color: #64748b;
}
.operation-tracking small {
  width: 100%;
}
.operation-inspection > footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  margin-top: 24px;
  padding-top: 20px;
  border-top: 1px solid #e7edf3;
}
.operation-inspection footer > span {
  font-size: 13px;
  color: #64748b;
}
@media (max-width: 600px) {
  .account-route {
    grid-template-columns: 1fr;
    gap: 12px;
  }
  .route-arrow {
    display: none;
  }
  .operation-inspection dl > div {
    grid-template-columns: 100px minmax(0, 1fr);
  }
  .operation-inspection > footer {
    flex-direction: column;
    align-items: stretch;
  }
  .operation-inspection h3 {
    font-size: 30px;
  }
}
</style>
