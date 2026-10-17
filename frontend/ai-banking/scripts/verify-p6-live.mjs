import assert from 'node:assert/strict'
import { mkdir, writeFile, rename } from 'node:fs/promises'
import { chromium } from '@playwright/test'

// No route interception: all browser/business/model results come from the real services.
const base=process.env.BANKING_P6_URL||'http://127.0.0.1:5178',token=process.env.BANKING_P6_JWT,foreign=process.env.BANKING_P6_FOREIGN_JWT
const phone=process.env.BANKING_P6_PHONE,password=process.env.BANKING_P6_PASSWORD,foreignPhone=process.env.BANKING_P6_FOREIGN_PHONE,foreignPassword=process.env.BANKING_P6_FOREIGN_PASSWORD
const fixture=JSON.parse(process.env.BANKING_P6_FIXTURE||'{}')
assert.ok(token&&foreign&&phone&&password&&foreignPhone&&foreignPassword&&fixture.user_id==='1960014006001','Use operator run-p6-live.py for the isolated simulated fixture and private phone credentials')
assert.ok(['127.0.0.1','localhost'].includes(new URL(base).hostname))
const modelIndependent=process.env.BANKING_P6_MODEL_INDEPENDENT==='1';
const report=modelIndependent?'live-independent':'live';
const out='test-results/p6';await mkdir(out,{recursive:true})
try { await rename(out+'/'+report+'.json',out+'/'+report+'-prior-'+Date.now()+'.json') } catch(e) { if(e.code!=='ENOENT')throw e }
const checks=[],errors=[],requests=[],state={...fixture},started=new Date().toISOString()
const browser=await chromium.launch({headless:true,executablePath:process.env.BANKING_TEST_BROWSER})
const page=await browser.newPage({viewport:{width:1440,height:1000}})
// Install before mounting Vue so its interval is controlled by this clock too.
await page.clock.install()
page.setDefaultTimeout(20000)
page.on('pageerror',e=>errors.push(e.message))
page.on('request',r=>{const path=new URL(r.url()).pathname;if(path.startsWith('/api/v')||path.startsWith('/product/api'))requests.push({path,method:r.method()})})
const pass=name=>{checks.push(name);console.log('PASS: '+name)}
async function api(path,body,method=body===undefined?'GET':'POST',auth=token,key=crypto.randomUUID()) {
  const r=await fetch(base+path,{method,headers:{Authorization:'Bearer '+auth,'Content-Type':'application/json','Idempotency-Key':key},body:body===undefined?undefined:JSON.stringify(body)})
  const v=await r.json();assert.ok(r.ok&&v.code==='OK',`${method} ${path}: ${r.status} ${v.code}`);return v.data
}
async function wait(read,check,label){for(let i=0;i<480;i++){const v=await read();if(check(v))return v;await new Promise(r=>setTimeout(r,500))}throw new Error('Timed out: '+label)}
async function nav(name){if(page.viewportSize().width<=900){await page.getByRole('button',{name:'打开导航',exact:true}).click();await page.locator('.sidebar.open').waitFor()}await page.getByRole('link',{name,exact:true}).click();await page.locator('h1').waitFor()}
async function connect(owner='owner'){
  const expectedOwner=owner==='foreign'?'1960014006002':fixture.user_id
  await page.getByRole('button',{name:'登录 / 注册',exact:true}).click()
  await page.getByLabel('手机号',{exact:true}).fill(owner==='foreign'?foreignPhone:phone)
  await page.getByLabel('密码',{exact:true}).fill(owner==='foreign'?foreignPassword:password)
  const response=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/login'&&r.request().method()==='POST')
  await page.getByRole('button',{name:'登录',exact:true}).click()
  const envelope=await(await response).json();assert.equal(envelope.code,'OK','P6 browser phone login failed')
  assert.equal(envelope.data.user.id,expectedOwner,'P6 phone login returned a different isolated owner')
  assert.equal(JSON.parse(Buffer.from(envelope.data.access_token.split('.')[1],'base64url').toString()).sub,expectedOwner,'P6 phone access-token subject mismatch')
  await page.getByText('已登录',{exact:true}).waitFor()
}
async function refs(){return page.evaluate(()=>JSON.parse(localStorage.getItem('qingrongjin.refs.v1.1960014006001')||'{}'))}
async function balance(){return api('/api/v1/accounts?limit=100')}
async function prepareTransfer(amount,description){await page.getByRole('button',{name:'准备模拟转账',exact:true}).click();await page.getByLabel('付款账户',{exact:true}).selectOption(fixture.account_id);await page.getByLabel('收款账户',{exact:true}).selectOption(fixture.target_account_id);await page.getByLabel('转账金额（元）',{exact:true}).fill(amount);await page.getByLabel('用途',{exact:true}).fill(description);await page.getByRole('button',{name:'创建待确认准备任务',exact:true}).click();await page.getByRole('region',{name:'任务准备确认'}).waitFor({timeout:60000});return (await refs()).agents[0]}
async function finishPreparation(){const confirmation=page.getByRole('region',{name:'任务准备确认'});assert.ok(await confirmation.getByRole('button',{name:'确认准备意向',exact:true}).isDisabled());await confirmation.getByRole('checkbox').check();await confirmation.getByRole('button',{name:'确认准备意向',exact:true}).click();await page.getByRole('button',{name:'核对金融意向',exact:true}).waitFor({timeout:60000});await page.getByRole('button',{name:'核对金融意向',exact:true}).click();await page.getByRole('dialog').getByRole('checkbox').waitFor();assert.ok(await page.getByRole('dialog').getByRole('button',{name:'授权并执行',exact:true}).isDisabled())}
try {
  assert.equal((await api('/api/v1/users/me')).id,fixture.user_id)
  assert.equal((await api('/product/api/v1/me')).user_id,fixture.user_id)
  const profileBefore=await api('/product/api/v1/profile')
  state.preference_minor=profileBefore.safety_reserve_minor===10029?10031:10029
  const preferenceText=(state.preference_minor/100).toFixed(2)
  await page.goto(base);await connect();await page.locator('.toolbar-actions select').selectOption(fixture.account_id)
  await page.getByLabel('资金安全预留偏好 / 元',{exact:true}).fill(preferenceText);await page.getByRole('button',{name:'保存资金偏好',exact:true}).click()
  await wait(()=>api('/product/api/v1/profile'),p=>p.safety_reserve_minor===state.preference_minor&&p.revision>profileBefore.revision,'profile persistence')
  pass('Same-origin Go/FastAPI authenticated identity and exact persistent preference')

  await nav('动态资金规划');await page.locator('.planning-goal-selection input').first().check()
  for(const [label,value] of [['正常生活开支预留 / 元','300'],['必要支出预留 / 元','600'],['现金缓冲 / 元','100'],['基准月收入 / 元','1000']])await page.getByLabel(label,{exact:true}).fill(value)
  await page.getByRole('button',{name:'应用资金安全偏好',exact:true}).click();await wait(()=>page.getByLabel('安全储备 / 元',{exact:true}).inputValue(),v=>v===preferenceText,'preference form applied')
  await page.getByRole('button',{name:'保存并计算方案',exact:true}).click();await page.locator('.option-list button').first().waitFor()
  state.financial_plan_id=(await refs()).plan
  const financial=await api('/api/v1/financial-plans/'+state.financial_plan_id)
  assert.equal(financial.result.options.length,3);assert.equal(financial.plan.settings.safety_reserve_minor,state.preference_minor)
  pass('Three deterministic financial options with explicit preference application')

  if(!modelIndependent){
  await nav('健康目标与目标金')
  const goalUI=page.getByRole('region',{name:'健康生活目标编译器'})
  await goalUI.getByLabel('描述你的生活和资金目标',{exact:true}).fill('未来三个月保持正常饮食和规律运动，可调整购物预算，每月少花100元，同时每月合理储蓄200元，不削减正常饮食和必要生活开支，不允许自动转账或取消订阅。')
  await goalUI.getByRole('button',{name:'解析生活目标',exact:true}).click();await page.waitForFunction(()=>document.querySelector('.lifestyle-v2 .p6-parse-result')||document.querySelector('.lifestyle-v2 .form-error'),{},{timeout:180000});assert.equal(await goalUI.locator('.form-error').count(),0,await goalUI.locator('.form-error').allTextContents().then(x=>x.join(';')));await goalUI.getByRole('checkbox',{name:'我已核对上述目标和约束'}).waitFor()
  await goalUI.getByRole('checkbox').check();await goalUI.getByRole('button',{name:'确认并保存生活目标',exact:true}).click();await goalUI.getByRole('button',{name:'编译生活资金方案',exact:true}).waitFor()
  state.lifestyle_goal_id=(await refs()).lifestyle[0]
  const actualGoal=await api('/api/v2/lifestyle-goals/'+state.lifestyle_goal_id)
  const spec=actualGoal.versions.find(v=>v.version===actualGoal.goal.current_version).spec
  assert.equal(spec.savings_monthly_minor,20000);assert.equal(spec.expense_reduction_monthly_minor,10000)
  assert.ok(spec.protected_categories.includes('正常饮食'))
  await goalUI.getByRole('button',{name:'编译生活资金方案',exact:true}).click();await goalUI.getByText(/需要补充：/).waitFor()
  pass('Real Coze V2 parsing distinguishes savings/reduction, protects food and returns missing inputs')
  await goalUI.getByLabel('预算数据',{exact:true}).selectOption(fixture.budget_id);await goalUI.getByLabel('账单月份',{exact:true}).fill('2026-09')
  await goalUI.getByRole('button',{name:'生成账单快照',exact:true}).click();await wait(refs,r=>!!r.billingReport,'billing snapshot')
  await goalUI.getByRole('button',{name:'编译生活资金方案',exact:true}).click();await page.locator('.p6-lifestyle-options article').first().waitFor()
  state.lifestyle_plan_id=(await refs()).lifestylePlan
  const lifestyle=await api('/api/v2/lifestyle-plans/'+state.lifestyle_plan_id)
  assert.equal(lifestyle.plan.result.options.length,3);assert.equal(lifestyle.plan.result.financial_execution_enabled,false)
  assert.equal(lifestyle.plan.result.rule_version,'lifestyle-2')
  assert.deepEqual(lifestyle.plan.result.options.map(o=>o.expense_reduction_minor),[5000,7500,10000])
  assert.ok(lifestyle.plan.snapshot.evidence_transaction_ids.length>=3)
  for(const option of lifestyle.plan.result.options)for(const c of option.categories.filter(c=>c.protected))assert.equal(c.proposed_minor,c.original_minor)
  await page.screenshot({path:out+'/lifestyle-desktop.png',fullPage:true})
  assert.equal(lifestyle.recommendations[2].status,'pending')
  await page.clock.setFixedTime(new Date(lifestyle.recommendations[2].expires_at+1000))
  await page.clock.runFor(1100)
  const expiredOption=page.locator('.p6-lifestyle-options article').nth(2)
  assert.ok(await expiredOption.getByRole('button',{name:'接受建议',exact:true}).isDisabled())
  assert.ok(await expiredOption.getByRole('button',{name:'拒绝建议',exact:true}).isDisabled())
  await page.clock.setFixedTime(new Date())
  await page.clock.runFor(1100)
  pass('Pending recommendation expiry disables feedback without another data response')
  await page.locator('.p6-lifestyle-options article').first().getByRole('button',{name:'拒绝建议',exact:true}).click()
  await wait(()=>api('/api/v2/lifestyle-plans/'+state.lifestyle_plan_id),p=>p.recommendations[0].status==='rejected','reject feedback')
  await page.locator('.p6-lifestyle-options article').nth(1).getByRole('button',{name:'接受建议',exact:true}).click()
  await wait(()=>api('/api/v2/lifestyle-plans/'+state.lifestyle_plan_id),p=>p.recommendations[1].status==='accepted','accept feedback persisted')
  await wait(()=>api('/product/api/v1/recommendations'),r=>r.some(x=>x.payload.status==='accepted'),'projection feedback')
  pass('V2 three-option evidence, protected spending, accepted/rejected feedback and PG projection')

  await nav('AI 智能金融助手');await page.getByLabel('金融任务指令',{exact:true}).fill('分析我的近期账单，解释消费分类和异常记录，必须提供实际数据依据。')
  await page.getByRole('button',{name:'发送',exact:true}).click()
  state.model_task_id=await wait(refs,r=>!!r.agents?.[0],'natural task').then(r=>r.agents[0])
  const modelTask=await wait(()=>api('/product/api/v1/agent-tasks/'+state.model_task_id),t=>['succeeded','failed'].includes(t.status),'real supervisor and specialist model task')
  assert.equal(modelTask.status,'succeeded',modelTask.error_code)
  assert.ok(modelTask.result.outputs.some(o=>o.agent_type==='billing'))
  assert.ok(modelTask.result.outputs.some(o=>o.workflow_execute_id||o.workflow_id))
  await page.screenshot({path:out+'/assistant-desktop.png',fullPage:true})
  pass('Real DeepSeek/Coze Supervisor compiles and runs specialist LangGraph task with evidence')

  }
  await nav('多智能体协同');const before=await balance(),count=requests.length
  state.transfer_task_id=await prepareTransfer('0.29','P6二阶段授权验收')
  assert.deepEqual((await balance()).map(a=>[a.id,a.balance_minor]),before.map(a=>[a.id,a.balance_minor]))
  assert.equal(requests.slice(count).filter(r=>r.path.endsWith('/authorize')||r.path.endsWith('/execute')).length,0)
  await page.screenshot({path:out+'/prepare-confirmation.png',fullPage:true})
  await finishPreparation()
  assert.deepEqual((await balance()).map(a=>[a.id,a.balance_minor]),before.map(a=>[a.id,a.balance_minor]))
  assert.equal(requests.slice(count).filter(r=>r.path.endsWith('/authorize')||r.path.endsWith('/execute')).length,0)
  await page.screenshot({path:out+'/financial-confirmation.png'})
  const dialog=page.getByRole('dialog');await dialog.getByRole('checkbox').check();await dialog.getByRole('button',{name:'授权并执行',exact:true}).dblclick();await dialog.locator('.status.succeeded').waitFor()
  const after=await balance()
  assert.equal(after.find(a=>a.id===fixture.account_id).balance_minor,before.find(a=>a.id===fixture.account_id).balance_minor-29)
  assert.equal(after.find(a=>a.id===fixture.target_account_id).balance_minor,before.find(a=>a.id===fixture.target_account_id).balance_minor+29)
  assert.equal(requests.slice(count).filter(r=>r.path.endsWith('/execute')).length,1)
  const operations=await api('/api/v1/execution/operations?kind=transfer')
  const op=operations.filter(o=>o.parameters.description==='P6二阶段授权验收'&&o.status==='succeeded').sort((a,b)=>b.created_at-a.created_at)[0]
  state.executed_operation_id=op.id;assert.ok((await api(`/api/v1/execution/operations/${op.id}/audits`)).length)
  await dialog.getByRole('button',{name:'关闭',exact:true}).last().click()
  pass('PREPARE_ONLY changes no funds; separate double-click-safe 29-cent execution balances and audits')

  const rejectStart=requests.length,rejectBalances=await balance();state.rejected_prepare_task_id=await prepareTransfer('0.31','P6拒绝准备验收')
  await page.getByRole('region',{name:'任务准备确认'}).getByRole('button',{name:'拒绝并结束任务',exact:true}).click()
  assert.equal((await wait(()=>api('/product/api/v1/agent-tasks/'+state.rejected_prepare_task_id),t=>t.status==='cancelled','preparation rejection')).status,'cancelled')
  assert.equal(requests.slice(rejectStart).filter(r=>r.path.endsWith('/authorize')||r.path.endsWith('/execute')).length,0)
  assert.deepEqual((await balance()).map(a=>[a.id,a.balance_minor]),rejectBalances.map(a=>[a.id,a.balance_minor]))
  state.rejected_financial_task_id=await prepareTransfer('0.33','P6金融授权拒绝验收');await finishPreparation();await page.getByRole('dialog').getByRole('button',{name:'拒绝此操作',exact:true}).click();await page.getByRole('dialog').locator('.status.rejected').waitFor();await page.getByRole('dialog').getByRole('button',{name:'关闭',exact:true}).last().click()
  assert.deepEqual((await balance()).map(a=>[a.id,a.balance_minor]),rejectBalances.map(a=>[a.id,a.balance_minor]))
  pass('Reject either preparation or financial authorization without changing balances')

  const unauthorized=await fetch(base+'/product/api/v1/agent-tasks/'+state.transfer_task_id,{headers:{Authorization:'Bearer '+foreign}})
  assert.ok([403,404].includes(unauthorized.status));const foreignAccount=await fetch(base+'/api/v1/accounts/'+fixture.account_id,{headers:{Authorization:'Bearer '+foreign}});assert.ok([403,404].includes(foreignAccount.status))
  const actor=await api('/product/api/v1/agent-tasks',{tasks:[{key:'cancel',skill_id:'subscription_analysis',skill_version:1,agent_type:'subscription',tool:'detect_subscriptions',execution_mode:'read_only',parameters:{}}]})
  const cancelled=await api('/product/api/v1/agent-tasks/'+actor.task_id+'/cancel',{expected_version:actor.version});assert.equal(cancelled.status,'cancelled')
  pass('Actual foreign task/account rejection and durable user cancellation')

  assert.ok(requests.some(r=>r.path.endsWith('/events/stream')),'Actual browser SSE')
  await page.reload();await page.getByText('已登录',{exact:true}).waitFor()
  const restored=await refs();assert.ok(restored.agents.includes(state.transfer_task_id));if(!modelIndependent)assert.equal(restored.lifestyle[0],state.lifestyle_goal_id)
  await page.getByRole('button',{name:'退出登录',exact:true}).click();await connect('foreign')
  await page.getByLabel('任务编号',{exact:true}).fill(state.transfer_task_id);await page.getByRole('button',{name:'查询任务',exact:true}).click();await page.getByRole('alert').filter({hasText:/not found|does not exist|不存在|未找到/}).first().waitFor()
  const foreignRefs=await page.evaluate(()=>JSON.parse(localStorage.getItem('qingrongjin.refs.v1.1960014006002')||'{}'))
  assert.ok(!foreignRefs.plan);assert.equal(foreignRefs.lifestyle?.length||0,0)
  await page.getByRole('button',{name:'退出登录',exact:true}).click();await connect()
  pass('Cookie reload restores authenticated owner references; phone/password cross-user UI returns a real error')
  await page.setViewportSize({width:390,height:844})
  for(const name of ['AI Banking 总览','健康目标与目标金','多智能体协同','AI 智能金融助手','智能账单分析','订阅与代扣','动态资金规划','可信执行与安全']){
    await nav(name);await page.waitForTimeout(700)
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'Mobile overflow: '+name)
    if(['AI Banking 总览','健康目标与目标金','AI 智能金融助手','多智能体协同'].includes(name))await page.screenshot({path:out+'/mobile-'+({'AI Banking 总览':'overview','健康目标与目标金':'goals','AI 智能金融助手':'assistant','多智能体协同':'agents'}[name])+'.png',fullPage:true})
  }
  assert.equal(errors.length,0,errors.join(';'))
  const local=await page.evaluate(()=>JSON.stringify({...localStorage}));assert.ok(!local.includes(token)&&!local.includes(foreign)&&!local.includes(password)&&!local.includes(foreignPassword)&&!local.includes(phone)&&!local.includes(foreignPhone)&&!local.includes('access_token')&&!local.includes('authorization_id')&&!local.includes('confirmation_nonce'))
  pass('All eight mobile routes render without overflow/runtime errors; no persisted credentials')
  await writeFile(out+'/'+report+'.json',JSON.stringify({passed:true,scope:modelIndependent?'model_independent_only':'full',model_checks_skipped:modelIndependent,fixture_only:false,response_interception:false,started_at:started,completed_at:new Date().toISOString(),checks,state,runtime_errors:errors,api_request_count:requests.length},null,2)+'\n')
} catch(e){await page.screenshot({path:out+'/live-failure.png',fullPage:true});await writeFile(out+'/'+report+'.json',JSON.stringify({passed:false,fixture_only:false,started_at:started,checks,state,error:e.message,runtime_errors:errors},null,2)+'\n');console.error(e.message);process.exitCode=1}
finally {await browser.close()}
