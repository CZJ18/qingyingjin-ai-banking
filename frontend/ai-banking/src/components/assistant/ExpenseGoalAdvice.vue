<script setup lang="ts">
import { computed, nextTick, ref, watch,onBeforeUnmount } from 'vue'
import { request, type Schema } from '@/api/client'
import { useSession } from '@/lib/store'
import { currentMonth, period } from '@/lib/queries'
import { money, minor } from '@/lib/utils'
import { categoryLabel } from '@/lib/categories'
import Button from '@/components/ui/button/Button.vue'
import ExpenseGoalNextSteps from '@/components/assistant/ExpenseGoalNextSteps.vue'
import ExpenseTrialWorkbench from '@/components/assistant/ExpenseTrialWorkbench.vue'
import { statementCheckInput, selectedExpenseActions, expenseOptionIsExtreme } from '@/lib/expense-goal-actions'

const previousPeriod=()=>{const [year,month]=currentMonth().split('-').map(Number);return new Date(Date.UTC(year!,month!-2,1)).toISOString().slice(0,7)}
const s=useSession(), open=ref(false), amount=ref(''), month=ref(previousPeriod())
const categories=ref<string[]>([]), busy=ref(false), error=ref('')
const preview=ref<Schema<'ExpensePreview'>|null>(null)
const sourceReport=ref<Schema<'BillingReport'>|null>(null)
const checkEnabled=ref(false),fullMonthConfirmed=ref(false),statementCount=ref(''),statementIncome=ref(''),statementExpense=ref('')
const restoringDraft=ref(false),restoredSnapshot=ref(false)
let draftScope=''
const currentDraftScope=()=>`${s.connected}:${s.sessionEpoch}:${s.user?.id}:${s.accountId}`
function openForm(){
 open.value=true
 void nextTick(()=>document.getElementById('expense-goal')?.scrollIntoView({block:'start',behavior:'smooth'}))
}
defineExpose({openForm})
const baselineLabel=computed(()=>preview.value ? new Intl.DateTimeFormat('zh-CN',{timeZone:'Asia/Shanghai',year:'numeric',month:'2-digit'}).format(new Date(preview.value.baseline_period.from)) : '')
let generation=0
onBeforeUnmount(()=>{generation++})
const nextPeriod=computed(()=>{
 const [year,m]=currentMonth().split('-').map(Number)
 const date=new Date(Date.UTC(year!,m!,1))
 return `${date.getUTCFullYear()}-${String(date.getUTCMonth()+1).padStart(2,'0')}`
})
const labels:Record<string,string>={
 budget:'所选月份没有足够的分类支出记录。请先补充账单，不把未记录支出当作零。',
 complete_historical_month_billing_report:'历史账单覆盖范围尚未核实。已结束月份和生成时间不能证明整月账单已收全，暂不能据此判断下月能省下多少钱。',
 user_adjustable_category_preferences:'请选择你愿意调整的开支类别，系统不会替你决定削减哪些生活开支。',
}
const optionNames:Record<string,string>={conservative:'保守尝试',balanced:'平衡调整',active:'积极调整'}
const planActions=computed(()=>selectedExpenseActions((preview.value?.result.options||[])
 .filter(option=>!expenseOptionIsExtreme(option.categories))
 .flatMap(option=>option.categories.filter(c=>c.proposed_minor<c.original_minor).map(c=>c.category))))
