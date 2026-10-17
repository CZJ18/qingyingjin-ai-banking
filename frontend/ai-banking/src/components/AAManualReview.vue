<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { DialogRoot, DialogPortal, DialogOverlay, DialogContent, DialogTitle, DialogDescription, DialogClose } from 'reka-ui'
import { aaApi } from '@/api/aa'
import type { Schema } from '@/api/client'
import { useSession, useUI } from '@/lib/store'
import { money } from '@/lib/utils'
import Button from '@/components/ui/button/Button.vue'
const emit = defineEmits<{ changed: [] }>()
const session=useSession(),ui=useUI(),open=ref(false),review=ref<Schema<'AAManualReview'>>(),checked=ref(false),completed=ref(false),busy=ref(false),error=ref(''),now=ref(Date.now())
let timer:ReturnType<typeof setInterval>|undefined
onMounted(()=>{timer=setInterval(()=>now.value=Date.now(),1000)})
onUnmounted(()=>clearInterval(timer))
watch(()=>session.sessionEpoch,()=>{open.value=false;review.value=undefined;checked.value=false;completed.value=false})
const participant=computed(()=>review.value?.bill.participants.find(p=>p.id===review.value?.participant_id))
const expired=computed(()=>!review.value||review.value.expires_at<=now.value)
const receiving=computed(()=>review.value?.purpose==='confirm_received')
async function inspect(b:Schema<'AABillView'>,p:Schema<'AAParticipant'>,purpose:'report_paid'|'confirm_received') {
 const epoch=session.sessionEpoch
 open.value=true;checked.value=false;completed.value=false;review.value=undefined;error.value='';busy.value=true
 try {const value=await aaApi.reviewManual(b.owner_id,b.id,{participant_id:p.id,purpose,expected_version:b.version});if(session.sessionEpoch!==epoch)return;review.value=value;now.value=Date.now();emit('changed')}
 catch(e){if(session.sessionEpoch===epoch)error.value=(e as Error).message}
 finally{if(session.sessionEpoch===epoch)busy.value=false}
}
async function copyDetails(){
 const r=review.value,p=participant.value;if(!r||!p||!checked.value||expired.value)return
 const text=[`AA账单：${r.bill.title}`,`收款人提示：${r.bill.recipient_hint}`,`账单总额：${money(r.bill.total_minor)}`,...(r.bill.items||[]).map(i=>`${i.name}：${money(i.amount_minor)}`),`本人份额：${p.name} ${money(p.amount_minor)}`,`备注：${r.bill.memo||''}`,'请在微信独立核对真实收款人及金额；本应用没有平台支付核验。'].join('\n')
 try{await navigator.clipboard.writeText(text);ui.notify('明细已复制，请自行打开微信并核对后付款')}catch{error.value='无法复制，请按页面明细手动填写微信转账；不要重复付款'}
}
async function confirm(){
 const r=review.value;if(!r||!checked.value||!completed.value||expired.value||busy.value)return
 const epoch=session.sessionEpoch;busy.value=true;error.value=''
 try{await aaApi.confirmManual(r.bill.owner_id,r.bill.id,{participant_id:r.participant_id,purpose:r.purpose,expected_version:r.bill.version,intent_id:r.intent_id,digest:r.digest,nonce:r.nonce});if(session.sessionEpoch!==epoch)return;open.value=false;emit('changed');ui.notify(receiving.value?'已记录发起人核对收款，未经微信平台核验':'已记录本人自报，等待发起人核对收款')}
 catch(e){if(session.sessionEpoch===epoch){error.value=(e as Error).message;completed.value=false;emit('changed')}}
 finally{if(session.sessionEpoch===epoch)busy.value=false}
}
defineExpose({inspect})
</script>
<template>
 <DialogRoot v-model:open="open"><DialogPortal><DialogOverlay class="aa-review-overlay"/><DialogContent class="aa-review-dialog">
  <DialogTitle>{{ receiving?'核对微信到账明细':'微信付款前核对明细' }}</DialogTitle>
  <DialogDescription>这是微信线下结算记录。应用不会自动扣款，也未取得微信官方支付回执。</DialogDescription>
  <p v-if="busy&&!review">正在读取服务器明细…</p><p v-if="error" role="alert">{{ error }}</p>
  <template v-if="review&&participant">
   <dl><dt>账单</dt><dd>{{ review.bill.title }}</dd><dt>收款人提示（非实名认证）</dt><dd>{{ review.bill.recipient_hint }}</dd><dt>总金额</dt><dd>{{ money(review.bill.total_minor) }}</dd></dl>
   <p v-if="review.bill.management_mode==='single_manager'">单人管理：本次仅记录发起人核实收款，未取得参与人本人确认或微信官方核验。</p>
   <h3>消费明细</h3><ul><li v-for="(i,index) in review.bill.items" :key="index">{{ i.name }} <b>{{ money(i.amount_minor) }}</b></li></ul>
   <h3>分摊明细</h3><ul><li v-for="p in review.bill.participants" :key="p.id">{{ p.name }} <b>{{ money(p.amount_minor) }}</b></li></ul>
   <p class="aa-review-amount">{{ receiving?'本次应核对到账':'本人应付' }}：{{ participant.name }} · {{ money(participant.amount_minor) }}</p><p>备注：{{ review.bill.memo||'无' }}</p>
   <p>账单版本 {{ review.bill.version }} · 本次核对有效至 {{ new Date(review.expires_at).toLocaleTimeString() }}</p>
   <label><input v-model="checked" type="checkbox"/> 我已逐项核对明细、份额、收款人和金额</label>
   <template v-if="!receiving"><Button variant="outline" :disabled="!checked||expired||busy" @click="copyDetails">复制付款明细，前往微信手动转账</Button><p>请自行打开微信，选择真实收款人，填写金额并在微信确认。返回本页不会自动记为成功。</p></template>
   <label><input v-model="completed" type="checkbox"/> {{ receiving?'我已在微信核实收到此人的这笔款项':'我已在微信完成这笔转账，现仅提交本人自报' }}</label>
   <p v-if="expired" role="alert">核对已过期，请关闭后重新读取明细。</p>
   <Button :disabled="!checked||!completed||expired||busy" @click="confirm">{{ receiving?'确认已核对收款（未获平台核验）':'提交我已付款，等待收款人核对' }}</Button>
  </template><DialogClose as-child><Button variant="outline">关闭，不提交确认</Button></DialogClose>
 </DialogContent></DialogPortal></DialogRoot>
</template>
<style scoped>
.aa-review-overlay{position:fixed;inset:0;background:#102c4666;z-index:80}.aa-review-dialog{position:fixed;top:50%;left:50%;transform:translate(-50%,-50%);width:min(600px,calc(100vw - 24px));max-height:90dvh;overflow:auto;background:white;border-radius:18px;padding:26px;z-index:81;color:#173251;overflow-wrap:anywhere}.aa-review-dialog h2{font-size:21px}.aa-review-dialog h3{margin:18px 0 8px}.aa-review-dialog p{font-size:13px;line-height:1.8}.aa-review-dialog dl{display:grid;grid-template-columns:1fr 2fr;gap:8px;margin:18px 0}.aa-review-dialog dd{margin:0}.aa-review-dialog li{display:flex;justify-content:space-between;gap:16px;padding:8px 0;border-bottom:1px solid #edf1f5}.aa-review-dialog ul{padding:0;list-style:none}.aa-review-dialog label{display:flex;gap:10px;margin:18px 0;font-size:14px;align-items:flex-start}.aa-review-dialog button{margin:6px 6px 6px 0;white-space:normal;height:auto;min-height:40px}.aa-review-amount{font-weight:700;font-size:18px!important}
</style>
