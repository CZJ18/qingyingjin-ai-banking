<script setup lang="ts">
import { computed } from 'vue'
import type { Schema } from '@/api/client'
import { money } from '@/lib/utils'
import { goalPace, selectedExpenseActions } from '@/lib/expense-goal-actions'
import { analysisPeriodLabel } from '@/lib/queries'
import { categoryLabel } from '@/lib/categories'

const props = defineProps<{ preview: Schema<'ExpensePreview'>; adjustableCategories: string[]; report?: Schema<'BillingReport'> | null }>()
const pace = computed(() => goalPace(props.preview.target_period, props.preview.target_minor))
const actions = computed(() => selectedExpenseActions(props.adjustableCategories))
const missingHistory = computed(() => props.preview.result.missing_information.some(
  item => ['budget', 'complete_historical_month_billing_report'].includes(item)))
const baselineMonth=computed(()=>{
 const value=analysisPeriodLabel(props.preview.baseline_period)
 return /^[1-9]\d{3}-(0[1-9]|1[0-2])$/.test(value)?value:undefined
})
const checkedReport=computed(()=>props.report?.report_id===props.preview.billing_report_id ? props.report : null)
function observed(category:string){return checkedReport.value?.category_breakdown.find(row=>row.category===category)}
</script>

<template>
  <section v-if="preview.source === 'deterministic_lifestyle_rules' && preview.result.status === 'needs_information' && pace"
    class="goal-next-steps" aria-label="资料不足时的行动建议">
    <h4>先试行 7 天，边做边补齐依据</h4>
    <p>不必等所有资料齐全才开始记录和尝试，但现在不能确认能省下 {{ money(preview.target_minor) }}。</p>
    <div class="goal-pace">
      <strong>目标节奏</strong>
      <p v-if="pace.dailyMinor > 0 && pace.weeklyMinor > 0">
        {{ preview.target_period }} 共 {{ pace.days }} 天；少花 {{ money(preview.target_minor) }}，
        相当于平均每天约 {{ money(pace.dailyMinor) }}、每 7 天约 {{ money(pace.weeklyMinor) }}。
      </p>
      <p v-else>按月记录目标 {{ money(preview.target_minor) }}，不把小额目标拆成零元日指标。</p>
      <p class="pace-note">以上仅是目标拆解，不是已核实能省下的金额，也不是每日消费上限或已经存下的钱。必要开支优先。</p>
    </div>
    <ol v-if="actions.length">
      <li v-for="action in actions" :key="action.category">
        <strong>{{ action.title }}</strong>
        <p v-if="!checkedReport" class="action-check">账单依据尚未核对，暂不展示品类金额。</p>
        <p v-else-if="observed(action.category)" class="action-check">这份账单已记录{{categoryLabel(action.category)}} {{money(observed(action.category)!.expense_minor)}}，{{observed(action.category)!.transaction_ids.length}} 笔；记录金额不是可节省额度。</p>
        <p v-else class="action-check">这份账单没有这类记录，金额仍待核实，不代表零支出。</p>
        <p>{{ action.action }}</p>
        <p class="action-check">怎么核对：{{ action.check }}</p>
        <RouterLink v-if="checkedReport" :to="{path:'/billing',query:{report_id:preview.billing_report_id,category:action.category}}">核对{{categoryLabel(action.category)}}的原始账单 →</RouterLink>
      </li>
    </ol>
    <p v-else>先在上方选择你愿意调整的一类非必要开支，再查看对应的试行方法；系统不会替你选择削减类别。</p>
    <div class="goal-review">
      <strong>第 7 天复盘</strong>
      <p>记录这 7 天各类实际花费和主动放弃的非必要购买，检查是否产生了其他开支；不能确认的减少金额先标为待核实，不计入可用余额。</p>
      <p v-if="missingHistory">历史账单覆盖范围尚未核实。请补充并核对已结束完整月份的账单范围；仅选择月份或导入 CSV 不能证明账单已收全。当前可以继续记录和进行 7 天试行。</p>
      <RouterLink v-if="missingHistory" :to="{path:'/billing',query:{import:'1',month:baselineMonth}}">去补充账单并核对覆盖范围 →</RouterLink>
      <p v-else>选定愿意调整的类别后，重新测算并比较基于账单的参考方案。</p>
    </div>
    <p class="pace-note">这些是规则生成的试行建议，不是新的模型回答；不会修改预算、取消订阅或执行资金操作。</p>
  </section>
</template>

<style scoped>
.goal-next-steps{margin-top:10px;line-height:1.75}.goal-next-steps h4{font-size:15px;margin:0 0 8px;color:#142d4e}.goal-next-steps p{margin:6px 0}.goal-pace,.goal-review{border:1px solid #dbe4ea;border-radius:9px;padding:12px 14px;margin:12px 0}.goal-pace{background:#f2f8f7}.goal-next-steps ol{padding-left:22px}.goal-next-steps li{padding:8px 0}.pace-note,.action-check{color:#52657b;font-size:13px}
</style>
