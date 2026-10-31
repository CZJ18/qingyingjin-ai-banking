import { expect,it } from 'vitest'
import {createSSRApp,h} from 'vue'
import {renderToString} from '@vue/server-renderer'
import TaskResult from '../src/components/assistant/TaskResult.vue'
import ModelAdmission from '../src/components/assistant/ModelAdmission.vue'
import type {TaskRow} from '../src/components/agents/task-view'

const row:TaskRow={task_id:'synthetic-task',key:'billing',agent_type:'billing',tool:'generate_billing_report',skill_id:'billing_analysis',execution_mode:'read_only',status:'succeeded',parameters:{},error_code:'',output:{status:'succeeded',workflow_execute_id:'direct_provider:synthetic-provider',explanation:'支出集中在餐饮和运动。',explanation_evidence_ids:['report'],result:{report_id:'report',currency:'CNY',total_income:100000,total_expense:24000,transaction_count:3,category_breakdown:[],findings:[{finding_id:'totals',description:'期间收入100000分',transaction_ids:[]}],anomalies:[],evidence_transaction_ids:['2','3','4']}}}
const render=async(component:unknown,props:Record<string,unknown>)=>{
 const app=createSSRApp(component as never,props);app.component('RouterLink',{setup(_,{slots}){return()=>h('a',slots.default?.())}});return renderToString(app)
}
it('presents saved billing interpretation with evidence, limitations and actionable non-executing next steps',async()=>{
 const html=await render(TaskResult,{task:row})
 for(const text of ['结论','数据依据','分析局限','具体建议','已记录 3 笔交易','完整月份','不能据此确认下月可节省金额','设置下月少花目标','模型费用未正式对账'])expect(html).toContain(text)
 expect(html).toContain('1,000.00');expect(html).not.toMatch(/<p[^>]*>期间收入100000分/)
 expect(html).not.toContain('费用已结算');expect(html).not.toContain('保证节省')
})
it('separates a saved real interpretation from permission to start a new model request',async()=>{
 const html=await render(ModelAdmission,{snapshot:null,loading:false,error:'模型费用服务暂不可用',completedTask:{status:'succeeded',result:{outputs:[row.output]}}})
 expect(html).toContain('已有模型解读可查看');expect(html).toContain('新分析暂不可用')
 expect(html).not.toContain('真实模型解读')
 expect(html).not.toContain('可以开始分析')
 const fake=await render(ModelAdmission,{snapshot:null,loading:false,error:'未配置',completedTask:{status:'failed',result:{outputs:[row.output]}}})
 expect(fake).not.toContain('已有模型解读可查看')
 const simulated=await render(ModelAdmission,{snapshot:null,loading:false,error:'未配置',completedTask:{status:'succeeded',result:{outputs:[{...row.output,workflow_execute_id:'fixture:synthetic'}]}}})
 expect(simulated).not.toContain('已有模型解读可查看')
})
it('offers optional category-specific actions backed by the report without inventing a saving amount',async()=>{
 const task={...row,output:{...row.output,result:{...row.output.result,category_breakdown:[
  {category:'shopping',expense_minor:7860,transaction_ids:['2','3']},
  {category:'dining',expense_minor:2350,transaction_ids:['4']},
 ]}}}
 const html=await render(TaskResult,{task})
 for(const text of ['规则生成的可选行动','购物','78.60','2 笔','冷静期','24 小时','怎么复盘','核对购物明细'])expect(html).toContain(text)
 expect(html).toContain('记录金额不是可节省额度')
 expect(html).not.toContain('削减餐饮');expect(html).not.toContain('保证节省')
})
it('does not offer a category trial without positive amounts and transaction evidence',async()=>{
 const task={...row,output:{...row.output,result:{...row.output.result,category_breakdown:[
  {category:'shopping',expense_minor:7860,transaction_ids:[]},
  {category:'subscriptions',expense_minor:0,transaction_ids:['4']},
 ]}}}
 const html=await render(TaskResult,{task})
 expect(html).not.toContain('冷静期');expect(html).not.toContain('先核对将要续费的订阅')
 expect(html).toContain('补齐上一完整月份账单')
})
it('recognizes a saved Coze interpretation without implying that a new paid request is enabled',async()=>{
 const output={...row.output,workflow_execute_id:'7702079573042659328'}
 const html=await render(ModelAdmission,{snapshot:null,loading:false,error:'本周期已关闭',completedTask:{status:'succeeded',result:{outputs:[output]}}})
 expect(html).toContain('已有模型解读可查看');expect(html).toContain('新分析暂不可用')
 expect(html).not.toContain('可以开始分析');expect(html).not.toContain('真实模型解读')
})
