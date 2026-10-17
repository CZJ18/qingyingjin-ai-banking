// Synthetic browser contract only. No real wallet, bank or model calls.
import {chromium} from '@playwright/test'
import assert from 'node:assert/strict'
import {mkdir,writeFile} from 'node:fs/promises'
const browser=await chromium.launch({headless:true,executablePath:process.env.BANKING_TEST_BROWSER||'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe'})
const origin=process.env.BANKING_UI_ORIGIN||'http://127.0.0.1:5178'
let bill,review,expired=false,confirms=0
const errors=[],calls=[]
const id='11111111-1111-4111-8111-111111111111',slot='22222222-2222-4222-8222-222222222222'
const view=(role)=>({...bill,is_owner:role==='owner',participants:bill.participants.map((p,i)=>({...p,is_me:role==='owner'?i===0:i===1}))})
async function pageFor(role){
 const page=await browser.newPage({viewport:{width:390,height:844}})
 page.on('pageerror',e=>errors.push(e.message))
 await page.route(/\/(?:product\/)?api\/v[12]\//,async route=>{
  assert.equal(role,'owner','single manager must only need one login');
  const r=route.request(),path=new URL(r.url()).pathname,body=r.postDataJSON();calls.push({path,method:r.method()})
  let data=[]
  if(path.endsWith('/auth/refresh'))return route.fulfill({status:401,json:{code:'AUTH_INVALID_CREDENTIALS',message:'fixture'}})
  if(path.endsWith('/auth/capabilities'))data={sms_enabled:false,sms_mode:'disabled',registration_verification_required:false,code_expires_in:300,resend_after:60}
  else if(path.endsWith('/auth/login'))data={access_token:'fixture-'+role,expires_in:900,user:{id:role==='owner'?'41':'42',display_name:'Fixture '+role},phone_masked:'188****0001',phone_verified:false}
  else if(path==='/api/v1/users/me')data={id:role==='owner'?'41':'42',display_name:'Fixture '+role}
  else if(path==='/api/v1/notifications/renewals')data={items:[],settings:{enabled:true,lead_days:3,version:1},last_checked_at:0,capacity_limited:false,unread_count:0}
  else if(path==='/api/v1/aa/bills'){
   if(r.method()==='POST'){
    assert.equal(body.payment_method,'wechat_manual');assert.equal(body.management_mode,'single_manager');assert.equal(body.account_id,undefined)
    assert.deepEqual(body.items,[{name:'Dinner',amount_minor:8000},{name:'Travel',amount_minor:2000}])
    bill={...body,id,owner_id:'41',currency:'CNY',status:'open',version:1,collected_minor:0,outstanding_minor:5000,platform_verified:false,created_at:Date.now(),participants:[{id:'33333333-3333-4333-8333-333333333333',name:'Owner',amount_minor:5000,status:'self',paid_at:0},{id:slot,name:'Payer',amount_minor:5000,status:'awaiting_receipt',paid_at:0}]}
   }
   data=r.method()==='POST'?view(role):bill?[view(role)]:[]
  }else if(path.endsWith('/review')){
   assert.equal(body.expected_version,bill.version);bill.version++
   review={bill:view(role),participant_id:slot,purpose:body.purpose,intent_id:'44444444-4444-4444-8444-444444444444',digest:'a'.repeat(64),nonce:'b'.repeat(64),expires_at:Date.now()+(expired?-1000:600000)};data=review
  }else if(path.endsWith('/confirm')){
   assert.equal(body.nonce,review.nonce);assert.equal(body.digest,review.digest);assert.equal(body.expected_version,review.bill.version)
   confirms++;bill.version++
   if(body.purpose==='report_paid'){bill.participants[1].status='payment_reported'}else{bill.participants[1].status='receipt_confirmed';bill.collected_minor=5000;bill.outstanding_minor=0;bill.status='settled'}
   data=view(role)
  }
  assert(!/\/(authorize|execute)$/.test(path),'manual workflow attempted financial execution')
  await route.fulfill({json:{code:'OK',data}})
 })
 await page.goto(origin+'/security')
 await page.getByRole('button',{name:'登录 / 注册',exact:true}).click()
 await page.getByLabel('手机号',{exact:true}).fill('18800000001');await page.getByLabel('密码',{exact:true}).fill('fixture-password-only')
 await page.getByRole('button',{name:'登录',exact:true}).click();await page.getByRole('dialog').waitFor({state:'hidden'})
 return page
}
try{
 const owner=await pageFor('owner')
 assert.equal(await owner.getByLabel('AA管理模式').inputValue(),'single_manager')
 assert.equal(await owner.getByLabel('AA结算方式').inputValue(),'wechat_manual')
 await owner.getByLabel('AA账单名称').fill('Dinner AA');await owner.getByLabel('AA收款人提示').fill('Owner - verify in WeChat')
 await owner.getByLabel('AA明细1项目').fill('Dinner');await owner.getByLabel('AA明细1金额').fill('80')
 await owner.getByRole('button',{name:'添加消费明细'}).click();await owner.getByLabel('AA明细2项目').fill('Travel');await owner.getByLabel('AA明细2金额').fill('20')
 await owner.getByLabel('AA总金额').fill('100');await owner.getByLabel('AA其他参与人').fill('Payer')
 await owner.getByRole('checkbox',{name:/我确认已垫付/}).check();await owner.getByRole('button',{name:'创建AA账单'}).click();await owner.locator('[data-bill-id]').waitFor()
 assert.equal(await owner.getByLabel('分享AA邀请码').count(),0)
 await owner.getByRole('button',{name:'复制分摊明细给朋友（无需账号）'}).waitFor()
 await owner.getByRole('button',{name:'核对微信到账并确认收款'}).click()
 let d=owner.getByRole('dialog');await d.getByText('消费明细',{exact:true}).waitFor()
 await d.getByText('分摊明细',{exact:true}).waitFor();await d.getByText('Owner - verify in WeChat',{exact:true}).waitFor()
 await d.getByText('单人管理：本次仅记录发起人核实收款，未取得参与人本人确认或微信官方核验。').waitFor()
 const confirm=d.getByRole('button',{name:'确认已核对收款（未获平台核验）'})
 assert(await confirm.isDisabled());assert.equal(confirms,0)
 await d.getByRole('checkbox',{name:'我已逐项核对明细、份额、收款人和金额'}).check();assert(await confirm.isDisabled())
 await d.getByRole('checkbox',{name:'我已在微信核实收到此人的这笔款项'}).check()
 await confirm.click();await d.waitFor({state:'hidden'});assert.equal(confirms,1);assert.equal(bill.collected_minor,5000)
 await owner.getByText('发起人已核对收款 · 未获微信核验',{exact:true}).waitFor()
 assert.equal(calls.filter(c=>c.path.endsWith('/join')||c.path.includes('/invitations/')).length,0)
 assert.equal(calls.filter(c=>c.path.endsWith('/auth/login')).length,1)
 // Expired owner review cannot be confirmed.
 bill.status='open';bill.participants[1].status='awaiting_receipt';expired=true
 await owner.getByRole('button',{name:'刷新进度',exact:true}).click();await owner.getByRole('button',{name:'核对微信到账并确认收款'}).click()
 d=owner.getByRole('dialog');await d.getByText('核对已过期，请关闭后重新读取明细。').waitFor();assert(await d.getByRole('button',{name:'确认已核对收款（未获平台核验）'}).isDisabled())
 assert.deepEqual(errors,[])
 assert.equal(await owner.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
 await mkdir('test-results',{recursive:true})
 const result={passed:true,fixture_only:true,checks:['single-manager and WeChat defaults','itemized creation without account or participant login','no private invite shown; share detail action available','server review includes item totals and every share','two checkboxes before owner receipt','owner-only label never impersonates payer','expired review blocked','one login, no joins or financial execution, mobile layout and no runtime errors'],real_wechat_payment_tested:false}
 await writeFile('test-results/single-manager-aa-contract.json',JSON.stringify(result,null,2));console.log(JSON.stringify(result))
}finally{await browser.close()}
