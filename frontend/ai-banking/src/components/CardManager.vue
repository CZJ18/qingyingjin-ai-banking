<script setup lang="ts">
import { computed, ref } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { CreditCard, Plus, Star } from 'lucide-vue-next'
import { request, type Schema } from '@/api/client'
import { useAccounts } from '@/lib/queries'
import { useSession, useUI } from '@/lib/store'
import { money, minor } from '@/lib/utils'
import State from './State.vue'
import Button from './ui/button/Button.vue'
import ExecutionDialog from './ExecutionDialog.vue'

const session = useSession(),
  ui = useUI(),
  accounts = useAccounts()
const cards = useQuery({
  queryKey: computed(() => ['cards', session.sessionEpoch]),
  queryFn: ({ signal }) => request<Schema<'BankCard'>[]>('/api/v1/cards', { signal }),
  enabled: computed(() => session.connected),
})
const eligible = computed(() => accounts.data.value?.filter((a) => a.status === 'active') || [])
const adding = ref(false),
  busy = ref(false),
  account = ref(''),
  issuer = ref(''),
  name = ref(''),
  lastFour = ref(''),
  type = ref<'debit' | 'credit'>('debit'),
  limit = ref('0')
const dialog = ref<InstanceType<typeof ExecutionDialog>>()
async function prepare(input: Schema<'ExecutionCardManageParameters'>) {
  if (busy.value || !session.connected) return
  busy.value = true
  try {
    await dialog.value?.prepare('card_manage', input)
  } catch (error) {
    ui.notify((error as Error).message)
  } finally {
    busy.value = false
  }
}
async function bind() {
  try {
    await prepare({
      action: 'bind',
      account_id: account.value,
      issuer: issuer.value,
      name: name.value,
      last_four: lastFour.value,
      card_type: type.value,
      credit_limit_minor: type.value === 'credit' ? minor(limit.value) : 0,
    })
  } catch (error) {
    ui.notify((error as Error).message)
  }
}
const accountName = (id: string) =>
  accounts.data.value?.find((a) => a.id === id)?.display_name || id
</script>

