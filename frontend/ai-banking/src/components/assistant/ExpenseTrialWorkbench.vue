<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { request, type Schema } from '@/api/client'
import { useSession } from '@/lib/store'
import { createStableActions } from '@/lib/product-actions'
import { selectedExpenseActions, expenseOptionIsExtreme } from '@/lib/expense-goal-actions'
import { categoryLabel } from '@/lib/categories'
import { money, date } from '@/lib/utils'
import Button from '@/components/ui/button/Button.vue'

const props=withDefaults(defineProps<{preview:Schema<'ExpensePreview'>|null;adjustableCategories:string[];canStart?:boolean}>(),{canStart:true})
const session=useSession(),chosen=ref(''),busy=ref(false),error=ref(''),offset=ref(0)
const notes=ref<Record<string,string>>({})
const selectedOption=ref('')
let alive=true,actions=createStableActions()
onBeforeUnmount(()=>{alive=false})
const context=()=>`${session.sessionEpoch}:${session.user?.id}:${session.accountId}`
watch(context,()=>{chosen.value='';notes.value={};busy.value=false;error.value='';offset.value=0;actions=createStableActions()})
watch(()=>props.preview?.billing_report_id,()=>{chosen.value='';selectedOption.value='';error.value=''})
const choices=computed(()=>selectedExpenseActions(props.adjustableCategories))
const checkedPlan=computed(()=>props.preview?.baseline_status==='user_reconciled'&&!!props.preview.statement_check)
const option=computed(()=>props.preview?.result.options.find(value=>value.name===selectedOption.value))
const categoryCut=computed(()=>{
 const row=option.value?.categories.find(value=>value.category===chosen.value)
 return row?row.original_minor-row.proposed_minor:0
})
const source=useQuery({
 queryKey:computed(()=>['expense-trial-source',session.sessionEpoch,props.preview?.billing_report_id,session.accountId]),
 enabled:computed(()=>session.connected&&!!props.preview&&!!session.accountId),
 queryFn:async({signal})=>{
  const report=await request<Schema<'BillingReport'>>('/api/v1/billing/reports/'+props.preview!.billing_report_id,{signal})
  if(report.report_id!==props.preview?.billing_report_id||report.user_id!==session.user?.id||report.accounts.length!==1||report.accounts[0]?.id!==session.accountId)
   throw new Error('账单依据与当前用户或账户不一致，请重新测算')
  return report
 },
})
const history=useQuery({
 queryKey:computed(()=>['expense-trials',session.sessionEpoch,session.accountId,offset.value]),
 enabled:computed(()=>session.connected&&!!session.accountId),
 queryFn:({signal})=>request<Schema<'ExpenseTrialView'>[]>(`/api/v2/expense-trials?account_id=${session.accountId}&limit=10&offset=${offset.value}`,{signal}),
})
function sourceCategory(category:string){return source.data.value?.category_breakdown.find(row=>row.category===category)}
function reportLink(reportId:string,category:string){return {path:'/billing',query:{report_id:reportId,category}}}
async function start(){
 if(!props.canStart){error.value='请先重新测算，核对最新依据后再开始新的试行';return}
 if(busy.value||!props.preview||!source.data.value||!choices.value.some(action=>action.category===chosen.value))return
 if(checkedPlan.value&&(!option.value||categoryCut.value<=0||expenseOptionIsExtreme(option.value.categories))){error.value='请选择非清零的参考方案及其中有可调整记录的类别';return}
 const own=context();busy.value=true;error.value=''
 const body:Schema<'ExpenseTrialInput'>={account_id:session.accountId,billing_report_id:props.preview.billing_report_id,
  target_period:props.preview.target_period,target_minor:props.preview.target_minor,category:chosen.value as Schema<'ExpenseTrialInput'>['category'],
  ...(checkedPlan.value?{statement_check:props.preview.statement_check!,expected_reduction_minor:categoryCut.value}:{})}
 try{
  await actions.run('expense-trial-start:'+own,body,key=>request<Schema<'ExpenseTrialView'>>('/api/v2/expense-trials',{method:'POST',body,key}))
  if(!alive||own!==context())return
  offset.value=0;chosen.value='';await history.refetch()
 }catch(e){if(alive&&own===context())error.value=(e as Error).message+'；若保存结果不确定，请先刷新试行列表核对。'}
 finally{if(alive&&own===context())busy.value=false}
}
async function review(view:Schema<'ExpenseTrialView'>){
 if(busy.value)return
 const own=context();busy.value=true;error.value=''
 const body:Schema<'ExpenseTrialReviewInput'>={expected_version:view.trial.version,note:notes.value[view.trial.id]??view.review?.note??''}
 try{
  await actions.run('expense-trial-review:'+view.trial.id+':'+own,body,key=>request<Schema<'ExpenseTrialView'>>('/api/v2/expense-trials/'+view.trial.id+'/reviews',{method:'POST',body,key}))
  if(!alive||own!==context())return
  delete notes.value[view.trial.id];await history.refetch()
 }catch(e){if(alive&&own===context())error.value=(e as Error).message+'；请重新查询记录后再决定是否保存。'}
 finally{if(alive&&own===context())busy.value=false}
}
</script>