watch([amount,month,()=>categories.value.join(','),()=>s.accountId,()=>s.sessionEpoch,checkEnabled,fullMonthConfirmed,statementCount,statementIncome,statementExpense],()=>{
 if(restoringDraft.value)return
 generation++;preview.value=null;sourceReport.value=null;restoredSnapshot.value=false;error.value=''
},{flush:'sync'})
watch([month,()=>s.accountId,()=>s.sessionEpoch],()=>{
 if(restoringDraft.value)return
 checkEnabled.value=false;fullMonthConfirmed.value=false;statementCount.value='';statementIncome.value='';statementExpense.value=''
},{flush:'sync'})
function saveDraft(){
 if(restoringDraft.value||draftScope!==currentDraftScope())return
 s.saveAssistantDraft({expense:{open:open.value,amount:amount.value,month:month.value,categories:[...categories.value],checkEnabled:checkEnabled.value,fullMonthConfirmed:fullMonthConfirmed.value,statementCount:statementCount.value,statementIncome:statementIncome.value,statementExpense:statementExpense.value,preview:preview.value,sourceReport:sourceReport.value}})
}
async function restoreDraft(){
 const own=++generation,scope=`${s.sessionEpoch}:${s.user?.id}:${s.accountId}`
 const draft=s.readAssistantDraft()?.expense
 draftScope=currentDraftScope()
 restoringDraft.value=true;preview.value=null;sourceReport.value=null;restoredSnapshot.value=false;error.value=''
 open.value=draft?.open||false;amount.value=draft?.amount||'';month.value=draft?.month||previousPeriod();categories.value=draft?.categories||[]
 checkEnabled.value=draft?.checkEnabled||false;fullMonthConfirmed.value=draft?.fullMonthConfirmed||false;statementCount.value=draft?.statementCount||'';statementIncome.value=draft?.statementIncome||'';statementExpense.value=draft?.statementExpense||''
 try{
  if(draft?.preview&&draft.sourceReport){
   const report=await request<Schema<'BillingReport'>>('/api/v1/billing/reports/'+encodeURIComponent(draft.sourceReport.report_id))
   if(own!==generation||scope!==`${s.sessionEpoch}:${s.user?.id}:${s.accountId}`)return
   const range=period(month.value)
   if(report.user_id!==s.user?.id||report.accounts.length!==1||report.accounts[0]?.id!==s.accountId||draft.preview.billing_report_id!==report.report_id||draft.preview.target_minor!==minor(amount.value)||draft.preview.target_period!==nextPeriod.value||Date.parse(report.analysis_period.from)!==Date.parse(range.from)||Date.parse(report.analysis_period.to)!==Date.parse(range.to)||JSON.stringify(report)!==JSON.stringify(draft.sourceReport))throw new Error('原试算依据已变化，请重新测算')
   sourceReport.value=report;preview.value=draft.preview;restoredSnapshot.value=true
  }
 }catch(e){if(own===generation)error.value='填写内容已恢复；原试算依据暂时无法核实，请重新测算。'}
 finally{if(own===generation){restoringDraft.value=false;saveDraft()}}
}
watch(()=>[s.connected,s.user?.id,s.accountId,s.sessionEpoch].join(':'),()=>{void restoreDraft()},{immediate:true,flush:'sync'})
watch([open,amount,month,()=>categories.value.join(','),checkEnabled,fullMonthConfirmed,statementCount,statementIncome,statementExpense,preview,sourceReport],saveDraft,{flush:'sync'})
async function calculate(){
 if(busy.value)return
 error.value=''
 let target:number
 try{target=minor(amount.value);if(target<1)throw new Error('请输入大于零的节省金额')}catch(e){error.value=(e as Error).message;return}
 if(!s.accountId){error.value='请先选择一个账户，明确本次账单范围';return}
 if(!/^\d{4}-(0[1-9]|1[0-2])$/.test(month.value)||month.value>currentMonth()){error.value='参考月份必须是本月或以前的月份';return}
 let statementCheck:Schema<'ExpenseStatementCheck'>|undefined
 try{if(checkEnabled.value)statementCheck=statementCheckInput(statementCount.value,statementIncome.value,statementExpense.value,fullMonthConfirmed.value)}catch(e){error.value=(e as Error).message;return}
 const own=++generation,account=s.accountId,range=period(month.value)
 busy.value=true
 try{
  const report=await request<Schema<'BillingReport'>>('/api/v1/billing/generate_billing_report',{
   method:'POST',body:{from:range.from,to:range.to,timezone:range.timezone,account_id:account,limit:100,offset:0},
  })
  if(own!==generation)return
  if(report.user_id!==s.user?.id||report.accounts.length!==1||report.accounts[0]?.id!==account)
   throw new Error('账单依据与当前用户或账户不一致，请重新测算')
  const result=await request<Schema<'ExpensePreview'>>('/api/v2/lifestyle-goals/expense-reduction-preview',{
   method:'POST',body:{billing_report_id:report.report_id,target_period:nextPeriod.value,target_minor:target,adjustable_categories:[...categories.value],...(statementCheck?{statement_check:statementCheck}:{})},
  })
  if(own===generation){
   if(result.billing_report_id!==report.report_id)throw new Error('建议与本次账单依据不一致，请重新测算')
   sourceReport.value=report;preview.value=result
   restoredSnapshot.value=false
  }
 }catch(e){if(own===generation)error.value=(e as Error).message}
 finally{busy.value=false}
}
</script>

