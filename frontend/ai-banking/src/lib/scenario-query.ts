import type { AgentSubmit, AgentTaskSpec } from '@/api/agent-client'
import { currentMonth, period } from './queries'

// Explicit user-selected tool queries, never a substitute for natural-language model planning.
export const scenarioQueries = [
  {id:'billing',name:'账单分析',button:'查询账单',destination:'/billing'},
  {id:'transfer',name:'智能转账',button:'查询转账范围',destination:'/security#recipient-heading'},
  {id:'investment',name:'理财操作',button:'查询理财产品',destination:'/scenarios#investment'},
  {id:'cards',name:'卡片管理',button:'查询卡片',destination:'/security#cards-title'},
  {id:'subscriptions',name:'订阅与代扣',button:'查询订阅',destination:'/subscriptions'},
  {id:'birthday',name:'跨场景生日安排',button:'查询生日安排',destination:'/scenarios#birthday'},
] as const
export type ScenarioQuery = typeof scenarioQueries[number]['id']

export function buildScenarioQuery(scenario:string, accountId:string, month:string):AgentSubmit {
  if (!scenarioQueries.some(entry=>entry.id===scenario)) throw new Error('请选择已支持的业务查询')
  if (!/^[1-9][0-9]{0,18}$/.test(accountId) || BigInt(accountId)>9223372036854775807n)
    throw new Error('请先选择有效的本人账户')
  const range=period(month)
  if (month>currentMonth()) throw new Error('账单与订阅查询月份不能晚于本月')
  const bindings={account_id:accountId,from:range.from,to:range.to}
  const spec:AgentTaskSpec={key:scenario,agent_type:'supervisor',tool:'get_scenario_overview',
    skill_id:'task_planning',skill_version:1,execution_mode:'read_only',
    parameters:{scenario,account_id:accountId},dependencies:[],use_coze:false,required_permissions:['READ']}
  if (scenario==='billing' || scenario==='subscriptions') {
    spec.agent_type=scenario==='billing'?'billing':'subscription'
    spec.tool=scenario==='billing'?'generate_billing_report':'detect_subscriptions'
    spec.skill_id=scenario==='billing'?'billing_analysis':'subscription_analysis'
    spec.parameters={...bindings,timezone:range.timezone}
  }
  return {tasks:[spec],bindings,max_attempts:1,timeout_ms:90000}
}