<template>
 <section v-if="session.connected&&session.accountId" class="expense-trials" aria-label="我的7天试行">
  <h3>我的 7 天试行</h3>
  <p>选一项愿意尝试的行动，保存到当前账户；刷新或重新登录后可以继续查看和复盘。</p>
  <div v-if="preview" class="trial-create">
   <h4>选一项，开始记录</h4>
   <label v-if="checkedPlan">本次参考方案<select v-model="selectedOption" aria-label="试行参考方案"><option value="">请选择，不自动替你决定</option><option v-for="plan in preview.result.options" :key="plan.name" :value="plan.name" :disabled="expenseOptionIsExtreme(plan.categories)">{{expenseOptionIsExtreme(plan.categories)?'清零上界，仅供参考':({conservative:'保守尝试',balanced:'平衡调整',active:'积极调整'} as Record<string,string>)[plan.name]||plan.name}} · 整体月度少花参考 {{money(plan.expense_reduction_minor)}}</option></select></label>
   <p v-if="checkedPlan&&preview.result.options.some(plan=>expenseOptionIsExtreme(plan.categories))" class="trial-note">要求某类开支全部清零的上界不列为试行建议，原目标和缺口保留。</p>
   <p v-if="source.isPending.value">正在核对实际账单依据…</p>
   <p v-else-if="source.error.value" role="alert">账单依据暂时无法核实，请重新测算后再保存。</p>
   <fieldset v-else-if="source.data.value&&choices.length" :disabled="busy">
    <legend>本次只试一类开支，不自动削减其他类别</legend>
    <label v-for="action in choices" :key="action.category" class="trial-choice">
     <input v-model="chosen" type="radio" name="expense-trial-category" :value="action.category" :aria-label="'试行'+categoryLabel(action.category)" />
     <span><strong>{{action.title}}</strong><span>{{action.action}}</span>
      <small v-if="sourceCategory(action.category)">参考账单已记录{{categoryLabel(action.category)}} {{money(sourceCategory(action.category)!.expense_minor)}}，{{sourceCategory(action.category)!.transaction_ids.length}} 笔。历史支出不等于可削减额度。</small>
      <small v-else>参考账单没有该类别记录，无法核实历史金额。</small>
      <RouterLink :to="reportLink(preview.billing_report_id,action.category)">查看这类账单依据 →</RouterLink>
     </span>
    </label>
   </fieldset>
   <p v-else-if="!choices.length">请先在上方勾选愿意调整的类别。</p>
   <p v-if="checkedPlan&&chosen&&option">该类本次月度少花参考 {{money(categoryCut)}}；保存后可查看原始依据和复盘。</p>
   <Button :disabled="!canStart||busy||!chosen||!source.data.value||(checkedPlan&&(!option||categoryCut<=0||expenseOptionIsExtreme(option.categories)))" @click="start">{{busy?'正在保存…':'开始7天试行'}}</Button>
   <p class="trial-note">只保存行动和账单范围，不改预算、不取消订阅、不转账。</p>
  </div>
  <p v-if="error" role="alert">{{error}}</p>
  <p v-if="history.isPending.value">正在读取已保存的试行…</p>
  <div v-else-if="history.error.value" role="alert">试行记录暂时无法读取。<button @click="history.refetch()">重新查询</button></div>
  <p v-else-if="!history.data.value?.length">还没有保存的试行。先测算目标，再选择一项开始。</p>
  <article v-for="view in history.data.value||[]" :key="view.trial.id" class="trial-record" :aria-label="'试行记录'+view.trial.id">
   <header><h4>{{categoryLabel(view.trial.category)}} · {{view.progress.window_complete?'7天已到，核对观察结果':'试行进行中'}}</h4><span>目标月份 {{view.trial.target_period}} · 少花目标 {{money(view.trial.target_minor)}}</span></header>
   <p><strong>本次试行：</strong>{{selectedExpenseActions([view.trial.category])[0]?.action}}</p>
   <p>开始 {{date(view.trial.started_at)}} · 复盘日 {{date(view.trial.ends_at)}}（北京时间）</p>
   <p>参考账单该类已记录 {{money(view.baseline.expense_minor)}} · {{view.baseline.transaction_ids.length}} 笔<span v-if="view.plan"> · 用户已核对汇总，非银行认证</span><span v-else-if="!view.baseline_complete"> · 月份依据不完整</span></p>
   <p v-if="view.plan">保存时的月度参考：本类预计少花 {{money(view.plan.expected_reduction_minor)}}，月度参考上限 {{money(view.plan.monthly_limit_minor)}}。这是计划，不是已实现节省。</p>
   <RouterLink :to="reportLink(view.trial.billing_report_id,view.trial.category)">查看保存时的账单依据 →</RouterLink>
   <div class="trial-progress">
    <strong>本次观察区间已记录</strong>
    <p>{{categoryLabel(view.trial.category)}}支出 {{money(view.progress.category_expense_minor)}}，该账户全部支出 {{money(view.progress.total_expense_minor)}}。</p>
    <p>节省金额待核实。没有记录到消费不等于没有花钱；参考月与观察区间长度不同，不能直接相减。</p>
   </div>
   <section v-if="view.comparison" aria-label="同长度七天参考对照">
    <h4>同长度七天参考对照</h4>
    <p v-if="view.comparison.status==='source_changed'">参考来源记录已缺失或与快照不一致，暂不计算差额；原试行和复盘记录仍保留。</p>
    <template v-else>
     <p>参考 {{date(Date.parse(view.comparison.baseline_period.from))}} 至 {{date(Date.parse(view.comparison.baseline_period.to))}}（不含终点）；本次区间为上方七天观察期。</p>
     <p>该类参考支出 {{money(view.comparison.category_expense_minor)}}，本次已记录 {{money(view.progress.category_expense_minor)}}；差额（参考减本次）{{money(view.comparison.category_difference_minor)}}。</p>
     <p>全账户参考支出 {{money(view.comparison.total_expense_minor)}}，本次已记录 {{money(view.progress.total_expense_minor)}}；整体差额 {{money(view.comparison.total_difference_minor)}}。</p>
     <p>正数表示本次记录较少，负数表示较多。虽然区间长度相同，日期和消费背景不同；还需核对本次记录覆盖情况，差额不是已确认节省。</p>
    </template>
   </section>
   <p v-else-if="view.plan">满七天后提供同长度参考对照；现在可先保存实际观察，不拿整月支出直接减去这几天支出。</p>
   <p v-if="view.review">已保存观察：{{view.review.note||'已核对当前账单记录'}} · {{date(view.review.observed_at)}}</p>
   <label class="trial-review">这次尝试有什么变化？<textarea :aria-label="'试行'+view.trial.id+'复盘备注'" :value="notes[view.trial.id]??view.review?.note??''" maxlength="500" placeholder="例如：改用已有会员，记录了实际消费，仍需核对月底账单。" @input="notes[view.trial.id]=($event.target as HTMLTextAreaElement).value" /></label>
   <Button variant="outline" :disabled="busy" @click="review(view)">保存本次复盘</Button>
   <span class="trial-note">{{view.progress.window_complete?'记录观察结果，不自动记为已省钱。':'不足7天也可以保存观察，仍保持进行中。'}}</span>
  </article>
  <div v-if="offset>0||history.data.value?.length===10" class="trial-pages"><Button :disabled="offset===0||busy" @click="offset=Math.max(0,offset-10)">上一页</Button><Button :disabled="history.data.value?.length!==10||busy" @click="offset+=10">下一页</Button></div>
 </section>