<template>
 <section id="expense-goal" class="expense-goal panel" aria-label="下月节省目标测算">
  <button class="goal-toggle" type="button" :aria-expanded="open" @click="open=!open">
   <span><strong>下个月想少花多少钱？</strong><small>从具体目标开始，查看可调整的开支和目标缺口</small></span><span>{{open?'收起':'试算目标 →'}}</span>
  </button>
  <div v-if="open" class="goal-content">
   <p class="goal-note">这里的“节省”指比参考月份少花，不等同于存入同等金额。使用已有账单和服务端规划规则，不调用模型、不消耗 AI 次数。</p>
   <form @submit.prevent="calculate">
    <fieldset class="draft-fields" :disabled="restoringDraft">
    <div class="goal-fields">
     <label>{{nextPeriod}} 希望少花 / 元<input v-model="amount" aria-label="下月少花金额" placeholder="例如 500" inputmode="decimal" required /></label>
     <label>参考账单月份<input v-model="month" aria-label="节省参考月份" type="month" :max="currentMonth()" required /></label>
    </div>
    <p class="goal-note">默认参考上一已结束月份；月份结束不代表账单已经收全。没有记录时请补充账单，不把空白当作零支出。</p>
    <RouterLink :to="{path:'/billing',query:{month}}">补录并核对这个月的完整账单 →</RouterLink>
    <fieldset><legend>核对完整月份的源账单</legend>
     <label><input type="checkbox" v-model="checkEnabled" aria-label="核对月账单汇总" />我已有该月完整导出账单，填写源账单汇总核对</label>
     <div v-if="checkEnabled" class="goal-fields statement-fields">
      <label>源账单交易笔数<input v-model="statementCount" aria-label="源账单交易笔数" inputmode="numeric" placeholder="填写源账单笔数" required /></label>
      <label>源账单收入合计 / 元<input v-model="statementIncome" aria-label="源账单收入合计" inputmode="decimal" placeholder="没有收入填写0" required /></label>
      <label>源账单支出合计 / 元<input v-model="statementExpense" aria-label="源账单支出合计" inputmode="decimal" placeholder="填写源账单合计" required /></label>
      <label><input type="checkbox" v-model="fullMonthConfirmed" aria-label="确认已核对完整月账单" />已按银行或支付平台导出文件核对该账户整月全部记录、笔数和收支，不将未收全记录当作完整月份。</label>
     </div>
     <p class="goal-note">系统会核对笔数和收支是否与本次报告一致；这是你的资料核对声明，不是银行认证，也不保证未来节省。汇总不一致时先补齐或纠正账单。</p>
    </fieldset>
    <fieldset><legend>你愿意调整哪些开支？</legend>
     <label v-for="c in ['shopping','subscriptions','sports','other']" :key="c"><input type="checkbox" v-model="categories" :value="c" />{{categoryLabel(c)}}</label>
    </fieldset>
    <p class="goal-note">不默认勾选任何类别。必要餐饮、住房、医疗和通勤受保护；运动等生活开支只有你主动选择后才参与测算。</p>
    <Button type="submit" :disabled="busy || !s.connected">{{busy?'正在读取账单并测算…':'查看节省建议'}}</Button>
    <p v-if="error" role="alert">{{error}}</p>
    </fieldset>
   </form>
   <p v-if="restoringDraft" role="status">正在核对恢复的账单依据…</p>
   <p v-else-if="restoredSnapshot" role="status">已恢复此前的填写和试算快照，依据已重新核对；再次测算后才能开始新的试行，不会自动提交。</p>
   <article v-if="preview" class="goal-result" aria-label="节省目标建议">
    <h3>结论</h3>
    <p v-if="preview.result.status==='needs_information'">暂时不能判断 {{preview.target_period}} 少花 {{money(preview.target_minor)}} 是否可行，需要先补充下面的信息。</p>
    <p v-else-if="preview.result.status==='conflict'">现有记录和你选择的可调整范围不足以覆盖目标，还差 {{money(preview.result.reduction_gap_minor)}}。原目标没有被自动降低。</p>
    <p v-else>按你核对的月账单汇总和愿意调整的类别，可以比较以下条件性参考方案；实际能省多少仍取决于下个月的真实消费。</p>
    <h3>数据依据</h3>
    <p>参考月份 {{baselineLabel}}（北京时间）；所选账户记录支出 {{money(preview.historical_expense_minor)}}，目标少花 {{money(preview.target_minor)}}。</p>
    <p class="goal-note">来源报告 {{preview.billing_report_id}} · {{preview.evidence_transaction_ids.length}} 笔交易证据</p>
    <p v-if="preview.baseline_status==='user_reconciled'" class="goal-note">交易笔数与收支汇总匹配，你已确认核对整月资料；银行真实性未核验。该结果只用于情景测算。</p>
    <h3>分析局限</h3>
    <ul><li v-for="field in preview.result.missing_information" :key="field">{{labels[field] || '仍有规划依据需要补充。'}}</li>
     <li>仅覆盖这个账户已记录的账单，不代表家庭全部收支，也不能证明未记录支出为零。</li>
     <li>历史消费不保证未来相同；预计少花不是当前余额、已存款或执行授权。</li>
    </ul>
    <h3>具体建议</h3>
    <ExpenseGoalNextSteps v-if="preview.result.status==='needs_information'" :preview="preview" :adjustable-categories="categories" :report="sourceReport" />
    <p v-else-if="!preview.result.options.length">当前没有足够依据给出具体削减额度。请核对账单和你愿意调整的类别后再测算。</p>
    <section v-for="option in preview.result.options" :key="option.name" class="goal-option">
     <h4>{{expenseOptionIsExtreme(option.categories)?'极端数学上界（不作为试行建议）':optionNames[option.name] || option.name}} · 月度少花参考 {{money(option.expense_reduction_minor)}}</h4>
     <p v-if="expenseOptionIsExtreme(option.categories)" class="option-boundary" role="note">这需要把至少一类开支全部降到0，不代表现实可行，也不会替你选作试行方案。请保留必要购买，优先比较保守方案或调整目标。</p>
     <ul><li v-for="c in option.categories.filter(c=>c.proposed_minor<c.original_minor)" :key="c.category">{{categoryLabel(c.category)}}：历史 {{money(c.historical_minor)}} → 月度参考上限 {{money(c.proposed_minor)}}，本类预计少花 {{money(c.original_minor-c.proposed_minor)}}。</li></ul>
     <p v-if="!option.categories.some(c=>c.proposed_minor<c.original_minor)">当前没有可据此削减的支出；不要为凑目标削减受保护的必要开支。</p>
    </section>
    <section v-if="planActions.length" aria-label="节省行动与替代办法">
     <h4>选定参考金额后，具体怎么开始？</h4>
     <section v-for="action in planActions" :key="action.category" :aria-label="categoryLabel(action.category)+'行动建议'">
      <p><strong>{{categoryLabel(action.category)}} · 具体行动：</strong>{{action.action}}</p>
      <p><strong>替代办法：</strong>{{action.alternative}}</p>
      <p><strong>怎么复盘：</strong>{{action.check}}</p>
      <RouterLink :to="{path:'/billing',query:{report_id:preview.billing_report_id,category:action.category}}">核对{{categoryLabel(action.category)}}明细，选出本人愿意调整的支出 →</RouterLink>
     </section>
    </section>
    <p v-if="preview.result.options.length">每周查看实际账单与月度参考上限的差距，再调整消费习惯。本测算不会自动修改预算或取消订阅。</p>
    <RouterLink to="/planning">如果目标是“存下多少钱”，去核对收入、必要支出与资金规划 →</RouterLink>
   </article>
  </div>
  <ExpenseTrialWorkbench :preview="preview" :adjustable-categories="categories" :can-start="!restoredSnapshot&&!restoringDraft&&!busy" />
 </section>
