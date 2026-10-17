import assert from 'node:assert/strict'
import { test } from 'node:test'
import { applyDeepSeek, bootstrapModelDescriptor, buildConfig, createNativeCall, nextVersion, parseEnv, rebindCanvas, runtimeLogLevel, safeLogLevel, verifyModels } from './configure-coze-deepseek.mjs'

const key = 'sk-unit-test-only-never-real-1234567890'
const env = { DEEPSEEK_API_KEY: key, DEEPSEEK_BASE_URL: 'https://api.deepseek.com/v1', DEEPSEEK_MODEL: 'deepseek-flash', DEEPSEEK_MAX_TOKENS: '4096' }
const canvas = { nodes: [{ id: '1', type: '1' }, { id: '2', type: '3', data: { inputs: { settingOnError: { timeoutMs: 60000, retryTimes: 0 }, llmParam: [
  ['modelType', '73'], ['modleName', 'old'], ['maxTokens', '1536'], ['systemPrompt', 'Return exact JSON evidence'], ['responseFormat', '2'],
].map(([name, content]) => ({ name, input: { value: { type: 'literal', content } } })) } } }, { id: '3', type: '5', data: { code: 'preserve' } }], edges: [{ sourceNodeID: '2', targetNodeID: '3' }] }

test('blank key fails closed without exposing the supplied value', () => {
  assert.throws(() => buildConfig({ ...env, DEEPSEEK_API_KEY: '' }), { message: 'DEEPSEEK_KEY_REQUIRED' })
  assert.throws(() => buildConfig({ ...env, DEEPSEEK_API_KEY: 'CHANGE_ME_private' }), { message: 'DEEPSEEK_KEY_INVALID' })
  assert.throws(() => buildConfig({ ...env, DEEPSEEK_API_KEY: 'invalid secret value with spaces' }), { message: 'DEEPSEEK_KEY_INVALID' })
})
test('config only permits the official HTTPS endpoint and bounded exact token integers', () => {
  assert.equal(buildConfig(env).model, 'deepseek-flash')
  for (const url of ['http://api.deepseek.com/v1', 'https://api.deepseek.com.evil/v1', 'https://api.deepseek.com/v1?api_key=secret', 'https://user:pass@api.deepseek.com/v1'])
    assert.throws(() => buildConfig({ ...env, DEEPSEEK_BASE_URL: url }), { message: 'DEEPSEEK_BASE_URL_INVALID' })
  for (const count of ['4096.0', '1e3', '0001', '0', '8193', 'NaN'])
    assert.throws(() => buildConfig({ ...env, DEEPSEEK_MAX_TOKENS: count }), { message: 'DEEPSEEK_MAX_TOKENS_INVALID' })
})
test('existing bootstrap retains a configured native DeepSeek provider and response cap', () => {
  const actual = bootstrapModelDescriptor({ modelProvider: 'DeepSeek', modelClass: 19, modelMaxTokens: 4096, modelConnection: { model: 'deepseek-flash' } })
  assert.deepEqual(actual, { modelName: '轻融金 DeepSeek deepseek-flash', modelClass: 19, maxTokens: 4096, versionDescription: 'DeepSeek 云模型联调' })
  const legacy = bootstrapModelDescriptor({ modelConnection: { model: 'qwen2.5:1.5b' } })
  assert.equal(legacy.modelClass, 1)
  assert.equal(legacy.maxTokens, 1536)
})
test('environment parsing rejects duplicate names and supports only explicit DeepSeek names', () => {
  assert.deepEqual(parseEnv('\uFEFF# comment\nDEEPSEEK_API_KEY=\nDEEPSEEK_MODEL="deepseek-flash"\r\n'), { DEEPSEEK_API_KEY: '', DEEPSEEK_MODEL: 'deepseek-flash' })
  assert.throws(() => parseEnv('DEEPSEEK_API_KEY=a\nDEEPSEEK_API_KEY=b'), { message: 'DEEPSEEK_ENV_INVALID' })
  assert.throws(() => parseEnv('OTHER_KEY=secret'), { message: 'DEEPSEEK_ENV_INVALID' })
})
test('the existing native logger defaults to info, and trace/debug prevent registration', () => {
  assert.equal(safeLogLevel(['LOG_LEVEL=info', 'SECRET=not-printed']), 'info')
  assert.equal(safeLogLevel([]), 'info')
  for (const level of ['debug', 'TRACE', ' debug ']) assert.throws(() => safeLogLevel([`LOG_LEVEL=${level}`]), { message: 'COZE_UNSAFE_DEBUG_LOGGING' })
})
test('runtime logger proof requires exactly one running backend PID and never trusts Docker Config.Env', () => {
  assert.equal(runtimeLogLevel('242\tinfo\n'), 'info')
  assert.equal(runtimeLogLevel('242\t\n'), 'info')
  assert.throws(() => runtimeLogLevel(''), { message: 'COZE_LOG_LEVEL_UNVERIFIED' })
  assert.throws(() => runtimeLogLevel('242\tinfo\n244\tinfo\n'), { message: 'COZE_LOG_LEVEL_UNVERIFIED' })
  assert.throws(() => runtimeLogLevel('242\tdebug\n'), { message: 'COZE_UNSAFE_DEBUG_LOGGING' })
})
test('canvas changes only native model fields and token cap, retaining schema, code, edges and deadline', () => {
  const before = structuredClone(canvas)
  const actual = rebindCanvas(canvas, '900', 'DeepSeek cloud', 4096)
  assert.deepEqual(canvas, before)
  const params = actual.nodes[1].data.inputs.llmParam
  assert.equal(params[0].input.value.content, '900')
  assert.equal(params[1].input.value.content, 'DeepSeek cloud')
  assert.equal(params[2].input.value.content, '4096')
  assert.equal(params[3].input.value.content, 'Return exact JSON evidence')
  assert.deepEqual(actual.edges, before.edges)
  assert.deepEqual(actual.nodes[2], before.nodes[2])
  assert.deepEqual(actual.nodes[1].data.inputs.settingOnError, before.nodes[1].data.inputs.settingOnError)
  assert.throws(() => rebindCanvas({ nodes: [], edges: [] }, '900', 'cloud', 4096), { message: 'COZE_CANVAS_NO_LLM' })
})
test('workflow versions increment a published semantic version and reject ambiguous labels', () => {
  assert.equal(nextVersion('v0.0.28'), 'v0.0.29')
  assert.equal(nextVersion('v2.1.8'), 'v2.1.9')
  assert.throws(() => nextVersion('custom-release'), { message: 'COZE_VERSION_UNSUPPORTED' })
})

