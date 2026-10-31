<script setup lang="ts">
import {computed,nextTick,ref,watch} from 'vue'
import {useRoute} from 'vue-router'
import {useQuery} from '@tanstack/vue-query'
import {request,type Schema} from '@/api/client'
import {useSession,useUI} from '@/lib/store'
import {useAccounts} from '@/lib/queries'
import {money,minor,date} from '@/lib/utils'
import Button from '@/components/ui/button/Button.vue'
import State from '@/components/State.vue'
import Status from '@/components/Status.vue'
import ExecutionDialog from '@/components/ExecutionDialog.vue'
import BirthdayReminders from '@/components/BirthdayReminders.vue'

const s=useSession(),ui=useUI(),accounts=useAccounts(),dialog=ref<InstanceType<typeof ExecutionDialog>>()
const state=useQuery({queryKey:computed(()=>['scenarios',s.sessionEpoch]),enabled:computed(()=>s.connected),queryFn:({signal})=>request<Schema<'ScenarioState'>>('/api/v1/execution/scenarios',{signal}),refetchInterval:3000})
const route=useRoute()
watch(()=>[route.hash,state.isLoading.value],async()=>{
 if(state.isLoading.value||!['#investment','#birthday'].includes(route.hash))return
 await nextTick()
 document.getElementById(route.hash.slice(1))?.scrollIntoView({block:'start'})
},{immediate:true,flush:'post'})
const object=(v:unknown):Record<string,unknown>=>v&&typeof v==='object'&&!Array.isArray(v)?v as Record<string,unknown>:{}
const cash=computed(()=>accounts.data.value?.filter(a=>a.status==='active'&&a.account_type==='checking')||[])
const ops=computed(()=>state.data.value?.operations||[])
const profile=computed(()=>ops.value.find(o=>o.kind==='investment_assessment'&&o.status==='succeeded'))
const risk=computed(()=>Number(object(profile.value?.result).risk_level)||0)
const source=ref(''),target=ref(''),scheduleAmount=ref(''),when=ref(''),description=ref('')
const answers=ref([0,0,0]),orderProduct=ref('sim-cash-v1'),orderAmount=ref(''),orderAction=ref<'buy'|'redeem'>('buy')
const occasionName=ref(''),birthday=ref(''),orderTime=ref('09:00'),reserve=ref('1000'),items=ref<string[]>([])
const occasionNameInput=ref<HTMLInputElement>()
const plans=computed(()=>ops.value.filter(o=>o.kind==='occasion_plan'&&o.status==='succeeded'))
const kindNames:Record<string,string>={scheduled_transfer:'定时模拟转账',investment_assessment:'模拟风险测评',investment_order:'模拟理财申赎',occasion_plan:'庆祝资金预留',occasion_order:'模拟鲜花/蛋糕订购',occasion_release:'退回庆祝剩余预留'}
function dueOf(op:Schema<'ExecutionOperation'>){return Number(object(op.parameters).execute_at)||0}
async function prepare(kind:Schema<'ExecutionPrepareInput'>['kind'],parameters:Schema<'ExecutionPrepareInput'>['parameters']){
 try{await dialog.value?.prepare(kind,parameters)}catch(e){ui.notify((e as Error).message)}
}
async function schedule(){
 try{const text=when.value.length===16?when.value+':00':when.value;const due=Date.parse(text+'+08:00');if(!Number.isFinite(due))throw new Error('请选择北京时间的执行时间')
 await prepare('scheduled_transfer',{source_account_id:source.value,target_account_id:target.value,amount_minor:minor(scheduleAmount.value),execute_at:due,description:description.value})}catch(e){ui.notify((e as Error).message)}
}
async function invest(){
 try{await prepare('investment_order',{action:orderAction.value,product_id:orderProduct.value,account_id:source.value,amount_minor:minor(orderAmount.value),assessment_id:profile.value?.id||''})}catch(e){ui.notify((e as Error).message)}
}
async function occasion(){
 try{await prepare('occasion_plan',{name:occasionName.value,birthday:birthday.value,order_time:orderTime.value,account_id:source.value,reserve_minor:minor(reserve.value),items:[...items.value]})}catch(e){ui.notify((e as Error).message)}
}
async function cancel(op:Schema<'ExecutionOperation'>){
 try{await request(`/api/v1/execution/operations/${op.id}/cancel`,{method:'POST',body:{decision:'reject',intent_digest:op.intent_digest,confirmation_nonce:op.confirmation_nonce}});await state.refetch();ui.notify('已撤销待执行计划，不会扣款')}catch(e){ui.notify((e as Error).message)}
}
function positionFor(plan:Schema<'ExecutionOperation'>){return state.data.value?.positions.find(p=>p.id===String(object(plan.result).reserve_account_id))}
async function fillBirthday(value:{name:string;birthday:string}){
 occasionName.value=value.name;birthday.value=value.birthday
 await nextTick()
 occasionNameInput.value?.scrollIntoView({block:'center',behavior:'smooth'})
 occasionNameInput.value?.focus({preventScroll:true})
 ui.notify('已填写对象和日期，保留你的金额、商品和订购时间；请核对后创建意向，尚未预留或订购')
}
</script>

