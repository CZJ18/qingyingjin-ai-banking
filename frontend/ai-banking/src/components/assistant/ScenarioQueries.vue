<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { agentApi, type AgentTask } from '@/api/agent-client'
import { useSession } from '@/lib/store'
import { currentMonth } from '@/lib/queries'
import { scenarioQueries, buildScenarioQuery } from '@/lib/scenario-query'
import Button from '@/components/ui/button/Button.vue'

const emit=defineEmits<{selected:[id:string]}>()
const s=useSession(),month=ref(currentMonth()),busy=ref(false),error=ref(''),saved=ref<AgentTask|null>(null)
let alive=true
let pending:{body:string;key:string}|undefined
const context=()=>`${s.sessionEpoch}:${s.user?.id}:${s.accountId}`
onBeforeUnmount(()=>{alive=false})
watch(context,()=>{pending=undefined;saved.value=null;error.value='';busy.value=false})
const disabled=computed(()=>busy.value||!s.connected||!s.accountId)
async function query(scenario:string){
 if(disabled.value)return
 const own=context();busy.value=true;error.value='';saved.value=null
 let attempted=false
 try{
  const body=buildScenarioQuery(scenario,s.accountId,month.value),serialized=JSON.stringify(body)
  if(pending?.body!==serialized)pending={body:serialized,key:crypto.randomUUID()}
  attempted=true
  const task=await agentApi.submit(body,pending.key)
  if(!alive||own!==context())return
  // A known submitted task is inspected on uncertainty, never automatically resubmitted or replayed.
  saved.value=task;pending=undefined;emit('selected',task.task_id)
  const result=await agentApi.run(task.task_id,task.version)
  if(!alive||own!==context())return
  saved.value=result
  if(result.status!=='succeeded')error.value='查询尚未完成，请查看下方服务端任务状态。'
 }catch(e){
  if(alive&&own===context())error.value=(e as Error).message+(saved.value?'；任务已保存，请查看任务状态，不会自动重跑。':attempted?'；提交结果不确定时，再次查询会沿用本次请求编号。':'')
 }finally{if(alive&&own===context())busy.value=false}
}
</script>
<template>
 <section id="scenario-queries" class="scenario-queries panel" aria-label="六类业务只读查询">
  <header><h2>先查清楚，再决定</h2><span>不调用模型 · 不消耗 AI 次数</span></header>
  <p>直接使用现有业务工具查询所选账户。账单与订阅按下方月份统计；其余查询当前资源、已登记日期和操作限制。自然语言分析仍使用上方真实模型准入。</p>
  <label>账单与订阅月份<input v-model="month" type="month" :max="currentMonth()" aria-label="只读查询月份" :disabled="busy" /></label>
  <div class="query-buttons"><Button v-for="entry in scenarioQueries" :key="entry.id" variant="outline" :disabled="disabled" @click="query(entry.id)">{{entry.button}}</Button></div>
  <p v-if="!s.accountId">请先在页面顶部选择一个本人账户。</p>
  <p v-if="busy" role="status">正在查询并保存任务，请稍候…</p>
  <p v-if="error" role="alert">{{error}}</p>
  <button v-if="saved" class="saved-query" type="button" @click="emit('selected',saved.task_id)">查看已保存的查询任务 →</button>
  <p class="query-note">结果来自后端工具和数据库，不是新的模型回答。转账、理财、卡片及商户操作仍为模拟；查询不创建金融操作意向。</p>
 </section>
</template>
<style scoped>
.scenario-queries{padding:18px 20px;margin-bottom:20px;line-height:1.7;overflow-wrap:anywhere}.scenario-queries header{display:flex;gap:12px;justify-content:space-between;flex-wrap:wrap;align-items:center}.scenario-queries h2{font-size:17px;margin:0}.scenario-queries p,.scenario-queries header span{font-size:13px;color:#52657b}.scenario-queries label{display:flex;align-items:center;gap:12px;font-size:14px}.scenario-queries input{min-width:0;padding:8px;border:1px solid #cbd5e1;border-radius:7px;font:inherit}.query-buttons{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px;margin:14px 0}.saved-query{padding:0;border:0;background:none;color:#0d766e;font:inherit;cursor:pointer}.scenario-queries [role=alert]{color:#b42318}@media(max-width:640px){.query-buttons{grid-template-columns:repeat(2,minmax(0,1fr))}.scenario-queries{padding:14px}.scenario-queries label{align-items:flex-start;flex-direction:column;gap:4px}}
</style>
