<script setup lang="ts">
import { computed } from 'vue'
import { money } from '@/lib/utils'
import { scenarioQueries } from '@/lib/scenario-query'
const props = defineProps<{ result: unknown }>()
const scenarios: Record<string, { name: string; path: string; action: string }> = {
  transfer: { name: '智能转账', path: '/security', action: '前往转账与收款人管理' },
  cards: { name: '卡片管理', path: '/security', action: '前往卡片管理' },
  investment: { name: '理财操作', path: '/scenarios', action: '前往理财测评与产品对比' },
  subscriptions: { name: '订阅与代扣', path: '/subscriptions', action: '前往订阅管理' },
  birthday: { name: '生日跨场景计划', path: '/scenarios', action: '前往生日日历与庆祝计划' },
}
const kinds: Record<string, string> = { source_account: '所选资金账户', saved_recipient_reference: '已保存的收款人引用', card: '模拟卡片', investment_product: '教学演示产品', subscription: '订阅记录', registered_birthday: '已登记生日' }
const states: Record<string, string> = { active:'正常',locked:'已锁定',lost:'已挂失',closed:'已关闭',cancelled:'已取消',pending:'待核对',requires_explicit_selection:'仍需本人查找并选择',available:'可查看',registered:'已登记',confirmed:'已核实',catalog_only:'仅为教学目录',recorded:'已记录',disabled:'已停用',candidate:'待核对候选',unbound:'未绑定' }
const numericKeys = ['amount_minor','version','credit_limit_minor','credit_available_minor','transaction_limit_minor','minimum_minor','risk_level','lock_days','next_billing_at','days_until'] as const
const moneyLabels: Record<string, string> = { amount_minor:'记录金额',credit_limit_minor:'模拟信用额度',credit_available_minor:'可用模拟信用额度',transaction_limit_minor:'单笔交易限额',minimum_minor:'起购金额' }
const object = (v: unknown): Record<string, unknown> | null => v && typeof v === 'object' && !Array.isArray(v) ? v as Record<string, unknown> : null
const boundedText = (v: unknown, max: number) => typeof v === 'string' && v.length > 0 && v.length <= max
const result = computed(() => {
  const raw=object(props.result)
  if (!raw || typeof raw.scenario!=='string' || !Object.hasOwn(scenarios,raw.scenario) || raw.is_simulated!==true || typeof raw.truncated!=='boolean' ||
    typeof raw.account_id!=='string' || !/^[1-9][0-9]{0,18}$/.test(raw.account_id) || typeof raw.user_id!=='string' || !/^[1-9][0-9]{0,18}$/.test(raw.user_id) ||
    !Array.isArray(raw.items) || raw.items.length>50 || !Array.isArray(raw.limitations) || raw.limitations.length>16 || !raw.limitations.every(v=>boundedText(v,1024))) return null
  const items: Record<string, unknown>[]=[]
  for (const candidate of raw.items) {
    const item=object(candidate)
    if (!item || !boundedText(item.id,128) || typeof item.kind!=='string' || !Object.hasOwn(kinds,item.kind) || !boundedText(item.status,64) ||
      item.currency!==undefined && item.currency!=='CNY' ||
      numericKeys.some(key=>item[key]!==undefined && (typeof item[key]!=='number' || !Number.isSafeInteger(item[key]) || Number(item[key])<0 || Number(item[key])>(key.endsWith('_minor') ? 1e12 : 1e15))) ||
      item.label!==undefined && (item.kind!=='investment_product' || !boundedText(item.label,128)) ||
      item.billing_interval!==undefined && !boundedText(item.billing_interval,32) ||
      item.month_day!==undefined && (typeof item.month_day!=='string' || !/^(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])$/.test(item.month_day)) ||
      item.next_date!==undefined && (typeof item.next_date!=='string' || !/^20[0-9]{2}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])$/.test(item.next_date))) return null
    // Copy only the documented, non-sensitive DTO. Unknown fields never reach the template.
    const safe:Record<string,unknown>={id:item.id,kind:item.kind,status:item.status}
    for (const key of [...numericKeys,'currency','label','billing_interval','month_day','next_date']) if (item[key]!==undefined) safe[key]=item[key]
    items.push(safe)
  }
  return { scenario:raw.scenario, accountId:raw.account_id, items, limitations:raw.limitations as string[], truncated:raw.truncated }
})
const choice=computed(()=>{
 if(!result.value)return null
 const entry=scenarios[result.value.scenario]!
 return {...entry,path:scenarioQueries.find(item=>item.id===result.value!.scenario)?.destination||entry.path}
})
const currencyValues=(item:Record<string,unknown>)=>Object.entries(moneyLabels).flatMap(([key,label])=>typeof item[key]==='number' ? [{label,value:money(item[key] as number)}] : [])
const status=(item:Record<string,unknown>)=>states[String(item.status)] || String(item.status)
</script>
<template>
 <section class="scenario-overview" aria-label="只读场景资源">
  <p v-if="!result || !choice" role="alert">场景查询结果不符合契约，请重新查询并核对服务版本；不会创建操作意向。</p>
  <template v-else>
   <h4>{{choice.name}} · 模拟业务</h4>
   <p>来自 Go 只读工具，范围为账户 #{{result.accountId}}。查询不会创建操作意向，也不会授予执行权限。</p>
   <p v-if="!result.items.length">当前范围没有可用记录，请前往业务页面核对或补充，不代表其他账户没有记录。</p>
   <ul v-else class="overview-resources">
    <li v-for="item in result.items" :key="String(item.kind)+':'+String(item.id)">
     <strong>{{item.label || kinds[String(item.kind)]}}</strong><span>编号 {{item.id}} · {{status(item)}}</span>
     <dl><div v-for="field in currencyValues(item)" :key="field.label"><dt>{{field.label}}</dt><dd>{{field.value}}</dd></div>
      <div v-if="item.risk_level!==undefined"><dt>风险等级</dt><dd>{{item.risk_level}}</dd></div>
      <div v-if="item.billing_interval"><dt>记录周期</dt><dd>{{item.billing_interval}}</dd></div>
      <div v-if="item.month_day"><dt>已登记月日</dt><dd>{{item.month_day}}</dd></div>
      <div v-if="item.next_date"><dt>下次日期</dt><dd>{{item.next_date}}</dd></div>
      <div v-if="item.days_until!==undefined"><dt>距离生日</dt><dd>{{item.days_until}} 天</dd></div>
     </dl>
    </li>
   </ul>
   <p v-if="result.truncated" role="status">本次只返回部分记录，请前往业务页面查看完整范围，不将缺失记录当作不存在。</p>
   <ul class="overview-limitations" aria-label="分析局限"><li v-for="(limitation,index) in result.limitations" :key="index">{{limitation}}</li></ul>
   <RouterLink :to="choice.path">{{choice.action}} →</RouterLink>
   <p>请在业务表单明确选择对象和参数，再核对待授权意向；规划确认不等于金融执行授权。</p>
  </template>
 </section>
</template>
<style scoped>
.scenario-overview{line-height:1.75;font-size:14px;overflow-wrap:anywhere}.scenario-overview h4{font-size:16px;margin:16px 0}.scenario-overview p,.overview-limitations{color:#52657b}.overview-resources{list-style:none;padding:0}.overview-resources>li{border:1px solid #e2e8f0;border-radius:8px;padding:12px;margin:10px 0}.overview-resources strong,.overview-resources span{display:block}.overview-resources span{font-size:13px;color:#64748b}.overview-resources dl{display:flex;flex-wrap:wrap;gap:16px;margin:12px 0 0}.overview-resources dt{font-size:12px;color:#64748b}.overview-resources dd{margin:3px 0 0}.scenario-overview a{color:#0d766e;font-weight:600}
</style>