test('explicit workflow deadline is bounded and changes only the native timeout field', () => {
  const config = buildConfig({...env, DEEPSEEK_WORKFLOW_TIMEOUT_MS:'120000'})
  assert.equal(config.workflowTimeoutMs,120000)
  const changed = rebindCanvas(canvas,'900','DeepSeek cloud',4096,config.workflowTimeoutMs)
  assert.equal(changed.nodes[1].data.inputs.settingOnError.timeoutMs,120000)
  assert.equal(changed.nodes[1].data.inputs.settingOnError.retryTimes,0)
  assert.equal(canvas.nodes[1].data.inputs.settingOnError.timeoutMs,60000)
  assert.throws(()=>buildConfig({...env,DEEPSEEK_WORKFLOW_TIMEOUT_MS:'999999'}))
})
test('model discovery is GET only, uses authorization header, and never returns provider error bodies', async () => {
  let calls = 0
  const config = buildConfig(env)
  const available = await verifyModels(config, async (url, options) => {
    calls++
    assert.equal(url, 'https://api.deepseek.com/v1/models')
    assert.equal(options.method, 'GET')
    assert.equal(options.headers.Authorization, `Bearer ${key}`)
    assert.equal(options.redirect, 'error')
    return new Response(JSON.stringify({ object: 'list', data: [{ id: 'deepseek-flash' }, { id: 'deepseek-v4-pro' }] }))
  })
  assert.deepEqual(available, ['deepseek-flash', 'deepseek-v4-pro'])
  assert.equal(calls, 1)
  await assert.rejects(verifyModels(config, async () => new Response(key, { status: 401 })), { message: 'DEEPSEEK_MODELS_HTTP_401' })
  await assert.rejects(verifyModels(config, async () => new Response(JSON.stringify({ data: [{ id: 'other' }] }))), { message: 'DEEPSEEK_MODEL_UNAVAILABLE' })
})
test('native errors identify only the fixed operation and integer code, never raw provider text or key', async () => {
  const call = createNativeCall({ cookie: 'private session' }, async (url, options) => {
    assert.equal(url, 'http://127.0.0.1:8888/api/admin/config/model/create')
    assert.equal(options.redirect, 'error')
    return new Response(JSON.stringify({ code: 700012345, msg: `provider leaked ${key}` }))
  })
  await assert.rejects(call('/api/admin/config/model/create', {}), { message: 'COZE_NATIVE_REJECTED_MODEL_CREATE_700012345' })
})

