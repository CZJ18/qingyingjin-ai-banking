// Display context and conditional estimates only. These do not grant account
// access, change the server's user-wide management scope or calculate funds.
const record=(v:unknown):Record<string,unknown>=>v&&typeof v==='object'&&!Array.isArray(v)?v as Record<string,unknown>:{}
function reference(value:unknown){
 const p=record(value),id=p.account_id,from=p.from,to=p.to,timezone=p.timezone
 if(typeof id!=='string'||!/^[1-9][0-9]{0,18}$/.test(id)||BigInt(id)>9223372036854775807n||typeof from!=='string'||typeof to!=='string'||from.length>40||to.length>40||!/T/.test(from)||!/T/.test(to)||!Number.isFinite(Date.parse(from))||!Number.isFinite(Date.parse(to))||Date.parse(to)<=Date.parse(from)||!['Asia/Shanghai','UTC'].includes(String(timezone)))return null
 return {accountId:id,from,to,timezone:String(timezone)}
}
export function subscriptionManagementPath(parameters:unknown){
 const p=reference(parameters);if(!p)return '/subscriptions'
 return '/subscriptions?'+new URLSearchParams({reference_account_id:p.accountId,reference_from:p.from,reference_to:p.to,reference_timezone:p.timezone})
}
export function subscriptionReference(query:unknown){
 const q=record(query);return reference({account_id:q.reference_account_id,from:q.reference_from,to:q.reference_to,timezone:q.reference_timezone})
}
export function subscriptionForecastView(value:unknown){
 const p=record(value),entries=Array.isArray(p.subscriptions)?p.subscriptions:[]
 const amount=(forecast:unknown,kind:string)=>{const f=record(forecast);return f.kind===kind&&typeof f.monthly_minor==='number'&&Number.isSafeInteger(f.monthly_minor)&&f.monthly_minor>=0&&f.monthly_minor<=1e12?f.monthly_minor:null}
 const has=(status:string)=>entries.some(v=>record(record(v).subscription).status===status)
 return {activeMinor:has('active')?amount(p.active_recurring_forecast,'conditional_if_all_active_cancelled'):null,cancelledMinor:has('cancelled')?amount(p.cancelled_avoided_spend,'estimated_avoided_future_spend'):null}
}
