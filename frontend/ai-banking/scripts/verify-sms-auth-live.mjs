import assert from 'node:assert/strict'
import { createHmac, randomBytes, randomUUID } from 'node:crypto'
import { execFile } from 'node:child_process'
import { readFile, mkdir, writeFile } from 'node:fs/promises'
import { promisify } from 'node:util'
import { fileURLToPath } from 'node:url'
import { resolve } from 'node:path'
import { chromium } from '@playwright/test'

// Operator acceptance only. No request/response interception, cloud SMS, test
// code endpoint, arbitrary Redis mutation or credentials persisted to artifacts.
const execute = promisify(execFile)
const root = fileURLToPath(new URL('../../../', import.meta.url))
const base = process.env.BANKING_SMS_URL || 'http://127.0.0.1:8088'
assert.ok(['127.0.0.1', 'localhost'].includes(new URL(base).hostname))
const out = resolve(root, 'output/sms-auth/live')
await mkdir(out, { recursive: true })
const envFile = resolve(root, 'deploy/banking-postgres/finance.app.env.local')
const smsFile = resolve(root, 'deploy/banking/sms.env.local')
const operator = Object.fromEntries((await readFile(envFile, 'utf8')).replace(/^\uFEFF/, '').split(/\r?\n/).filter((l) => l && !l.startsWith('#') && l.includes('=')).map((l) => { const i = l.indexOf('='); return [l.slice(0,i), l.slice(i+1).replace(/^(['"])(.*)\1$/, '$2')] }))
function userJWT(uid) {
  const encode = (x) => Buffer.from(JSON.stringify(x)).toString('base64url')
  const now = Math.floor(Date.now()/1000)-2
  const raw = encode({alg:'HS256',typ:'JWT'})+'.'+encode({iss:operator.BANKING_JWT_ISSUER||'qingyingjin',aud:[operator.BANKING_JWT_AUDIENCE||'banking-service'],sub:uid,iat:now,nbf:now,exp:now+1800,jti:randomUUID(),token_use:'user'})
  return raw+'.'+createHmac('sha256',operator.BANKING_USER_JWT_SECRET).update(raw).digest('base64url')
}
const makePhone = () => '188'+String(randomBytes(4).readUInt32BE()%100000000).padStart(8,'0')
const phone = makePhone(), legacyPhone=makePhone(), password=randomBytes(24).toString('base64url'), nextPassword=randomBytes(24).toString('base64url')
async function code(number,purpose) {
  const r=await execute(resolve(root,'output/sms-auth/sms-inbox.exe'),['-env-file',envFile,'-env-file',smsFile,'-phone',number,'-purpose',purpose,'-allow-development-code'],{timeout:10000,windowsHide:true})
  const message=JSON.parse(r.stdout)
  assert.ok(/^[0-9]{6}$/.test(message.code))
  return message.code
}
async function api(path,body,token) {
  const r=await fetch(base+path,{method:body===undefined?'GET':'POST',headers:{'Content-Type':'application/json',Origin:base,...(token?{Authorization:'Bearer '+token}:{}),'Idempotency-Key':randomUUID()},body:body===undefined?undefined:JSON.stringify(body)})
  return {status:r.status,body:await r.json()}
}
const checks=[],errors=[]
let failure='',stage='start'
const pass=(name)=>{checks.push(name);console.log('PASS: '+name)}
const browser=await chromium.launch({headless:true,executablePath:process.env.BANKING_TEST_BROWSER||'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe'})
const context=await browser.newContext({viewport:{width:1440,height:1000}})
const page=await context.newPage();page.setDefaultTimeout(25000);page.on('pageerror',(e)=>errors.push(e.message))
const dialog=page.getByRole('dialog')
async function login(number,pwd) {
  await page.getByRole('button',{name:'登录 / 注册',exact:true}).click()
  if(await dialog.getByRole('button',{name:'去登录',exact:true}).count())await dialog.getByRole('button',{name:'去登录',exact:true}).click()
  await dialog.getByLabel('手机号',{exact:true}).fill(number);await dialog.getByLabel('密码',{exact:true}).fill(pwd)
  const wait=page.waitForResponse((r)=>new URL(r.url()).pathname==='/api/v1/auth/login'&&r.request().method()==='POST')
  await dialog.getByRole('button',{name:'登录',exact:true}).click()
  const response=await wait;assert.equal(response.status(),200)
  await page.getByText('已登录',{exact:true}).waitFor()
  return (await response.json()).data
}
try {
  stage='capabilities'
  const capabilities=await api('/api/v1/auth/capabilities')
  assert.equal(capabilities.body.data.sms_mode,'development')
  assert.equal(capabilities.body.data.registration_verification_required,true)
  const absent=await api('/api/v1/auth/register',{phone:makePhone(),password})
  assert.equal(absent.status,401);assert.equal(absent.body.code,'AUTH_CODE_INVALID')
  pass('Development mode explicit; registration without a verified challenge fails')

  stage='registration'
  await page.goto(base+'/security');await page.getByRole('button',{name:'登录 / 注册',exact:true}).click()
  await dialog.getByRole('button',{name:'立即注册',exact:true}).click()
  await dialog.getByText('开发短信不会发送到手机，请使用本机开发短信收件箱读取验证码。',{exact:true}).waitFor()
  await page.screenshot({path:out+'/registration-desktop.png'})
  await dialog.getByLabel('手机号',{exact:true}).fill(phone);await dialog.getByLabel('密码',{exact:true}).fill(password);await dialog.getByLabel('确认密码',{exact:true}).fill(password)
  const sending=page.waitForResponse((r)=>new URL(r.url()).pathname==='/api/v1/auth/sms/registration')
  await dialog.getByRole('button',{name:'发送验证码',exact:true}).click()
  const sent=await sending;assert.equal(sent.status(),200)
  const challenge=(await sent.json()).data,registrationSentAt=Date.now()
  assert.ok(!('code' in challenge));assert.equal(challenge.retry_after,60)
  const otp=await code(phone,'register')
  const body=await sent.text();assert.ok(!body.includes(otp))
  assert.ok(await dialog.getByRole('button',{name:/秒后重发/}).isDisabled())
  await dialog.getByLabel('验证码',{exact:true}).fill(otp)
  const registering=page.waitForResponse((r)=>new URL(r.url()).pathname==='/api/v1/auth/register'&&r.request().method()==='POST')
  await dialog.getByRole('button',{name:'注册并登录',exact:true}).click()
  const registered=await registering;assert.equal(registered.status(),201)
  const first=(await registered.json()).data
  assert.equal(first.phone_verified,true);assert.equal(first.phone_verification_method,'development')
  await page.getByText('已登录',{exact:true}).waitFor()
  const replay=await api('/api/v1/auth/register',{phone,password,challenge_id:challenge.challenge_id,code:otp})
  assert.equal(replay.status,401)
  await page.getByRole('button',{name:'账户资料',exact:true}).click()
  await dialog.getByText('开发验证（非真实手机号认证）',{exact:true}).waitFor()
  await dialog.getByRole('button',{name:'关闭',exact:true}).last().click()
  pass('Real UI registration reads encrypted local inbox; code is single-use and never returned over HTTP')

  stage='legacy fixture'
  // Unique isolated simulated identity; owner-bound binding never selects or
  // changes any historical user or credits a financial account.
  const legacyID=String(700000000000000000n+BigInt(randomBytes(6).readUIntBE(0,6)))
  const legacyToken=userJWT(legacyID)
  assert.equal((await api('/api/v1/users/me',{display_name:'短信迁移隔离验收'},legacyToken)).status,201)
  const binding=await api('/api/v1/auth/bind',{phone:legacyPhone,password},legacyToken)
  assert.equal(binding.status,200);assert.equal(binding.body.data.phone_verified,false)
  await page.getByRole('button',{name:'退出登录',exact:true}).click()
  await login(legacyPhone,password)
  await page.getByRole('button',{name:'账户资料',exact:true}).click()
  await dialog.getByText('未验证',{exact:true}).waitFor()
  await dialog.getByLabel('当前密码',{exact:true}).fill(password)
  await dialog.getByRole('button',{name:'发送验证码',exact:true}).click()
  await dialog.getByRole('button',{name:/秒后重发/}).waitFor()
  await dialog.getByLabel('验证码',{exact:true}).fill(await code(legacyPhone,'verify_phone'))
  await dialog.getByRole('button',{name:'确认验证手机号',exact:true}).click()
  await dialog.getByText('开发验证（非真实手机号认证）',{exact:true}).waitFor()
  await dialog.getByRole('button',{name:'关闭',exact:true}).last().click()
  await page.getByRole('button',{name:'退出登录',exact:true}).click()
  pass('Existing unverified identity requires original password plus its owner-bound phone challenge')

  stage='cooldown'
  const remaining=Math.max(0,registrationSentAt+61000-Date.now())
  if(remaining){console.log('WAIT: respecting real 60-second phone resend cooldown');await new Promise((r)=>setTimeout(r,remaining))}
  stage='recovery'
  await page.getByRole('button',{name:'登录 / 注册',exact:true}).click()
  await dialog.getByRole('button',{name:'忘记密码',exact:true}).click()
  await dialog.getByRole('heading',{name:'找回密码',exact:true}).waitFor()
  await dialog.getByLabel('手机号',{exact:true}).fill(phone)
  await dialog.getByLabel('新密码',{exact:true}).fill(nextPassword);await dialog.getByLabel('确认新密码',{exact:true}).fill(nextPassword)
  const beforeResetLogin=await api('/api/v1/auth/login',{phone,password})
  assert.equal(beforeResetLogin.status,200)
  const liveOldAccess=beforeResetLogin.body.data.access_token
  assert.equal((await api('/api/v1/users/me',undefined,liveOldAccess)).status,200)
  const resettingCode=page.waitForResponse((r)=>new URL(r.url()).pathname==='/api/v1/auth/sms/password-reset')
  await dialog.getByRole('button',{name:'发送验证码',exact:true}).click()
  const resetResponse=await resettingCode;assert.equal(resetResponse.status(),200)
  const resetChallenge=(await resetResponse.json()).data,resetOTP=await code(phone,'password_reset')
  await dialog.getByLabel('验证码',{exact:true}).fill(resetOTP)
  const oldOperatorToken=userJWT(first.user.id)
  assert.equal((await api('/api/v1/users/me',undefined,oldOperatorToken)).status,200)
  const resetting=page.waitForResponse((r)=>new URL(r.url()).pathname==='/api/v1/auth/password/reset')
  await dialog.getByRole('button',{name:'重置密码',exact:true}).click()
  const resetResult=await resetting;assert.equal(resetResult.status(),200);assert.equal((await resetResult.json()).data,null)
  await dialog.getByRole('heading',{name:'登录轻融金',exact:true}).waitFor()
  assert.equal((await api('/api/v1/users/me',undefined,first.access_token)).status,401)
  assert.equal((await api('/api/v1/users/me',undefined,liveOldAccess)).status,401)
  assert.equal((await api('/api/v1/users/me',undefined,oldOperatorToken)).status,401)
  assert.equal((await api('/api/v1/auth/password/reset',{phone,challenge_id:resetChallenge.challenge_id,code:resetOTP,new_password:password})).status,401)
  pass('Password reset requires a valid code, does not sign in and revokes old access and legacy user tokens')

  stage='new password'
  await dialog.getByLabel('手机号',{exact:true}).fill(phone);await dialog.getByLabel('密码',{exact:true}).fill(password)
  await dialog.getByRole('button',{name:'登录',exact:true}).click();await dialog.getByRole('alert').filter({hasText:'手机号或密码错误'}).waitFor()
  await dialog.getByLabel('密码',{exact:true}).fill(nextPassword);await dialog.getByRole('button',{name:'登录',exact:true}).click()
  await page.getByText('已登录',{exact:true}).waitFor();await page.reload();await page.getByText('已登录',{exact:true}).waitFor()
  await page.getByRole('button',{name:'账户资料',exact:true}).click();await dialog.getByText('开发验证（非真实手机号认证）',{exact:true}).waitFor()
  const local=await page.evaluate(()=>JSON.stringify({...localStorage}))
  for(const secret of [phone,legacyPhone,password,nextPassword,otp,resetOTP,first.access_token,liveOldAccess])assert.ok(!local.includes(secret))
  await page.setViewportSize({width:390,height:844});await page.screenshot({path:out+'/profile-mobile.png'})
  const dimensions=await page.evaluate(()=>({viewport:innerWidth,page:document.documentElement.scrollWidth}));assert.ok(dimensions.page<=dimensions.viewport)
  assert.deepEqual(errors,[])
  pass('Old password fails, new password signs in and survives reload; no secrets in localStorage or mobile overflow')
} catch(error) {
  failure=(error?.name==='TimeoutError'?'Browser timeout':'Assertion failure')+' at '+stage
  console.error(failure)
  await page.locator('input').evaluateAll((inputs)=>inputs.forEach((i)=>{i.value=''})).catch(()=>{})
  await page.screenshot({path:out+'/failure.png'}).catch(()=>{})
} finally {
  await context.request.post(base+'/api/v1/auth/logout',{headers:{Origin:base},data:{}}).catch(()=>{})
  await writeFile(out+'/acceptance.json',JSON.stringify({passed:!failure,checks,failure,browser_errors:errors,response_interception:false,sms_delivery:'encrypted_local_development_inbox',real_sms_sent:false,simulation_only:true},null,2)+'\n')
  await browser.close()
}
if(failure)process.exitCode=1