function fixture() {
  const roles = ['supervisor', 'billing', 'subscription', 'savings', 'lifestyle', 'goal_v2']
  const state = { spaceId: '12', cookie: 'session=test', workflows: Object.fromEntries(roles.map((role, i) => [role, { id: String(i + 100), published: true, revision: 4 }])) }
  const log = []
  const versions = new Map(roles.map((role, i) => [String(i + 100), 'v0.0.4']))
  const canvases = new Map(roles.map((role, i) => [String(i + 100), JSON.stringify(canvas)]))
  const dirty = new Set()
  const call = async (path, body) => {
    log.push({ path, body })
    if (path.endsWith('/model/create')) return { code: 0, id: '900' }
    if (path.endsWith('/canvas')) return { code: 0, data: { workflow: { workflow_id: body.workflow_id, space_id: '12', schema_json: canvases.get(body.workflow_id) }, workflow_version: versions.get(body.workflow_id), vcs_data: { type: dirty.has(body.workflow_id) ? 1 : 3, submit_commit_id: '123456' } } }
    if (path.endsWith('/workflow_detail_info')) {
      const id = body.workflow_filter_list[0].workflow_id
      return { code: 0, data: { list: [{ workflow_id: id, space_id: '12', latest_flow_version: versions.get(id), flow_version: body.workflow_filter_list[0].workflow_version ?? '' }] } }
    }
    if (path.endsWith('/save')) { canvases.set(body.workflow_id, body.schema); dirty.add(body.workflow_id); return { code: 0 } }
    if (path.endsWith('/publish')) {
      assert.equal(body.commit_id, '123456', 'Publish must use the native generated CommitID, not the caller UUID ignored by Save')
      versions.set(body.workflow_id, body.workflow_version); dirty.delete(body.workflow_id)
      return { code: 0, data: { success: true } }
    }
    throw new Error('unexpected native API')
  }
  return { state, call, log, versions, canvases, dirty, roles }
}
test('apply accepts the native WorkflowDetailInfoDataList MarshalJSON array, rebinds IDs, and preserves history', async () => {
  const f = fixture(), config = buildConfig(env), backups = []
  const options = { config, state: f.state, call: f.call, persist: async () => {}, backup: async record => backups.push(record), logLevel: 'info' }
  options.call = async (path, body) => {
    const response = await f.call(path, body)
    if (path.endsWith('/workflow_detail_info')) response.data = response.data.list
    return response
  }
  const result = await applyDeepSeek(options)
  const create = f.log.find(row => row.path.endsWith('/model/create'))
  assert.equal(create.body.model_class, 19)
  assert.deepEqual(create.body.connection.base_conn_info, { base_url: config.baseURL, api_key: key, model: config.model })
  assert.equal(result.workflows.length, 6)
  assert.equal(backups.length, 6)
  for (const role of f.roles) assert.equal(f.state.workflows[role].version, 'v0.0.5')
  assert.equal(result.thinking_control_supported, false)
  assert.ok(!JSON.stringify(result).includes(key))
  await applyDeepSeek(options)
  assert.equal(f.log.filter(row => row.path.endsWith('/model/create')).length, 1)
  assert.equal(f.log.filter(row => row.path.endsWith('/publish')).length, 6)
})
test('apply rejects dirty drafts before paid registration or native mutation', async () => {
  const f = fixture()
  const call = async (path, body) => {
    const data = await f.call(path, body)
    if (path.endsWith('/canvas')) data.data.vcs_data.type = 1
    return data
  }
  await assert.rejects(applyDeepSeek({ config: buildConfig(env), state: f.state, call, persist: async () => {}, backup: async () => {}, logLevel: 'info' }), { message: 'COZE_UNPUBLISHED_DRAFT' })
  assert.equal(f.log.some(row => row.path.endsWith('/model/create')), false)
  assert.equal(f.log.some(row => row.path.endsWith('/save')), false)
})
test('apply prevents key-bearing registration while the real native logger is debug', async () => {
  const f = fixture()
  await assert.rejects(applyDeepSeek({ config: buildConfig(env), state: f.state, call: f.call, persist: async () => {}, backup: async () => {}, logLevel: 'debug' }), { message: 'COZE_UNSAFE_DEBUG_LOGGING' })
  assert.equal(f.log.length, 0)
})