<template>
 <div class="page scenarios-page">
  <div class="page-heading"><div><h1>理财、定时与跨场景计划</h1><p>让资金和时间安排有据可查，每项操作均需本人明确授权。</p></div></div>
  <p class="simulation-label">模拟业务环境 · 不接真实银行或商户 · 本页不调用模型</p>
  <BirthdayReminders @prepare="fillBirthday" />
  <State :connected="s.connected" :loading="state.isLoading.value" :error="state.error.value" @retry="state.refetch()">
   <section class="panel scenario-panel"><h2>选择专用模拟资金账户</h2>
    <label>资金账户<select v-model="source" aria-label="场景资金账户" required><option disabled value="">请选择本人活期账户</option><option v-for="a in cash" :key="a.id" :value="a.id">{{a.display_name}} · {{money(a.balance_minor)}}</option></select></label>
    <p>预留和持仓账户不能通过普通转账、AA或交易导入直接动用。必要生活储备仍由原风控保护；没有可用账户时，请去安全中心创建模拟账户。</p><RouterLink to="/security">前往账户与授权管理 →</RouterLink>
   </section>
   <section class="panel scenario-panel" aria-label="定时转账"><h2>定时模拟转账</h2>
    <form @submit.prevent="schedule" class="scenario-form">
     <label>转入本人账户<select v-model="target" aria-label="定时转入账户" required><option disabled value="">请选择</option><option v-for="a in cash.filter(a=>a.id!==source)" :key="a.id" :value="a.id">{{a.display_name}}</option></select></label>
     <label>转账金额 / 元<input v-model="scheduleAmount" inputmode="decimal" required /></label>
     <label>执行时间（北京时间）<input v-model="when" type="datetime-local" step="1" required /></label>
     <label>转账用途<input v-model="description" maxlength="128" required /></label>
     <Button type="submit" :disabled="!source">创建定时转账意向</Button>
    </form><p>仅支持未来30天内的单次模拟转账。核对并授权后，后台到期重新核验余额和保护资金；不会自动续期。授权不预先扣款，可以在执行前撤销，过期或失败后不会自动再次转账。</p>
   </section>
   <section id="investment" class="panel scenario-panel" aria-label="模拟理财"><h2>模拟理财与风险适配</h2>
    <p>以下是固定的教学演示目录，不是实际可购买产品或投资建议。仅按本金模拟申购和赎回，不生成净值或收益；信用额度不作为资金。</p>
    <form @submit.prevent="prepare('investment_assessment',{answers:[...answers]})" class="scenario-form">
     <label v-for="(label,i) in ['投资知识与经验','可承受本金损失的能力','对波动的接受程度']" :key="label">{{label}}<select v-model.number="answers[i]" :aria-label="label" required><option :value="0" disabled>请选择本人情况</option><option :value="1">较低 / 保守</option><option :value="2">中等</option><option :value="3">较高</option></select></label>
     <Button type="submit" :disabled="answers.some(a=>!a)">保存模拟风险测评</Button>
    </form>
    <p>当前已授权测评：{{risk?'风险承受级别 '+risk+'，按三项中最低级别限制产品':'尚未完成'}}。测评不等于申购授权，也不代替银行正式适当性审核。</p>
    <div class="product-grid"><article v-for="p in state.data.value?.products" :key="p.id"><h3>{{p.name}}</h3><p>风险级别 {{p.risk_level}} · 起点 {{money(p.minimum_minor)}} · 模拟赎回无锁定期</p><p>{{p.description}}</p><strong>{{risk>=p.risk_level?'符合当前模拟测评范围':'当前未符合测评范围'}}</strong></article></div>
    <form @submit.prevent="invest" class="scenario-form">
     <label>产品<select v-model="orderProduct" aria-label="模拟理财产品"><option v-for="p in state.data.value?.products" :key="p.id" :value="p.id">{{p.name}}</option></select></label>
     <label>申赎类型<select v-model="orderAction" aria-label="申赎类型"><option value="buy">申购</option><option value="redeem">赎回</option></select></label>
     <label>申赎金额 / 元<input v-model="orderAmount" required inputmode="decimal" /></label><Button type="submit" :disabled="!source">创建申赎意向</Button>
    </form>
    <ul><li v-for="p in state.data.value?.positions.filter(p=>p.account_type==='investment')" :key="p.id">{{p.display_name}}：持有模拟本金 {{money(p.balance_minor)}}；收益未模拟，不是现实产品收益。</li></ul>
   </section>
   <section id="birthday" class="panel scenario-panel" aria-label="庆祝跨场景计划"><h2>生日庆祝：预留资金 → 模拟订购</h2>
    <p>手动提供经本人确认的生日与对象，不推断关系或读取联系人。花店和蛋糕店均为本地模拟目录，不联系真实商户。预留资金不代表已经花掉。</p>
    <form @submit.prevent="occasion" class="scenario-form">
     <label>庆祝对象<input ref="occasionNameInput" v-model="occasionName" required maxlength="32" /></label><label>生日日期<input v-model="birthday" type="date" required /></label><label>订购时间（北京时间）<input v-model="orderTime" type="time" required /></label><label>预留金额 / 元<input v-model="reserve" required inputmode="decimal" /></label>
     <fieldset><legend>明确选择模拟商品</legend><label><input type="checkbox" v-model="items" value="flowers" />鲜花 ¥200.00</label><label><input type="checkbox" v-model="items" value="cake" />蛋糕 ¥200.00</label></fieldset><Button type="submit" :disabled="!source||!items.length">创建庆祝预留意向</Button>
    </form>
    <article v-for="p in plans" :key="p.id" class="occasion-record"><h3>{{object(p.parameters).name}} · {{object(p.parameters).birthday}}</h3><p>预留账户剩余 {{money(positionFor(p)?.balance_minor)}} · 选定商品总价 {{money(Number(object(p.snapshot).catalog_cost_minor))}}</p><p>生日两天前（北京时间）：{{date(Number(object(p.snapshot).order_due))}}</p>
     <Button variant="outline" @click="prepare('occasion_order',{plan_id:p.id,execute_at:Number(object(p.snapshot).order_due)})">授权生日两天前模拟订购</Button>
     <Button variant="outline" @click="prepare('occasion_order',{plan_id:p.id,execute_at:0})">立即模拟订购</Button>
     <Button variant="ghost" :disabled="!source" @click="prepare('occasion_release',{plan_id:p.id,account_id:source})">退回剩余预留</Button><p>订购还需单独核对商品、金额和时间后授权；仅保存预留计划不会自动下单。退回前需撤销尚未执行的订单。</p>
    </article>
   </section>
   <section class="panel scenario-panel" aria-label="场景操作历史"><h2>计划与操作历史</h2><p>定时任务状态来自服务端；授权等待不等于已经转账或下单。失败、撤销和未知记录继续保留。</p>
    <p v-if="!ops.length">暂无场景操作。</p><article v-for="op in ops" :key="op.id" class="scenario-operation"><strong>{{kindNames[op.kind]||op.kind}}</strong><Status :value="op.status" /><p>{{money(op.amount_minor)}} · {{op.id}}</p><p v-if="dueOf(op)">约定执行时间 {{date(dueOf(op))}}</p><p v-if="op.error_code" role="alert">{{op.error_code}}</p>
     <Button v-if="op.status==='pending_authorization'" variant="outline" @click="dialog?.inspect(op.id)">继续核对并授权</Button><Button v-if="op.status==='authorized'&&dueOf(op)" variant="outline" @click="cancel(op)">撤销定时操作</Button><Button variant="ghost" @click="dialog?.inspect(op.id)">查看执行依据</Button>
    </article>
   </section>
  </State><ExecutionDialog ref="dialog" />
 </div>
</template>

<style scoped>
.scenario-panel{padding:22px;margin:20px 0;line-height:1.75;overflow-wrap:anywhere}.scenario-panel h2{font-size:20px;margin:0 0 12px}.scenario-panel p{font-size:13px;color:#52657b}.scenario-form{display:flex;gap:14px;flex-wrap:wrap;align-items:end;margin:18px 0}.scenario-form label,.scenario-panel>label{display:flex;flex-direction:column;gap:6px;font-size:13px}.scenario-form input,.scenario-form select,.scenario-panel>label select{padding:9px;border:1px solid #cbd5e1;border-radius:6px;font:inherit;min-width:0}.scenario-form fieldset label{display:inline-flex;flex-direction:row;margin-right:14px}.product-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,230px),1fr));gap:12px}.product-grid article,.occasion-record,.scenario-operation{padding:15px;border:1px solid #dbe4ea;border-radius:9px;margin-top:12px}.product-grid h3{font-size:16px}.scenario-operation>strong{margin-right:15px}@media(max-width:600px){.scenario-form{display:grid;grid-template-columns:1fr}.scenario-panel{padding:15px}.scenario-form input,.scenario-form select{width:100%;box-sizing:border-box}}
</style>
