import {expect,it} from 'vitest'
import {modelAdmissionState,modelFailureMessage} from '../src/lib/model-admission'

const ready={currency:'USD',frozen:false,approval_window:{next_request_maximum_cost:'0.00000100',remaining_approval_budget:'0.00000100',remaining_requests:1,remaining_concurrent_slots:1},approval_limits:{max_requests:1,max_concurrent:1,simulation:true}}
it('explains a refused input limit without encouraging a retry or a higher allowance',()=>{
 expect(modelFailureMessage('MODEL_CONTEXT_LIMIT_EXCEEDED')).toContain('输入')
 expect(modelFailureMessage('MODEL_CONTEXT_LIMIT_EXCEEDED')).toContain('缩小')
 expect(modelFailureMessage('MODEL_CONTEXT_LIMIT_EXCEEDED')).not.toContain('提高')
})
it('requires a complete, usable owner-bound snapshot and preserves decimal precision',()=>{
 expect(modelAdmissionState(ready).available).toBe(true)
 expect(modelAdmissionState(null).available).toBe(false)
 for(const missing of ['currency','approval_window','approval_limits']){
  const raw={...ready};delete raw[missing as keyof typeof ready]
  expect(modelAdmissionState(raw).available).toBe(false)
 }
 for(const change of [{remaining_requests:0},{remaining_concurrent_slots:0},{remaining_requests:2},{remaining_requests:1.5},{remaining_approval_budget:'0.00000099'},{remaining_approval_budget:'unknown'},{remaining_approval_budget:1}]){
  expect(modelAdmissionState({...ready,approval_window:{...ready.approval_window,...change}}).available).toBe(false)
 }
 expect(modelAdmissionState({...ready,approval_limits:{...ready.approval_limits,max_requests:0}}).available).toBe(false)
 expect(modelAdmissionState({...ready,frozen:true}).available).toBe(false)
})
it('explains admission and unknown failures without promising that a sent request was free',()=>{
 expect(modelFailureMessage('MODEL_EXECUTION_CONFIRMATION_REQUIRED')).toContain('独立确认')
 expect(modelFailureMessage('MODEL_BUDGET_UNAVAILABLE')).toContain('费用服务')
 expect(modelFailureMessage('MODEL_OUTCOME_UNKNOWN')).toContain('不要重复')
 expect(modelFailureMessage('MODEL_OUTCOME_UNKNOWN')).not.toContain('没有扣费')
})
it('explains interrupted legacy Coze requests without suggesting a blind replay',()=>{
 expect(modelFailureMessage('COZE_UNAVAILABLE')).toContain('不要重复')
 expect(modelFailureMessage('COZE_UNAVAILABLE')).toContain('可能已发出')
})
it('distinguishes exhausted requests from money and active calls',()=>{
 const snapshot={...ready,approval_window:{...ready.approval_window,remaining_approval_budget:'0.10',remaining_requests:0}}
 expect(modelAdmissionState(snapshot).reason).toContain('请求次数已用尽')
 expect(modelAdmissionState(snapshot).reason).toContain('单加预算')
 const active={...ready,approval_window:{...ready.approval_window,remaining_concurrent_slots:0}}
 expect(modelAdmissionState(active).reason).toContain('正在处理')
 expect(modelAdmissionState(active).reason).toContain('不要重复提交')
 const insufficient={...ready,approval_window:{...ready.approval_window,remaining_approval_budget:'0.00000099'}}
 expect(modelAdmissionState(insufficient).reason).toContain('剩余预算不足')
 expect(modelAdmissionState(insufficient).reason).not.toContain('请求次数已用尽')
})
it('asks to recheck the reviewed price policy when admission is no longer valid',()=>{
 expect(modelFailureMessage('MODEL_APPROVAL_REQUIRED')).toContain('价格策略')
 expect(modelFailureMessage('MODEL_APPROVAL_REQUIRED')).not.toContain('自动续')
})
