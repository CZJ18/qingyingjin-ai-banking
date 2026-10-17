// Actual local multi-user browser/API acceptance. No intercepted responses or SQL fixtures.
import assert from 'node:assert/strict'
import { randomBytes, randomUUID } from 'node:crypto'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { mkdir, writeFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { resolve } from 'node:path'
import { chromium } from '@playwright/test'
const execute = promisify(execFile)
const root = fileURLToPath(new URL('../../../', import.meta.url))
const base = process.env.BANKING_RECIPIENT_URL || 'http://127.0.0.1:8088'
assert.ok(['127.0.0.1', 'localhost'].includes(new URL(base).hostname))
const out = resolve(root, 'output/recipient-transfers/live')
await mkdir(out, { recursive: true })
const checks = [], errors = [], users = []
const sameName = '同名收款验收' + Date.now()
let failure = ''
async function api(user, path, body, status = 200) {
 const r = await fetch(base + path, { method: body === undefined ? 'GET' : 'POST', headers: { 'Content-Type': 'application/json', Origin: base, 'Idempotency-Key': randomUUID(), ...(user?.access ? { Authorization: 'Bearer ' + user.access } : {}) }, body: body === undefined ? undefined : JSON.stringify(body) })
 const env = await r.json()
 if (r.status !== status) throw new Error(path + ' HTTP ' + r.status + ' ' + env.code)
 if (status >= 400) return env
 assert.equal(env.code, 'OK', path)
 if (path === '/api/v1/auth/register') user.cookie = r.headers.getSetCookie().find(c => c.startsWith('qingrongjin_refresh=')) || ''
 return env.data
}
async function operation(user, kind, parameters) {
 let v = await api(user, '/api/v1/execution/operations', { kind, parameters }, 201)
 v = await api(user, `/api/v1/execution/operations/${v.operation.id}/authorize`, { decision: 'approve', intent_digest: v.operation.intent_digest, confirmation_nonce: v.operation.confirmation_nonce })
 v = await api(user, `/api/v1/execution/operations/${v.operation.id}/execute`, { authorization_id: v.operation.authorization_id, intent_digest: v.operation.intent_digest, expected_version: v.operation.version })
 assert.equal(v.operation.status, 'succeeded')
 return v.operation.result
}
const browser = await chromium.launch({ headless: true, executablePath: process.env.BANKING_TEST_BROWSER || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe' })
async function register(index) {
 const user = {}, phone = '188' + String(randomBytes(4).readUInt32BE() % 100000000).padStart(8, '0'), password = randomBytes(24).toString('base64url')
 const challenge = await api(user, '/api/v1/auth/sms/registration', { phone })
 const inbox = await execute(resolve(root, 'output/sms-auth/sms-inbox.exe'), ['-env-file', resolve(root, 'deploy/banking-postgres/finance.app.env.local'), '-env-file', resolve(root, 'deploy/banking/sms.env.local'), '-phone', phone, '-purpose', 'register', '-allow-development-code'], { timeout: 10000, windowsHide: true })
 const code = JSON.parse(inbox.stdout).code
 assert.match(code, /^[0-9]{6}$/)
 const r = await api(user, '/api/v1/auth/register', { phone, password, display_name: index === 0 ? '转账付款验收' : sameName, challenge_id: challenge.challenge_id, code }, 201)
 user.id = r.user.id; user.access = r.access_token; user.phone = phone
 const a = await operation(user, 'open_account', { display_name: index === 0 ? '我的付款账户' : '我的收款账户', account_type: 'checking', currency: 'CNY', initial_balance_minor: index === 0 ? 50000 : 0, confirm_simulated_funds: true })
 user.account = a.id
 user.context = await browser.newContext({ viewport: { width: 1440, height: 1100 } })
 const pair = user.cookie.split(';')[0], at = pair.indexOf('=')
 await user.context.addCookies([{ name: pair.slice(0, at), value: pair.slice(at + 1), domain: new URL(base).hostname, path: '/api/v1/auth', httpOnly: true, secure: false, sameSite: 'Strict' }])
 user.page = await user.context.newPage(); user.page.setDefaultTimeout(30000)
 user.page.on('pageerror', e => errors.push(e.message))
 users.push(user)
 await user.page.goto(base + '/security')
 await user.page.locator('.recipient-transfer').getByRole('heading', { name: '找人转账', exact: true }).waitFor()
 return user
}
async function confirm(user, success = true) {
 const d = user.page.getByRole('dialog')
 await d.getByText('向已选收款人转账', { exact: true }).waitFor()
 assert.equal(await d.getByRole('button', { name: '授权并执行', exact: true }).isDisabled(), true)
 await d.getByRole('checkbox').check()
 await d.getByRole('button', { name: '授权并执行', exact: true }).click()
 await d.getByText(success ? '成功' : '失败', { exact: true }).waitFor()
 await d.getByRole('button', { name: '关闭', exact: true }).last().click()
}

const mask = phone => phone.slice(0,3)+'****'+phone.slice(7)
async function receiving(user, enabled) {
 const panel=user.page.locator('.recipient-transfer'),details=panel.locator('.recipient-settings')
 if ((await details.getAttribute('open')) === null) await details.locator('summary').click()
 const toggle=details.getByLabel('允许通过姓名或手机号查找我并向我转账')
 await toggle.setChecked(enabled)
 if(enabled) await details.getByLabel('我的默认收款账户',{exact:true}).selectOption(user.account)
 const response=user.page.waitForResponse(r=>r.url().endsWith('/api/v1/recipients/settings')&&r.request().method()==='PUT')
 await details.getByRole('button',{name:'保存收款设置',exact:true}).click()
 assert.equal((await response).status(),200)
 assert.equal((await api(user,'/api/v1/recipients/settings')).enabled,enabled)
}
async function lookup(user,kind,query) {
 const panel=user.page.locator('.recipient-transfer')
 await panel.getByLabel('收款人查找方式',{exact:true}).selectOption(kind)
 await panel.getByLabel('收款人查询内容',{exact:true}).fill(query)
 const response=user.page.waitForResponse(r=>r.url().endsWith('/api/v1/recipients/search')&&r.request().method()==='POST')
 await panel.getByRole('button',{name:'查找收款人',exact:true}).click()
 const r=await response;assert.equal(r.status(),200)
 const env=await r.json();assert.equal(env.code,'OK')
 await panel.getByRole('button',{name:'查找收款人',exact:true}).waitFor()
 return env.data
}
try {
 const caps=await api(null,'/api/v1/auth/capabilities');assert.equal(caps.sms_mode,'development')
 for(let i=0;i<3;i++) await register(i)
 const [sender,first,second]=users,panel=sender.page.locator('.recipient-transfer')
 for(const user of users) { const config=await api(user,'/api/v1/recipients/settings');assert.equal(config.enabled,false) }
 assert.equal((await lookup(sender,'name',sameName)).items.length,0)
 checks.push('three actual verified development users; receiving defaults closed and no user is listed without opt-in')
 await receiving(first,true);await receiving(second,true)
 const byName=await lookup(sender,'name',sameName)
 assert.equal(byName.items.length,2)
 assert.equal(await panel.getByRole('radio').count(),2)
 assert.equal(await panel.getByRole('button',{name:'核对收款人与转账',exact:false}).isDisabled(),true)
 for(const user of [first,second]) {const item=byName.items.find(r=>r.id===user.id);assert.ok(item);assert.equal(item.phone_masked,mask(user.phone))}
 const resultsText=await panel.locator('.recipient-results').innerText()
 assert.ok(!resultsText.includes(first.phone)&&!resultsText.includes(second.phone))
 checks.push('exact display-name lookup returns both same-name recipients, masked phones and requires an explicit choice')
 await panel.locator(`input[type=radio][value="${first.id}"]`).check()
 await panel.getByLabel('找人转账金额',{exact:true}).fill('20')
 await panel.getByLabel('找人转账备注',{exact:true}).fill('按姓名转账 · 聚餐费用')
 await panel.getByRole('button',{name:'核对收款人与转账',exact:false}).click()
 const dialog=sender.page.getByRole('dialog')
 await dialog.getByText('向已选收款人转账',{exact:true}).waitFor()
 await dialog.getByText(mask(first.phone),{exact:true}).waitFor()
 assert.equal((await api(sender,`/api/v1/accounts/${sender.account}/balance`)).balance_minor,50000)
 assert.equal((await api(first,`/api/v1/accounts/${first.account}/balance`)).balance_minor,0)
 await confirm(sender)
 assert.equal((await api(sender,`/api/v1/accounts/${sender.account}/balance`)).balance_minor,48000)
 assert.equal((await api(first,`/api/v1/accounts/${first.account}/balance`)).balance_minor,2000)
 checks.push('name-selected transfer shows bound recipient confirmation; no debit before consent; 20 CNY posts to the correct person')
 const byPhone=await lookup(sender,'phone','+86'+second.phone)
 assert.equal(byPhone.items.length,1);assert.equal(byPhone.items[0].id,second.id)
 assert.equal(await panel.getByRole('button',{name:'核对收款人与转账',exact:false}).isDisabled(),true)
 await panel.locator(`input[type=radio][value="${second.id}"]`).check()
 await panel.getByLabel('找人转账金额',{exact:true}).fill('30')
 await panel.getByLabel('找人转账备注',{exact:true}).fill('按手机号转账 · 生活费用')
 await panel.getByRole('button',{name:'核对收款人与转账',exact:false}).click()
 await confirm(sender)
 assert.equal((await api(sender,`/api/v1/accounts/${sender.account}/balance`)).balance_minor,45000)
 assert.equal((await api(second,`/api/v1/accounts/${second.account}/balance`)).balance_minor,3000)
 checks.push('full +86 phone resolves exactly one opted-in recipient and authorized 30 CNY transfer reaches that account')
 for(const [recipient,memo] of [[first,'按姓名转账 · 聚餐费用'],[second,'按手机号转账 · 生活费用']]) {
  const incoming=await api(recipient,`/api/v1/transactions?bank_account_id=${recipient.account}&limit=100`)
  const outgoing=await api(sender,`/api/v1/transactions?bank_account_id=${sender.account}&limit=100`)
  assert.ok(incoming.items.some(t=>t.description===memo&&t.direction==='income'))
  assert.ok(outgoing.items.some(t=>t.description===memo&&t.direction==='expense'))
 }
 checks.push('both counterparties retain each exact transfer description in actual persisted transaction APIs')
 await lookup(sender,'phone',first.phone)
 await panel.locator(`input[type=radio][value="${first.id}"]`).check()
 await panel.getByLabel('找人转账金额',{exact:true}).fill('10')
 await panel.getByLabel('找人转账备注',{exact:true}).fill('关闭收款后禁止执行')
 await panel.getByRole('button',{name:'核对收款人与转账',exact:false}).click()
 await dialog.getByText('向已选收款人转账',{exact:true}).waitFor()
 await receiving(first,false)
 await confirm(sender,false)
 assert.equal((await api(sender,`/api/v1/accounts/${sender.account}/balance`)).balance_minor,45000)
 assert.equal((await api(first,`/api/v1/accounts/${first.account}/balance`)).balance_minor,2000)
 const ops=await api(sender,'/api/v1/execution/operations')
 const failed=ops.find(o=>o.parameters?.description==='关闭收款后禁止执行')
 assert.ok(failed);assert.equal(failed.status,'failed');assert.equal(failed.error_code,'STATE_CONFLICT')
 assert.equal((await lookup(sender,'phone',first.phone)).items.length,0)
 checks.push('recipient closing receiving invalidates the prepared transfer; execution fails without debit and phone lookup no longer lists them')
 await lookup(sender,'name',sameName)
 await panel.locator(`input[type=radio][value="${second.id}"]`).check()
 await panel.getByLabel('收款人查询内容',{exact:true}).fill('修改查询会清空旧选择')
 assert.equal(await panel.getByRole('button',{name:'核对收款人与转账',exact:false}).isDisabled(),true)
 checks.push('editing lookup information clears previous recipient selection instead of reusing a stale choice')
 let total=0
 for(const user of users){const b=await api(user,`/api/v1/accounts/${user.account}/balance`);assert.equal(b.reconciled,true);total+=b.balance_minor}
 assert.equal(total,50000)
 checks.push('all three account balances match their own ledgers and total simulated cash stays 500 CNY')
 await lookup(sender,'name',sameName)
 await panel.locator(`input[type=radio][value="${second.id}"]`).check()
 await panel.screenshot({path:resolve(out,'desktop.png'),mask:[panel.getByLabel('收款人查询内容',{exact:true})]})
 await sender.page.setViewportSize({width:390,height:844})
 await panel.screenshot({path:resolve(out,'mobile.png'),mask:[panel.getByLabel('收款人查询内容',{exact:true})]})
 assert.ok(await sender.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth))
 assert.deepEqual(errors,[])
 checks.push('mobile has no horizontal overflow or uncaught browser errors; sensitive lookup input is masked in screenshots')
} catch(e) {
 failure=e.message
 for(let i=0;i<users.length;i++) await users[i].page?.screenshot({path:resolve(out,`failure-${i}.png`),fullPage:true,mask:[users[i].page.locator('input'),users[i].page.locator('textarea')]}).catch(()=>{})
} finally {
 for(const user of users){const cookies=await user.context.cookies(base+'/api/v1/auth/refresh').catch(()=>[]);const refresh=cookies.find(c=>c.name==='qingrongjin_refresh');if(refresh)await fetch(base+'/api/v1/auth/logout',{method:'POST',headers:{'Content-Type':'application/json',Origin:base,Cookie:refresh.name+'='+refresh.value},body:'{}'}).catch(()=>{})}
 await writeFile(resolve(out,'acceptance.json'),JSON.stringify({passed:!failure,scope:'actual local three-user browser/API; no mocks or SQL fixtures; local development verification only',checks,errors,failure},null,2))
 await browser.close()
}
if(failure)throw new Error('Recipient live acceptance failed: '+failure)
console.log(checks.length+' live recipient acceptance groups passed')
