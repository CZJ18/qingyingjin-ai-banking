import {expect,it,vi} from 'vitest'
import {createSSRApp,h} from 'vue'
import {renderToString} from '@vue/server-renderer'
import {selectedExpenseActions,expenseOptionIsExtreme} from '../src/lib/expense-goal-actions'
import {modelFailureMessage} from '../src/lib/model-admission'
import TaskResult from '../src/components/assistant/TaskResult.vue'
import ModelAdmission from '../src/components/assistant/ModelAdmission.vue'
import {ApiError,request} from '../src/api/client'
const render=async(component:unknown,props:Record<string,unknown>)=>{
 const app=createSSRApp(component as never,props);app.component('RouterLink',{setup(_,{slots}){return()=>h('a',slots.default?.())}});return renderToString(app)
}
it('offers concrete conditional alternatives without inventing transaction needs or savings',()=>{
 for(const action of selectedExpenseActions(['shopping','subscriptions','sports','other'])){
  expect(action.alternative.length).toBeGreaterThan(10)
  expect(action.action).not.toContain('保证')
 }
 expect(selectedExpenseActions(['shopping'])[0]?.alternative).toContain('现有')
 expect(selectedExpenseActions(['dining','medical'])).toEqual([])
})
it('distinguishes a category-zero mathematical boundary from an ordinary trial',()=>{
 expect(expenseOptionIsExtreme([{original_minor:300000,proposed_minor:0}])).toBe(true)
 expect(expenseOptionIsExtreme([{original_minor:300000,proposed_minor:250000}])).toBe(false)
 expect(expenseOptionIsExtreme([{original_minor:0,proposed_minor:0}])).toBe(false)
})
const subscriptionTask=(entries:unknown[],kind='conditional_if_all_active_cancelled')=>({task_id:'synthetic-subscription',key:'subscriptions',agent_type:'subscription',tool:'detect_subscriptions',status:'succeeded',parameters:{},error_code:'',output:{result:{subscriptions:entries,duplicate_groups:[],suggestions:[],evidence_transaction_ids:[],active_recurring_forecast:{kind,monthly_minor:9900}}}})
it('does not show a zero forecast or imply no real charges when there are no identified subscriptions',async()=>{
 const html=await render(TaskResult,{task:subscriptionTask([])})
 expect(html).toContain('未识别到订阅候选')
 expect(html).toContain('暂无法估算')
 expect(html).toContain('不代表实际订阅费用为零')
 expect(html).not.toContain('99.00')
})
it('labels nonempty cancellation estimates and does not count them as next-month spend or realized savings',async()=>{
 const task=subscriptionTask([{subscription:{id:'synthetic-1',merchant_name:'合成订阅',amount_minor:9900,status:'active'}}])
 const html=await render(TaskResult,{task})
 expect(html).toContain('全部活跃订阅确认停扣后的月度费用参考')
 expect(html).toContain('99.00');expect(html).toContain('不是已节省金额')
 expect(html).not.toContain('月度订阅预测')
 const unknown=await render(TaskResult,{task:subscriptionTask([{subscription:{id:'synthetic-1',merchant_name:'合成订阅',amount_minor:9900}}],'unknown')})
 expect(unknown).toContain('暂无法估算');expect(unknown).not.toContain('<strong>¥99.00</strong>')
})
it('passes a non-sensitive admission reason through the API client while preserving existing status and code',async()=>{
 vi.stubGlobal('fetch',vi.fn(async()=>new Response(JSON.stringify({code:'MODEL_MONETARY_ADMISSION_REQUIRED',message:'model admission is not configured',data:{reason:'MODEL_ADMISSION_NOT_CONFIGURED'}}),{status:503})))
 try{await request('/api/v1/model-admission');throw new Error('unexpected success')}
 catch(error){expect(error).toBeInstanceOf(ApiError);expect((error as ApiError).code).toBe('MODEL_MONETARY_ADMISSION_REQUIRED');expect((error as ApiError & {reason:string}).reason).toBe('MODEL_ADMISSION_NOT_CONFIGURED')}
 finally{vi.unstubAllGlobals()}
})
it('offers actual non-model actions for an unconfigured instance and never marks it enabled',async()=>{
 const message=modelFailureMessage('MODEL_MONETARY_ADMISSION_REQUIRED','MODEL_ADMISSION_NOT_CONFIGURED')
 expect(message).toContain('当前入口');expect(message).toContain('未配置');expect(message).not.toContain('额度已用尽')
 const html=await render(ModelAdmission,{snapshot:null,loading:false,error:message,reason:'MODEL_ADMISSION_NOT_CONFIGURED'})
 expect(html).toContain('当前入口尚未启用AI分析');expect(html).toContain('试算节省目标');expect(html).toContain('查询账单、订阅或卡片');expect(html).toContain('不调用模型')
 expect(html).not.toContain('可以开始分析')
})