<template>
  <section class="panel card-manager" aria-labelledby="cards-title">
    <div class="section-heading">
      <div>
        <h2 id="cards-title"><CreditCard :size="19" />我的银行卡</h2>
        <p class="card-note">模拟卡片 · 关联账户管理，操作需本人确认</p>
      </div>
      <Button variant="outline" :disabled="!session.connected" @click="adding = !adding"
        ><Plus :size="14" />绑定模拟卡</Button
      >
    </div>
    <form v-if="adding" class="card-form" @submit.prevent="bind">
      <label
        >关联模拟账户<select v-model="account" aria-label="关联模拟账户" required>
          <option disabled value="">请选择本人账户</option>
          <option v-for="a in eligible" :key="a.id" :value="a.id">
            {{ a.display_name }} · {{ a.masked_account_number }}
          </option>
        </select></label
      >
      <label
        >卡片名称<input v-model="name" required maxlength="32" placeholder="例如：日常生活卡"
      /></label>
      <label
        >发行机构（模拟）<input
          v-model="issuer"
          required
          maxlength="32"
          placeholder="填写模拟发卡行"
      /></label>
      <label
        >卡片类型<select v-model="type" aria-label="卡片类型">
          <option value="debit">借记卡</option>
          <option value="credit">信用卡</option>
        </select></label
      >
      <label
        >模拟尾号（仅四位）<input
          v-model="lastFour"
          required
          inputmode="numeric"
          pattern="[0-9]{4}"
          minlength="4"
          maxlength="4"
          autocomplete="off"
          placeholder="1234"
      /></label>
      <label v-if="type === 'credit'"
        >模拟信用额度 / 元<input v-model="limit" required inputmode="decimal"
      /></label>
      <p class="card-note form-note">
        仅填写四位模拟尾号，不要填写完整卡号、有效期或安全码。新卡不会增加账户余额。
      </p>
      <p v-if="!eligible.length" class="card-note form-note">
        需要先创建本人可用的模拟账户；账户加载失败时请刷新账户管理。
      </p>
      <Button type="submit" :disabled="busy || !eligible.length">创建绑定意向</Button>
    </form>
    <State
      :connected="session.connected"
      :loading="cards.isLoading.value"
      :error="cards.error.value"
      :empty="!cards.data.value?.length"
      @retry="cards.refetch()"
    >
      <div class="card-grid">
        <article
          v-for="card in cards.data.value"
          :key="card.id"
          class="bank-card"
          :class="{ unbound: card.status === 'unbound' }"
        >
          <header>
            <span>{{ card.issuer }}</span
            ><span class="card-tag"
              >{{ card.card_type === 'credit' ? '信用卡' : '借记卡' }} · 模拟</span
            >
          </header>
          <h3>{{ card.name }}</h3>
          <p class="card-number">
            SIM <span aria-label="尾号">•••• {{ card.last_four }}</span>
          </p>
          <p class="card-account">关联：{{ accountName(card.account_id) }}</p>
          <dl v-if="card.card_type === 'credit'" class="credit-summary">
            <div>
              <dt>信用额度</dt>
              <dd>{{ money(card.credit_limit_minor) }}</dd>
            </div>
            <div>
              <dt>已用额度</dt>
              <dd>{{ money(card.credit_used_minor) }}</dd>
            </div>
            <div>
              <dt>可用额度</dt>
              <dd>{{ money(card.credit_available_minor) }}</dd>
            </div>
          </dl>
          <p v-if="card.card_type === 'credit'" class="card-note">
            额度不是现金余额，尚未接入信用卡消费与还款。
          </p>
          <footer>
            <span v-if="card.status === 'unbound'">已解绑 · 保留历史</span
            ><span v-else-if="card.is_default" class="default-label"><Star :size="13" />默认卡</span
            ><span v-else>已绑定</span>
            <div v-if="card.status === 'active'" class="card-actions">
              <Button
                v-if="!card.is_default"
                variant="ghost"
                :disabled="busy"
                @click="prepare({ action: 'set_default', card_id: card.id })"
                >设为默认</Button
              ><Button
                variant="ghost"
                :disabled="busy"
                @click="prepare({ action: 'unbind', card_id: card.id })"
                >解绑</Button
              >
            </div>
          </footer>
        </article>
      </div>
    </State>
    <p class="card-note">默认卡仅表示管理偏好，不授予自动扣款权限。解绑不关闭账户、不转移资金。</p>
    <ExecutionDialog ref="dialog" />
  </section>
</template>

<style scoped>
.card-manager h2 {
  display: flex;
  align-items: center;
  gap: 8px;
}
.card-note {
  font-size: 12px;
  color: var(--text-muted, #64748b);
  line-height: 1.7;
  margin-top: 8px;
}
.card-form {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
  gap: 14px;
  padding: 20px 0;
}
.card-form label {
  display: flex;
  flex-direction: column;
  gap: 7px;
  font-size: 13px;
}
.form-note {
  grid-column: 1/-1;
}
.card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 300px), 1fr));
  gap: 18px;
  margin: 20px 0;
}
.bank-card {
  padding: 22px;
  border: 1px solid var(--border, #cbd5e1);
  border-radius: 14px;
  background: var(--surface, #f8fafc);
  min-width: 0;
}
.bank-card header,
.bank-card footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
}
.bank-card header {
  font-size: 13px;
}
.card-tag {
  font-size: 11px;
  color: #64748b;
}
.bank-card h3 {
  font-size: 18px;
  margin: 20px 0 12px;
}
.card-number {
  font-size: 12px;
  color: #64748b;
  letter-spacing: 1px;
}
.card-number span {
  font-size: 22px;
  color: var(--text, #183e37);
  margin-left: 16px;
  font-variant-numeric: tabular-nums;
}
.card-account {
  font-size: 12px;
  margin: 12px 0;
  overflow-wrap: anywhere;
}
.bank-card footer {
  border-top: 1px solid var(--border, #cbd5e1);
  padding-top: 12px;
  margin-top: 16px;
  font-size: 12px;
}
.card-actions,
.default-label {
  display: flex;
  align-items: center;
  gap: 6px;
}
.default-label {
  color: #137a58;
}
.credit-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 18px;
  margin: 16px 0;
}
.credit-summary dt {
  font-size: 11px;
  color: #64748b;
}
.credit-summary dd {
  margin: 5px 0 0;
  font-size: 14px;
  font-variant-numeric: tabular-nums;
}
.unbound {
  opacity: 0.65;
}
</style>
