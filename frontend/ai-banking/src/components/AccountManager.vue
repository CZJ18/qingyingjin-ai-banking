<script setup lang="ts">
import { ref } from 'vue'
import { Wallet, Plus } from 'lucide-vue-next'
import { useAccounts } from '@/lib/queries'
import { useSession, useUI } from '@/lib/store'
import { money, minor } from '@/lib/utils'
import type { Schema } from '@/api/client'
import State from './State.vue'
import Status from './Status.vue'
import Button from './ui/button/Button.vue'
import ExecutionDialog from './ExecutionDialog.vue'
const accounts = useAccounts(),
  s = useSession(),
  ui = useUI(),
  open = ref(false),
  name = ref(''),
  opening = ref('0'),
  dialog = ref<InstanceType<typeof ExecutionDialog>>()
async function create() {
  try {
    const parameters: Schema<'ExecutionOpenAccountParameters'> = {
      display_name: name.value,
      account_type: 'checking',
      initial_balance_minor: minor(opening.value),
      confirm_simulated_funds: true,
      currency: 'CNY',
    }
    await dialog.value?.prepare('open_account', parameters)
  } catch (e) {
    ui.notify((e as Error).message)
  }
}
</script>
<template>
  <section class="panel account-manager">
    <div class="section-heading">
      <h2>账户管理</h2>
      <Button variant="outline" @click="open = !open"><Plus :size="14" />创建模拟账户</Button>
    </div>
    <form v-if="open" class="filter-bar" @submit.prevent="create">
      <label class="grow"
        >账户名称<input v-model="name" required placeholder="我的日常生活账户" /></label
      ><label>模拟期初余额 / 元<input v-model="opening" required inputmode="decimal" /></label
      ><Button type="submit" :disabled="!s.connected">创建开户意向</Button>
    </form>
    <State
      :connected="s.connected"
      :loading="accounts.isLoading.value"
      :error="accounts.error.value"
      :empty="!accounts.data.value?.length"
      @retry="accounts.refetch()"
      ><div class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>账户名称</th>
              <th>脱敏账号</th>
              <th>余额</th>
              <th>状态</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="a in accounts.data.value" :key="a.id">
              <td>{{ a.display_name }}</td>
              <td>{{ a.masked_account_number }}</td>
              <td>{{ money(a.balance_minor) }}</td>
              <td><Status :value="a.status" /></td>
              <td>
                <Button
                  variant="ghost"
                  :disabled="!['active', 'frozen'].includes(a.status)"
                  @click="
                    dialog?.prepare('account_status', {
                      account_id: a.id,
                      expected_version: a.version,
                      status: a.status === 'frozen' ? 'active' : 'frozen',
                    })
                  "
                  >{{ a.status === 'frozen' ? '申请解冻' : '申请冻结' }}</Button
                >
              </td>
            </tr>
          </tbody>
        </table>
      </div></State
    ><ExecutionDialog ref="dialog" />
  </section>
</template>
