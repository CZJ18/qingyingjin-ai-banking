// Opt-in real product test. No routing interception or model response substitution.
import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'
import { chromium } from '@playwright/test'

const base=process.env.BANKING_CLOSURE_URL, out=process.env.BANKING_CLOSURE_OUT
const fixture=JSON.parse(process.env.BANKING_CLOSURE_FIXTURE||'{}')
assert.ok(base&&out&&fixture.user_id&&['127.0.0.1','localhost'].includes(new URL(base).hostname))
let token=process.env.BANKING_CLOSURE_JWT
const report={started_at:new Date().toISOString(),network_interception:false,scenarios:{},requests:[],page_errors:[]}
const browser=await chromium.launch({headless:true,executablePath:process.env.BANKING_TEST_BROWSER})
const page=await browser.newPage({viewport:{width:1440,height:1000}})
page.setDefaultTimeout(20000)
page.on('pageerror',e=>report.page_errors.push(e.message))
page.on('response',r=>{const path=new URL(r.url()).pathname;if(path.startsWith('/api/')||path.startsWith('/product/api/'))report.requests.push({method:r.request().method(),path,status:r.status()})})
function clean(value){if(Array.isArray(value))return value.map(clean);if(value&&typeof value==='object')return Object.fromEntries(Object.entries(value).filter(([k])=>!['nonce','confirmation_nonce','authorization_id','authorizations','access_token','refresh_token'].includes(k)).map(([k,v])=>[k,clean(v)]));return value}
async function save(){await writeFile(out+'/browser-product.json',JSON.stringify(clean(report),null,2))}
async function api(path,body,method=body===undefined?'GET':'POST'){
  const r=await fetch(base+path,{method,headers:{Authorization:'Bearer '+token,'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()},body:body===undefined?undefined:JSON.stringify(body)})
  const v=await r.json();assert.ok(r.ok&&v.code==='OK',`${path}: ${r.status} ${v.code}`);return v.data
}
async function nav(name){await page.getByRole('link',{name,exact:true}).click();await page.locator('h1').waitFor()}
async function task(instruction,monthly=false){
  await nav('AI 智能金融助手')
  await page.getByRole('button',{name:'新任务',exact:true}).click()
  await page.getByRole('button',{name:monthly?'本月目标':'一般任务',exact:true}).click()
  await page.locator('#task-input').fill(instruction)
  const submitted=page.waitForResponse(r=>new URL(r.url()).pathname==='/product/api/v1/agent-tasks'&&r.request().method()==='POST',{timeout:200000})
  const ran=page.waitForResponse(r=>new URL(r.url()).pathname.endsWith('/run')&&r.request().method()==='POST',{timeout:200000})
  await page.getByRole('button',{name:'发送',exact:true}).click()
  const sr=await submitted,sv=await sr.json();assert.equal(sr.status(),202,JSON.stringify(sv))
  const rr=await ran,rv=await rr.json();assert.equal(rr.status(),200,JSON.stringify(rv))
  const value=rv.data
  const model=value.result?.outputs?.find(o=>o.agent_type==='supervisor'&&o.workflow_execute_id)
  // While paused at review, the supervisor model evidence lives in its call journal.
  if(value.status==='succeeded')assert.ok(model?.workflow_execute_id,'real Supervisor execute ID required')
  report.last_task=value;await save()
  return value
}
const focus=process.env.BANKING_CLOSURE_FOCUS?.split(',')
async function scenario(name,work){if(focus&&!focus.includes(name)){report.scenarios[name]={status:'NOT TESTED'};return}try{report.scenarios[name]={status:'PASS',evidence:await work()}}catch(e){report.scenarios[name]={status:'FAIL',error:String(e.message).slice(0,1000)}}await save();console.log(name+': '+report.scenarios[name].status)}

try{
  await mkdir(out,{recursive:true});await page.goto(base)
  await page.getByRole('button',{name:'登录 / 注册',exact:true}).click()
  await page.getByLabel('手机号',{exact:true}).fill(process.env.BANKING_CLOSURE_PHONE)
  await page.getByLabel('密码',{exact:true}).fill(process.env.BANKING_CLOSURE_PASSWORD)
  const login=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/auth/login')
  await page.getByRole('button',{name:'登录',exact:true}).click()
  const loginResult=await (await login).json();token=loginResult.data.access_token
  assert.equal(loginResult.data.user.id,fixture.user_id)
  await page.getByLabel('当前账户',{exact:true}).selectOption(fixture.account_id)

  await scenario('A_natural_billing',async()=>{
    const value=await task('帮我分析最近的消费，看看主要花在哪里，有哪些可以优化的地方。')
    assert.equal(value.status,'succeeded',value.error_code)
    const billing=value.result.outputs.find(o=>o.agent_type==='billing')
    assert.ok(billing);assert.equal(billing.result.total_expense,fixture.expected_expense_minor)
    assert.ok(billing.evidence.length);await page.getByText('任务已完成',{exact:true}).first().waitFor()
    await page.screenshot({path:out+'/A-billing.png',fullPage:true})
    return value
  })
  await scenario('B_subscription_detection',async()=>{
    await nav('订阅与代扣')
    const pending=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/subscriptions/detect_subscriptions')
    await page.getByRole('button',{name:'识别近期订阅',exact:true}).click()
    const response=await pending,v=await response.json();assert.equal(response.status(),200,JSON.stringify(v))
    const found=v.data.subscriptions.find(i=>i.subscription.merchant_name==='Closure monthly service')
    assert.ok(found);assert.equal(found.subscription.amount_minor,3900);assert.equal(found.subscription.billing_interval,'monthly')
    assert.equal(found.payments.length,3)
    await page.getByText('Closure monthly service',{exact:true}).first().waitFor()
    await page.screenshot({path:out+'/B-subscriptions.png',fullPage:true})
    const invalid=await fetch(base+'/api/v1/subscriptions/detect_subscriptions',{method:'POST',headers:{Authorization:'Bearer '+token,'Content-Type':'application/json'},body:JSON.stringify({from:'invalid'})})
    assert.equal(invalid.status,400)
    return {report:v.data,invalid_range_status:invalid.status}
  })
  await scenario('B_natural_subscription',async()=>{
    const value=await task('请识别近期周期性消费，基于真实扣款证据检查订阅费用并给出优化建议。识别使用服务端默认近三个月窗口，不限定本月，不进行取消或任何资金操作。')
    assert.equal(value.status,'succeeded',value.error_code)
    const results=value.result.outputs.filter(o=>o.agent_type==='subscription')
    for(const tool of ['detect_subscriptions','suggest_subscription_actions'])assert.ok(results.some(o=>o.result.tool===tool&&o.evidence.length),tool+' not executed')
    assert.ok(results.some(o=>o.result.total_recorded_expense_minor===11700))
    assert.equal(value.result.financial_execution_enabled,false)
    await page.screenshot({path:out+'/B-natural-subscriptions.png',fullPage:true});return value
  })
  // Restore a server-validated existing plan through its normal product control.
  await nav('动态资金规划');await page.locator('.plan-lookup summary').click()
  await page.getByLabel('规划编号',{exact:true}).fill(fixture.plan_id)
  await page.getByRole('button',{name:'查询规划',exact:true}).click()
  await page.locator('.option-list button').first().waitFor()

  await scenario('C_monthly_savings',async()=>{
    const value=await task('本月希望储蓄1000元，保留正常生活支出和安全储备。请读取已绑定的资金方案，结合账户和账单提出建议，等待我最终确认，不自动执行任何金融操作。',true)
    assert.equal(value.status,'waiting_confirmation',value.error_code)
    assert.equal(value.request.monthly_goal.period,'2026-10')
    assert.equal(value.confirmation.confirmation_scope,'PLAN_ACCEPTANCE')
    const review=page.getByRole('region',{name:'本月方案最终确认',exact:true})
    await review.getByRole('checkbox').check()
    const resumed=page.waitForResponse(r=>new URL(r.url()).pathname.endsWith('/resume'),{timeout:200000})
    await review.getByRole('button',{name:'确认方案',exact:true}).click()
    const final=(await (await resumed).json()).data;assert.equal(final.status,'succeeded',final.error_code)
    const accept=final.result.outputs.find(o=>o.task_id.endsWith('.monthly_plan_review')).result
    assert.equal(accept.plan_accepted,true);assert.equal(accept.authorization_granted,false)
    assert.equal(final.result.financial_execution_enabled,false)
    assert.ok(final.result.outputs.some(o=>o.workflow_execute_id),'real model planning evidence required')
    await page.screenshot({path:out+'/C-monthly.png',fullPage:true});return final
  })
  await scenario('C_goal_compiler',async()=>{
    await nav('健康目标与目标金')
    const region=page.getByRole('region',{name:'健康生活目标编译器',exact:true})
    await region.getByLabel('描述你的生活和资金目标',{exact:true}).fill('未来三个月保持正常饮食和规律运动，可调整购物预算，每月少花100元，同时每月合理储蓄1000元，不削减正常饮食和必要生活开支，不允许自动转账或取消订阅。')
    const response=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v2/lifestyle-goals/parse',{timeout:200000})
    await region.getByRole('button',{name:'解析生活目标',exact:true}).click()
    const r=await response,v=await r.json();assert.equal(r.status(),200,JSON.stringify(v))
    assert.equal(v.data.status,'parsed');assert.equal(v.data.spec.savings_monthly_minor,100000)
    assert.equal(v.data.spec.expense_reduction_monthly_minor,10000);assert.ok(v.data.execute_id)
    await region.getByRole('checkbox',{name:'我已核对上述目标和约束'}).check()
    await region.getByRole('button',{name:'确认并保存生活目标',exact:true}).click()
    await region.getByRole('button',{name:'编译生活资金方案',exact:true}).waitFor()
    await region.getByLabel('预算数据',{exact:true}).selectOption(fixture.budget_id)
    await region.getByLabel('账单月份',{exact:true}).fill('2026-09')
    const billed=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/billing/generate_billing_report')
    await region.getByRole('button',{name:'生成账单快照',exact:true}).click()
    assert.equal((await billed).status(),200)
    const compiled=page.waitForResponse(r=>/\/api\/v2\/lifestyle-goals\/[^/]+\/compile$/.test(new URL(r.url()).pathname))
    await region.getByRole('button',{name:'编译生活资金方案',exact:true}).click()
    const cr=await compiled,cv=await cr.json();assert.equal(cr.status(),201,JSON.stringify(cv))
    await writeFile(out+'/C-goal-actual.json',JSON.stringify({parsed:v.data,compiled:cv.data},null,2))
    assert.equal(cv.data.plan.result.status,'ready',JSON.stringify(cv.data.plan.result))
    assert.equal(cv.data.plan.result.options.length,3)
    for(const option of cv.data.plan.result.options){
      assert.equal(option.savings_monthly_minor,100000)
      assert.equal(option.categories.find(c=>c.category==='food'||c.name==='food').proposed_minor,80000)
    }
    await page.screenshot({path:out+'/C-goal.png',fullPage:true});return {parsed:v.data,compiled:cv.data}
  })
  await scenario('D_cross_agent',async()=>{
    const value=await task('请分别完成本月账单分析、订阅费用检查和已有资金方案查询。账单使用generate_billing_report，订阅使用analyze_subscription_cost，储蓄使用get_financial_plan并读取绑定的计划。请让三个专业Agent查询真实数据后汇总，不创建意向，不执行金融操作。')
    assert.equal(value.status,'succeeded',value.error_code)
    for(const role of ['billing','subscription','savings'])assert.ok(value.result.outputs.some(o=>o.agent_type===role&&o.evidence.length),role+' missing')
    assert.equal(value.result.financial_execution_enabled,false)
    await page.screenshot({path:out+'/D-agents.png',fullPage:true});return value
  })
  await scenario('billing_scope',async()=>{
    await nav('智能账单分析')
    await page.waitForFunction(()=>document.querySelector('main')?.textContent.includes('339.00'))
    const text=await page.locator('main').innerText()
    assert.ok(!text.includes('当前分页收入超过周期汇总')&&!text.includes('当前分页支出超过周期汇总'))
    return {visible_expense:339,integrity_false_positive:false}
  })
  report.page_title=await page.title();report.final_url=page.url()
  assert.equal(report.page_errors.length,0)
}catch(e){report.fatal=String(e.message).slice(0,1000)}finally{await save();await browser.close()}
process.exitCode=report.fatal||Object.values(report.scenarios).some(s=>s.status==='FAIL')?1:0