</template>

<style scoped>
.draft-fields{border:0;margin:0;padding:0;min-width:0}
.expense-goal{margin-bottom:20px;overflow-wrap:anywhere}.goal-toggle{display:flex;justify-content:space-between;align-items:center;gap:14px;width:100%;padding:16px 20px;text-align:left;background:transparent;border:0;color:#142d4e;cursor:pointer}.goal-toggle small{display:block;margin-top:5px;color:#52657b;font-size:13px}.goal-content{padding:0 20px 20px}.goal-note{font-size:13px;line-height:1.7;color:#52657b}.goal-fields{display:grid;grid-template-columns:1fr 1fr;gap:16px}.goal-fields label{display:grid;gap:6px;font-size:14px}.goal-fields input{min-width:0;padding:10px;border:1px solid #cbd5e1;border-radius:7px;font:inherit}fieldset{margin:16px 0;border:1px solid #dbe4ea;border-radius:8px;padding:10px}fieldset label{display:inline-flex;align-items:center;gap:6px;margin-right:18px;padding:5px 0}.goal-result{margin-top:20px;border-top:1px solid #e7edf3;line-height:1.75}.goal-result h3{font-size:16px;color:#142d4e;margin:18px 0 6px}.goal-result p{margin:6px 0}.goal-result ul{padding-left:22px}.goal-option{background:#f5f8fb;border-radius:8px;padding:10px 14px;margin:10px 0}.goal-option h4{margin:0;font-size:14px}@media(max-width:640px){.goal-fields{grid-template-columns:1fr}.goal-toggle{padding:14px}.goal-content{padding:0 14px 14px}.goal-toggle>span:last-child{flex-shrink:0;font-size:13px}}
</style>
