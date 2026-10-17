<script setup lang="ts">
import { ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import { request, type Schema } from '@/api/client'
import { minor } from '@/lib/utils'
import { useUI } from '@/lib/store'
import Button from './ui/button/Button.vue'
const props = defineProps<{ goal: Schema<'SavingsGoal'> }>(),
  target = ref(''),
  monthly = ref(''),
  ends = ref(''),
  busy = ref(false),
  ui = useUI(),
  qc = useQueryClient()
watch(
  () => props.goal,
  (g) => {
    target.value = String(g.target_minor / 100)
    monthly.value = String(g.plan?.monthly_minor / 100)
    ends.value = new Intl.DateTimeFormat('sv-SE', {
      timeZone: 'Asia/Shanghai',
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    }).format(new Date(g.ends_at))
  },
  { immediate: true },
)
async function save() {
  if (busy.value) return
  busy.value = true
  try {
    const g = props.goal
    const body: Schema<'PlanningGoalUpdateInput'> = {
      expected_target_minor: g.target_minor,
      expected_monthly_minor: g.plan.monthly_minor,
      expected_ends_at: g.ends_at,
      target_minor: minor(target.value),
      monthly_minor: minor(monthly.value),
      ends_at: Date.parse(ends.value + 'T23:59:59+08:00'),
    }
    await request(`/api/v1/savings/goals/${g.id}/settings`, { method: 'PUT', body })
    await qc.invalidateQueries({ queryKey: ['savings'] })
    ui.notify('目标金计划已更新，资金余额未变更')
  } catch (e) {
    ui.notify((e as Error).message)
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <details class="goal-settings">
    <summary>修改目标金额与储蓄计划</summary>
    <form @submit.prevent="save">
      <div class="form-columns">
        <label>目标金额 / 元<input v-model="target" required inputmode="decimal" /></label
        ><label>每月储蓄计划 / 元<input v-model="monthly" required inputmode="decimal" /></label
        ><label>截止日期<input v-model="ends" type="date" required /></label>
      </div>
      <Button type="submit" :disabled="busy">{{ busy ? '保存中…' : '保存计划修改' }}</Button>
    </form>
  </details>
</template>
