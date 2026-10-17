import { readFile, writeFile, mkdir } from 'node:fs/promises'
import { resolve } from 'node:path'
import { randomBytes } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { bootstrapModelDescriptor, containerLogLevel } from './configure-coze-deepseek.mjs'

// Uses native authenticated APIs; private state lives in ignored output/, never in browser code.
const root = resolve(import.meta.dirname, '../..')
const privatePath = resolve(root, 'output/coze-local-private.json')
const base = 'http://127.0.0.1:8888'
await mkdir(resolve(root, 'output'), { recursive: true })
let state
try { state = JSON.parse(await readFile(privatePath, 'utf8')) } catch { state = { email: 'banking-local@qingyingjin.local', password: randomBytes(24).toString('base64url'), workflows: {} } }
async function persist() { await writeFile(privatePath, JSON.stringify(state, null, 2)) }
async function call(path, body, method = 'POST') {
  let res, data
  try { res = await fetch(base + path, { method, headers: { 'Content-Type': 'application/json', ...(state.cookie ? { Cookie: state.cookie } : {}) }, body: method === 'GET' ? undefined : JSON.stringify(body), redirect: 'error', signal: AbortSignal.timeout(60000) }) }
  catch { throw new Error(`${path}: native API unreachable`) }
  try { data = await res.json() } catch { throw new Error(`${path}: invalid native API response`) }
  if (!res.ok || data.code !== 0) throw new Error(`${path}: HTTP ${res.status}; native API rejected request`)
  const cookies = res.headers.getSetCookie()
  if (cookies.length) state.cookie = cookies.map(c => c.split(';')[0]).join('; ')
  return data
}
if (!state.cookie) {
  await persist()
  const user = await call('/api/passport/web/email/register/v2/', { email: state.email, password: state.password })
  state.userId = user.data.user_id || user.data.uid || user.data.id
  await persist()
  console.log('Native account registered; password and session saved only in ignored local state')
} else {
  state.cookie = ''
  await call('/api/passport/web/email/login/', { email: state.email, password: state.password })
  await persist()
}
const spaces = await call('/api/playground_api/space/list', {})
state.spaceId = spaces.data.bot_space_list.find(s => s.space_type === 1)?.id
await persist()
console.log('Native personal space:', state.spaceId)
if (!state.pat) {
  const result = await call('/api/permission_api/pat/create_personal_access_token_and_permission', { name: 'Banking local integration', duration_day: '30' })
  state.pat = result.data.token
  if (!state.pat) throw new Error('Native PAT creation returned no token')
  await persist()
  console.log('Native PAT created; token not printed')
}
const p3 = process.argv.includes('--p3')
if (process.argv.includes('--publish')) {
  const descriptor = bootstrapModelDescriptor(state)
  const modelName = descriptor.modelName
  if (!state.modelId || (state.modelRegisteredKey || 'qwen2.5:1.5b') !== state.modelConnection?.model) {
    if (!state.modelConnection) throw new Error('Configure a verified model connection before publishing workflows')
    if (descriptor.modelClass === 19) containerLogLevel()
    const model = await call('/api/admin/config/model/create', {
      model_class: descriptor.modelClass, model_name: modelName,
      connection: { base_conn_info: state.modelConnection },
    })
    state.modelId = model.id
    state.modelRegisteredKey = state.modelConnection.model
    if (!state.modelId) throw new Error('Native model creation returned no ID')
    await persist()
    console.log('Verified real model registered:', state.modelId)
  }
  for (const role of (p3 ? ['supervisor', 'billing', 'subscription', 'savings', 'lifestyle', 'goal_v2'] : ['supervisor', 'billing', 'subscription', 'savings', 'goal'])) {
    const canvasPath = resolve(root, `output/banking-${role}.canvas.json`)
    const generator = role === 'goal_v2' ? 'coze/banking/lifestyle-agent/generate_goal_canvas.py' : role === 'goal' ? 'coze/banking/goal-compiler/generate_canvas.py' : 'coze/banking/supervisor-agent/generate_canvas.py'
    execFileSync('python', ['-X', 'utf8', resolve(root, generator), '--model-id', state.modelId, '--model-name', modelName, ...(['goal','goal_v2'].includes(role) ? [] : ['--role', role]), '--out', canvasPath])
    const canvas = JSON.parse(await readFile(canvasPath, 'utf8'))
    for (const node of canvas.nodes.filter(n => n.type === '3')) {
      node.data.inputs.settingOnError.timeoutMs = 60000
      for (const field of node.data.inputs.llmParam)
        if (field.name === 'maxTokens') field.input.value.content = String(descriptor.maxTokens)
      if (role === 'goal') {
        const field = node.data.inputs.llmParam.find(f => f.name === 'systemPrompt')
        const schema = JSON.parse(await readFile(resolve(root, 'services/banking-service/internal/goal/schemas/goal-parse.schema.json'), 'utf8'))
        const compactSchema = JSON.stringify(schema, (key, value) => ['maximum', 'minimum', 'maxItems', 'maxLength', 'maxProperties', '$id', '$schema'].includes(key) ? undefined : value)
        field.input.value.content = await readFile(resolve(root, 'coze/banking/goal-compiler/system-prompt.md'), 'utf8') + '\nSchema：' + compactSchema
      }
    }
    if (role === 'goal') {
      const example = JSON.parse(await readFile(resolve(root, 'backend/domain/workflow/internal/canvas/examples/code_plugin.json'), 'utf8'))
      const code = example.nodes.find(n => n.type === '5')
      const llm = canvas.nodes.find(n => n.type === '3')
      code.id = '700001'
      code.data.nodeMeta.title = '原文证据金额单位转换'
      code.data.inputs.code = await readFile(resolve(root, 'coze/banking/goal-compiler/normalize_amounts.py'), 'utf8')
      code.data.inputs.inputParameters = [['model_output', llm.id, 'output'], ['original_input', '100001', 'input_string']].map(([name, blockID, variable]) => ({ name, input: { type: 'string', value: { type: 'ref', content: { source: 'block-output', blockID, name: variable }, rawMeta: { type: 1 } } } }))
      code.data.outputs = [{ type: 'string', name: 'output' }]
      code.data.inputs.settingOnError = { processType: 1, timeoutMs: 10000, retryTimes: 0 }
      canvas.nodes.push(code)
      canvas.edges = canvas.edges.map(e => e.sourceNodeID === llm.id && e.targetNodeID === '900001' ? { ...e, targetNodeID: code.id } : e)
      canvas.edges.push({ sourceNodeID: code.id, targetNodeID: '900001' })
      canvas.nodes.find(n => n.type === '2').data.inputs.inputParameters[0].input.value.content.blockID = code.id
    }
    await writeFile(canvasPath, JSON.stringify(canvas))
    const canvasHash = createHash('sha256').update(JSON.stringify(canvas)).digest('hex')
    if (!state.workflows[role]) {
      const created = await call('/api/workflow_api/create', { name: `轻盈金 ${role}`, desc: '本地真实模型银行工作流，金融执行须独立授权', icon_uri: 'default_icon/workflow.png', space_id: state.spaceId, flow_mode: 0 })
      state.workflows[role] = { id: created.data.workflow_id, published: false }
      await persist()
    }
    const wf = state.workflows[role]
    if (!wf.published || (p3 && wf.canvasHash !== canvasHash) || process.argv.includes('--refresh') || (['billing', 'subscription', 'savings'].includes(role) && process.argv.includes('--refresh-agents')) || (role === 'goal' && process.argv.includes('--refresh-goal'))) {
      const revision = wf.published ? (wf.revision || 1) + 1 : 1
      await call('/api/workflow_api/save', { workflow_id: wf.id, space_id: state.spaceId, schema: await readFile(canvasPath, 'utf8'), submit_commit_id: crypto.randomUUID() })
      await call('/api/workflow_api/publish', { workflow_id: wf.id, space_id: state.spaceId, has_collaborator: false, force: true, workflow_version: `v0.0.${revision}`, version_description: descriptor.versionDescription })
      wf.published = true
      wf.revision = revision
      wf.canvasHash = canvasHash
      await persist()
      console.log('Native workflow published:', role, wf.id)
    }
  }
}
if (process.argv.includes('--configure')) {
  if (!['supervisor', 'billing', 'subscription', 'savings', 'goal'].every(r => state.workflows[r]?.published))
    throw new Error('Publish all five native workflows before applying Banking configuration')
  const ids = {
    BANKING_SUPERVISOR_WORKFLOW_ID: state.workflows.supervisor.id,
    BANKING_BILLING_AGENT_WORKFLOW_ID: state.workflows.billing.id,
    BANKING_SUBSCRIPTION_AGENT_WORKFLOW_ID: state.workflows.subscription.id,
    BANKING_SAVINGS_AGENT_WORKFLOW_ID: state.workflows.savings.id,
    BANKING_GOAL_WORKFLOW_ID: state.workflows.goal.id,
    BANKING_COZE_PAT: state.pat,
    BANKING_COZE_TIMEOUT: '60s',
  }
  if (p3) {
    if (!state.workflows.lifestyle?.published || !state.workflows.goal_v2?.published)
      throw new Error('Publish both native P3 workflows before applying configuration')
    ids.COZE_LIFESTYLE_GOAL_WORKFLOW_ID = state.workflows.goal_v2.id
    ids.BANKING_LIFESTYLE_AGENT_WORKFLOW_ID = state.workflows.lifestyle.id
  }
  for (const [file, url] of [['.env', 'http://coze-server-local:8888'], ['.env.local', base]]) {
    const path = resolve(import.meta.dirname, file)
    let content = await readFile(path, 'utf8')
    for (const [key, value] of Object.entries({ ...ids, BANKING_COZE_BASE_URL: url })) {
      const line = `${key}=${value}`
      content = new RegExp(`^${key}=.*$`, 'm').test(content) ? content.replace(new RegExp(`^${key}=.*$`, 'm'), () => line) : content + '\n' + line + '\n'
    }
    await writeFile(path, content)
  }
  console.log('Banking configuration updated with native PAT and published IDs; secrets not printed')
}
if (process.argv.includes('--verify-p3')) {
  const wf = state.workflows.goal_v2
  if (!wf?.published) throw new Error('Publish goal_v2 first')
  const results = []
  for (const text of ['三个月每月少花1000元，可以调整shopping，禁止转账。', '想健康生活，少花些钱。']) {
    const response = await fetch(base + '/v1/workflow/run', { method: 'POST', headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${state.pat}` }, body: JSON.stringify({ workflow_id: wf.id, parameters: JSON.stringify({ input_string: JSON.stringify({ text }) }), is_async: false }), signal: AbortSignal.timeout(90000) })
    const run = await response.json()
    if (!response.ok || run.code !== 0 || !run.execute_id || !run.data) throw new Error(`Native goal_v2 failed: HTTP ${response.status}, code ${run.code}`)
    const output = JSON.parse(JSON.parse(run.data).output)
    results.push({ text, workflowId: wf.id, executeId: run.execute_id, output })
    await writeFile(resolve(root,'output/coze-p3-results.json'), JSON.stringify(results,null,2))
    if (text.startsWith('三个月')) {
      assert.equal(output.period_months,3)
      assert.ok(Number.isSafeInteger(output.expense_reduction_monthly_minor) && output.expense_reduction_monthly_minor > 0)
      assert.ok(output.evidence.expense_reduction_monthly_minor.includes('1000元'))
      // Raw model units are untrusted: Go converts exact original evidence and
      // independently validates it; the Banking live test checks 100000 cents.
      assert.equal(output.savings_monthly_minor,0)
    } else {
      if (output.status === 'needs_information') assert.ok(output.missing_information.includes('period_months'))
      else {
        // Native execution is not schema acceptance. Some small-model outputs
        // use null: retain that evidence and require Banking to reject it.
        assert.equal(output.period_months,null)
        console.log('Ambiguous raw model output requires Banking schema rejection; not accepted as a parsed goal')
      }
    }
    console.log('Real v2 workflow execution:',run.execute_id)
  }
}
if (process.argv.includes('--verify') || process.argv.includes('--verify-goal')) {
  const results = []
  for (const role of (process.argv.includes('--verify-goal') ? ['goal'] : ['supervisor', 'billing', 'subscription', 'savings', 'goal'])) {
    const wf = state.workflows[role]
    if (!wf?.published) throw new Error(`${role} is not published`)
    const payload = role === 'supervisor' ? {
      instruction: '分析本月消费，只需一个账单分析任务，不执行金融操作。',
      bindings: { from: '2026-10-01T00:00:00Z', to: '2026-11-01T00:00:00Z', account_id: '73' },
    } : role === 'goal' ? {
      text: '未来3个月每月储蓄100元，保持正常饮食与规律运动，每月必要开支200元，每月生活预算300元，每月收入1000元。',
    } : { task_id: 'local-model-verification', agent_type: role, authoritative_result: { note: 'No financial execution requested' }, evidence: [] }
    const response = await fetch(base + '/v1/workflow/run', { method: 'POST', headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${state.pat}` }, body: JSON.stringify({ workflow_id: wf.id, parameters: JSON.stringify({ input_string: JSON.stringify(payload) }), is_async: false }), signal: AbortSignal.timeout(90000) })
    const run = await response.json()
    if (run.code !== 0 || !run.execute_id || !run.data) throw new Error(`Native ${role} execution failed: code ${run.code}`)
    const end = JSON.parse(run.data)
    const output = JSON.parse(end.output)
    results.push({ role, workflowId: wf.id, executeId: run.execute_id, output })
    await writeFile(resolve(root, 'output/coze-live-results.json'), JSON.stringify(results, null, 2))
    if (role === 'goal') {
      for (const [field, value] of Object.entries({ period_months: 3, savings_monthly_minor: 10000, monthly_income_minor: 100000, spending_budget_minor: 30000, essential_expenses_minor: 20000, savings_target_minor: null }))
        assert.equal(output[field], value, `Real goal workflow fixture: ${field}`)
      for (const field of ['period_months', 'savings_monthly_minor', 'monthly_income_minor', 'spending_budget_minor', 'essential_expenses_minor'])
        assert.ok(output.evidence[field] && payload.text.includes(output.evidence[field]), `Missing source evidence: ${field}`)
    } else if (role !== 'supervisor') {
      assert.equal(output.task_id, payload.task_id)
      assert.equal(output.agent_type, role)
      assert.ok(output.explanation && Array.isArray(output.evidence_ids))
    }
    console.log('Real workflow execution:', role, run.execute_id)
  }
  await writeFile(resolve(root, 'output/coze-live-results.json'), JSON.stringify(results, null, 2))
}