</template>

<style scoped>
.expense-trials{padding:18px 20px;border-top:1px solid #e3eaf1;line-height:1.75;overflow-wrap:anywhere}.expense-trials h3{font-size:17px;margin:0 0 8px}.expense-trials h4{margin:0;font-size:15px}.expense-trials p{margin:8px 0}.trial-create,.trial-record{border:1px solid #dae5e9;padding:14px;border-radius:9px;margin:14px 0}.trial-create{background:#f7fafb}.trial-choice{display:flex;align-items:flex-start;gap:10px;margin:12px 0}.trial-choice>span{display:grid;gap:5px}.trial-choice input{margin-top:6px}.trial-choice small,.trial-note{font-size:13px;color:#52657b}.trial-create fieldset{border:0;padding:0;min-width:0}.trial-record header{display:flex;justify-content:space-between;gap:12px;flex-wrap:wrap}.trial-record header span{font-size:13px}.trial-review{display:grid;gap:6px}.trial-review textarea{width:100%;box-sizing:border-box;min-height:72px;border:1px solid #cbd5e1;border-radius:7px;padding:10px;font:inherit;margin-bottom:10px}.trial-progress{background:#f2f7f6;padding:10px;border-radius:7px;margin:12px 0}.trial-pages{display:flex;gap:10px}
</style>
