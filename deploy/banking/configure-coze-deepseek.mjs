import { createHash, randomUUID } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { mkdir, readFile, rename, writeFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { buildGatewayConfig, verifyGateway } from './model-spend-gateway.mjs'

const root = resolve(import.meta.dirname, '../..')
const officialBase = 'https://api.deepseek.com/v1'
const cozeBase = 'http://127.0.0.1:8888'
const requiredRoles = ['supervisor', 'billing', 'subscription', 'savings', 'lifestyle', 'goal_v2']
const envNames = new Set(['DEEPSEEK_API_KEY', 'DEEPSEEK_BASE_URL', 'DEEPSEEK_MODEL', 'DEEPSEEK_MAX_TOKENS', 'DEEPSEEK_WORKFLOW_TIMEOUT_MS', 'BANKING_ENVIRONMENT', 'DEEPSEEK_GATEWAY_BASE_URL', 'DEEPSEEK_GATEWAY_ALLOWED_ORIGIN', 'DEEPSEEK_GATEWAY_VERIFY_BASE_URL', 'DEEPSEEK_GATEWAY_TOKEN'])
const fail = code => { throw new Error(code) }
const hash = value => createHash('sha256').update(value).digest('hex')
const nativeID = value => typeof value === 'string' && /^[1-9][0-9]{0,18}$/.test(value) && BigInt(value) < 2n ** 63n

export function parseEnv(content) {
  const values = {}
  for (const line of content.replace(/^\uFEFF/, '').split(/\r?\n/)) {
    if (!line.trim() || line.trimStart().startsWith('#')) continue
    const match = /^([A-Z_]+)=(.*)$/.exec(line)
    if (!match || !envNames.has(match[1]) || Object.hasOwn(values, match[1])) fail('DEEPSEEK_ENV_INVALID')
    let value = match[2].trim()
    if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) value = value.slice(1, -1)
    values[match[1]] = value
  }
  return values
}

export function buildConfig(env) {
  const apiKey = env.DEEPSEEK_API_KEY ?? ''
  if (!apiKey) fail('DEEPSEEK_KEY_REQUIRED')
  if (!/^[\x21-\x7e]{20,512}$/.test(apiKey) || /CHANGE_ME|placeholder|your[-_ ]?api/i.test(apiKey)) fail('DEEPSEEK_KEY_INVALID')
  const baseURL = env.DEEPSEEK_BASE_URL || officialBase
  if (baseURL !== officialBase) fail('DEEPSEEK_BASE_URL_INVALID')
  const model = env.DEEPSEEK_MODEL || 'deepseek-flash'
  if (!/^[a-z0-9][a-z0-9._-]{1,127}$/.test(model)) fail('DEEPSEEK_MODEL_INVALID')
  const tokenText = env.DEEPSEEK_MAX_TOKENS || '4096'
  if (!/^[1-9][0-9]{0,3}$/.test(tokenText) || Number(tokenText) > 8192) fail('DEEPSEEK_MAX_TOKENS_INVALID')
  const deadline=env.DEEPSEEK_WORKFLOW_TIMEOUT_MS
  if(deadline!==undefined && (!/^\d+$/.test(deadline)||Number(deadline)<30000||Number(deadline)>180000)) fail('DEEPSEEK_WORKFLOW_TIMEOUT_INVALID')
  return { apiKey, baseURL, model, maxTokens: Number(tokenText), ...(deadline===undefined?{}:{workflowTimeoutMs:Number(deadline)}) }
}

export function bootstrapModelDescriptor(state) {
  const cloud = state.modelProvider === 'DeepSeek' && state.modelClass === 19
  return {
    modelName: cloud ? `轻融金 DeepSeek ${state.modelConnection?.model || ''}` : `轻盈金本地 ${state.modelConnection?.model || 'Qwen2.5'}`,
    modelClass: cloud ? 19 : 1,
    maxTokens: cloud && Number.isInteger(state.modelMaxTokens) ? state.modelMaxTokens : 1536,
    versionDescription: cloud ? 'DeepSeek 云模型联调' : '本地模型联调',
  }
}

export function safeLogLevel(containerEnv) {
  if (!Array.isArray(containerEnv) || containerEnv.some(value => typeof value !== 'string')) fail('COZE_LOG_LEVEL_UNVERIFIED')
  const values = containerEnv.filter(value => value.startsWith('LOG_LEVEL='))
  if (values.length > 1) fail('COZE_LOG_LEVEL_UNVERIFIED')
  // backend/main.go:setLogLevel defaults to INFO for missing/unrecognized values.
  const level = (values[0]?.slice('LOG_LEVEL='.length) || 'info').trim().toLowerCase()
  if (['trace', 'debug'].includes(level)) fail('COZE_UNSAFE_DEBUG_LOGGING')
  return ['info', 'notice', 'warn', 'error', 'fatal'].includes(level) ? level : 'info'
}

async function boundedJSON(response, code) {
  if (!response.ok) fail(`${code}_HTTP_${response.status}`)
  if (!response.body) fail(`${code}_INVALID`)
  let size = 0, text = ''
  const decoder = new TextDecoder()
  for await (const chunk of response.body) {
    size += chunk.byteLength
    if (size > 4 * 1024 * 1024) fail(`${code}_INVALID`)
    text += decoder.decode(chunk, { stream: true })
  }
  text += decoder.decode()
  try { return JSON.parse(text) } catch { fail(`${code}_INVALID`) }
}

export async function verifyModels(config, fetcher = fetch) {
  let response
  try {
    response = await fetcher(`${config.baseURL}/models`, {
      method: 'GET', headers: { Authorization: `Bearer ${config.apiKey}`, Accept: 'application/json' },
      redirect: 'error', signal: AbortSignal.timeout(20000),
    })
  } catch { fail('DEEPSEEK_MODELS_UNREACHABLE') }
  const body = await boundedJSON(response, 'DEEPSEEK_MODELS')
  if (!Array.isArray(body.data) || body.data.length > 256 || body.data.some(item => typeof item?.id !== 'string' || !/^[a-zA-Z0-9][a-zA-Z0-9._-]{1,127}$/.test(item.id))) fail('DEEPSEEK_MODELS_INVALID')
  const models = [...new Set(body.data.map(item => item.id))]
  if (!models.includes(config.model)) fail('DEEPSEEK_MODEL_UNAVAILABLE')
  return models
}

export function nextVersion(version) {
  const match = /^v(\d{1,6})\.(\d{1,6})\.(\d{1,6})$/.exec(version)
  if (!match || Number(match[3]) >= 999999) fail('COZE_VERSION_UNSUPPORTED')
  return `v${Number(match[1])}.${Number(match[2])}.${Number(match[3]) + 1}`
}

export function rebindCanvas(original, modelID, modelName, maxTokens, workflowTimeoutMs) {
  if(workflowTimeoutMs!==undefined && (!Number.isInteger(workflowTimeoutMs)||workflowTimeoutMs<30000||workflowTimeoutMs>180000)) fail('DEEPSEEK_WORKFLOW_TIMEOUT_INVALID')
  if (!nativeID(modelID) || typeof modelName !== 'string' || !modelName.trim() || !Number.isInteger(maxTokens) || maxTokens < 1 || maxTokens > 8192) fail('COZE_MODEL_BINDING_INVALID')
  if (!original || !Array.isArray(original.nodes) || !Array.isArray(original.edges)) fail('COZE_CANVAS_INVALID')
  const canvas = structuredClone(original)
  let count = 0
  for (const node of canvas.nodes) {
    if (String(node.type) !== '3') continue
    count++
    const fields = node.data?.inputs?.llmParam
    if (!Array.isArray(fields)) fail('COZE_CANVAS_INVALID')
    if(workflowTimeoutMs!==undefined) node.data.inputs.settingOnError={...node.data.inputs.settingOnError,timeoutMs:workflowTimeoutMs}
    for (const [name, value] of Object.entries({ modelType: modelID, modleName: modelName, maxTokens: String(maxTokens) })) {
      const matches = fields.filter(field => field.name === name)
      if (matches.length !== 1 || !matches[0].input?.value || matches[0].input.value.type !== 'literal') fail('COZE_CANVAS_INVALID')
      matches[0].input.value.content = value
    }
  }
  if (!count) fail('COZE_CANVAS_NO_LLM')
  return canvas
}

function detailItem(response, workflowID, spaceID, version) {
  // entity/vo/workflow_info.go:MarshalJSON serializes the list directly.
  const list = Array.isArray(response?.data) ? response.data : response?.data?.list
  if (!Array.isArray(list) || list.length !== 1 || list[0].workflow_id !== workflowID || list[0].space_id !== spaceID) fail('COZE_WORKFLOW_OWNERSHIP_INVALID')
  const item = list[0]
  if (!item.latest_flow_version || (version && item.flow_version !== version)) fail('COZE_WORKFLOW_NOT_PUBLISHED')
  return item
}

export async function applyDeepSeek({ config, state, call, persist, backup, logLevel }) {
  safeLogLevel([`LOG_LEVEL=${logLevel ?? 'trace'}`])
  if (!nativeID(state?.spaceId) || !state.workflows || requiredRoles.some(role => !nativeID(state.workflows[role]?.id))) fail('COZE_PRIVATE_STATE_INVALID')
  // The legacy Goal compiler remains an active API when configured; migrate it too.
  const roles = [...requiredRoles, ...(nativeID(state.workflows.goal?.id) ? ['goal'] : [])]
  const snapshots = []
  for (const role of roles) {
    const id = state.workflows[role].id
    const draft = (await call('/api/workflow_api/canvas', { space_id: state.spaceId, workflow_id: id })).data
    if (draft?.workflow?.workflow_id !== id || draft.workflow.space_id !== state.spaceId) fail('COZE_WORKFLOW_OWNERSHIP_INVALID')
    if (draft.vcs_data?.type !== 3) fail('COZE_UNPUBLISHED_DRAFT')
    const detail = detailItem(await call('/api/workflow_api/workflow_detail_info', {
      space_id: state.spaceId, workflow_filter_list: [{ workflow_id: id }],
    }), id, state.spaceId)
    if (detail.latest_flow_version !== draft.workflow_version) fail('COZE_WORKFLOW_VERSION_CONFLICT')
    let canvas
    try { canvas = JSON.parse(draft.workflow.schema_json) } catch { fail('COZE_CANVAS_INVALID') }
    nextVersion(detail.latest_flow_version)
    rebindCanvas(canvas, '1', 'preflight', config.maxTokens)
    snapshots.push({ role, workflow_id: id, version: detail.latest_flow_version, canvas })
  }
  const identityHash = hash(JSON.stringify([config.baseURL, config.model, config.apiKey]))
  const oldBinding = { model_id: state.modelId, registered_model: state.modelRegisteredKey }
  let modelID = state.deepseekModel?.identityHash === identityHash ? state.deepseekModel.id : undefined
  if (!nativeID(modelID)) {
    const created = await call('/api/admin/config/model/create', {
      model_class: 19, model_name: `轻融金 DeepSeek ${config.model}`,
      connection: { base_conn_info: { base_url: config.baseURL, api_key: config.apiKey, model: config.model } },
    })
    modelID = created.id
    if (!nativeID(modelID)) fail('COZE_MODEL_ID_INVALID')
    state.deepseekModel = { identityHash, id: modelID, model: config.model, baseURL: config.baseURL }
    state.modelMigrationHistory = [...(state.modelMigrationHistory || []), oldBinding]
    state.modelId = modelID
    state.modelClass = 19
    state.modelProvider = 'DeepSeek'
    state.modelMaxTokens = config.maxTokens
    state.modelRegisteredKey = config.model
    state.modelConnection = { base_url: config.baseURL, api_key: config.apiKey, model: config.model }
    await persist()
  }
  state.modelMaxTokens = config.maxTokens
  const report = { model_id: modelID, model: config.model, provider: 'DeepSeek', thinking_control_supported: false, workflows: [] }
  for (const snapshot of snapshots) {
    const rebound = rebindCanvas(snapshot.canvas, modelID, `轻融金 DeepSeek ${config.model}`, config.maxTokens, config.workflowTimeoutMs)
    const canvasHash = hash(JSON.stringify(rebound))
    const oldHash = hash(JSON.stringify(snapshot.canvas))
    if (oldHash === canvasHash) {
      report.workflows.push({ role: snapshot.role, workflow_id: snapshot.workflow_id, version: snapshot.version, unchanged: true })
      continue
    }
    await backup(snapshot)
    // Recheck latest release immediately before saving; never overwrite a changed draft.
    const current = (await call('/api/workflow_api/canvas', { space_id: state.spaceId, workflow_id: snapshot.workflow_id })).data
    if (current?.vcs_data?.type !== 3 || current.workflow_version !== snapshot.version || hash(current.workflow?.schema_json || '') !== hash(JSON.stringify(snapshot.canvas))) {
      // Native schema formatting may differ. Compare parsed canonical objects instead.
      let currentCanvas
      try { currentCanvas = JSON.parse(current?.workflow?.schema_json) } catch { fail('COZE_WORKFLOW_VERSION_CONFLICT') }
      if (current?.vcs_data?.type !== 3 || current.workflow_version !== snapshot.version || hash(JSON.stringify(currentCanvas)) !== oldHash) fail('COZE_WORKFLOW_VERSION_CONFLICT')
    }
    const version = nextVersion(snapshot.version)
    const commit = randomUUID()
    await call('/api/workflow_api/save', { workflow_id: snapshot.workflow_id, space_id: state.spaceId, schema: JSON.stringify(rebound), submit_commit_id: commit })
    // Native Save generates its own CommitID, ignoring request.submit_commit_id.
    const saved = (await call('/api/workflow_api/canvas', { space_id: state.spaceId, workflow_id: snapshot.workflow_id })).data
    let savedCanvas
    try { savedCanvas = JSON.parse(saved?.workflow?.schema_json) } catch { fail('COZE_WORKFLOW_VERSION_CONFLICT') }
    if (saved?.workflow?.workflow_id !== snapshot.workflow_id || saved.workflow.space_id !== state.spaceId || saved.workflow_version !== snapshot.version || hash(JSON.stringify(savedCanvas)) !== canvasHash || !nativeID(saved.vcs_data?.submit_commit_id)) fail('COZE_WORKFLOW_VERSION_CONFLICT')
    await call('/api/workflow_api/publish', {
      workflow_id: snapshot.workflow_id, space_id: state.spaceId, has_collaborator: false,
      force: true, workflow_version: version, version_description: `DeepSeek ${config.model}; existing schemas and financial guards retained`, commit_id: saved.vcs_data.submit_commit_id,
    })
    detailItem(await call('/api/workflow_api/workflow_detail_info', {
      space_id: state.spaceId, workflow_filter_list: [{ workflow_id: snapshot.workflow_id, workflow_version: version }],
    }), snapshot.workflow_id, state.spaceId, version)
    // Confirm the original immutable release remains queryable.
    detailItem(await call('/api/workflow_api/workflow_detail_info', {
      space_id: state.spaceId, workflow_filter_list: [{ workflow_id: snapshot.workflow_id, workflow_version: snapshot.version }],
    }), snapshot.workflow_id, state.spaceId, snapshot.version)
    const workflow = state.workflows[snapshot.role]
    workflow.published = true
    workflow.version = version
    workflow.revision = Number(version.split('.').at(-1))
    workflow.canvasHash = canvasHash
    workflow.modelId = modelID
    await persist()
    report.workflows.push({ role: snapshot.role, workflow_id: snapshot.workflow_id, version, previous_version: snapshot.version, unchanged: false })
  }
  return report
}

async function atomicJSON(path, value) {
  const temporary = `${path}.${randomUUID()}.tmp`
  await writeFile(temporary, JSON.stringify(value, null, 2) + '\n', { mode: 0o600 })
  await rename(temporary, path)
}

export function runtimeLogLevel(proof) {
  const lines = proof.split('\n').filter(line => line !== '')
  if (lines.length !== 1 || !/^[1-9][0-9]*\t[^\r\n]*$/.test(lines[0])) fail('COZE_LOG_LEVEL_UNVERIFIED')
  return safeLogLevel([`LOG_LEVEL=${lines[0].split('\t')[1]}`])
}

export function containerLogLevel() {
  let proof
  try {
    // The local launcher sources /env/.env.debug after Docker created the container.
    // Inspect only the running Go backend's actual environment, never Config.Env.
    const probe = `for p in /proc/[0-9]*; do
      exe=$(readlink "$p/exe" 2>/dev/null)
      case "$exe" in
        /tmp/go-build*/backend|/app/backend|/app/coze-server)
          level=$(tr '\\000' '\\n' < "$p/environ" | sed -n 's/^LOG_LEVEL=//p')
          printf '%s\\t%s\\n' "\${p##*/}" "$level"
          ;;
      esac
    done`
    proof = execFileSync('docker', ['exec', 'coze-server-local', 'sh', '-c', probe], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], maxBuffer: 8192 })
  } catch { fail('COZE_LOG_LEVEL_UNVERIFIED') }
  return runtimeLogLevel(proof)
}

export function createNativeCall(state, fetcher = fetch) {
  const operations = {
    '/api/passport/web/email/login/': 'LOGIN', '/api/workflow_api/canvas': 'CANVAS',
    '/api/workflow_api/workflow_detail_info': 'DETAIL', '/api/admin/config/model/create': 'MODEL_CREATE',
    '/api/workflow_api/save': 'SAVE', '/api/workflow_api/publish': 'PUBLISH',
  }
  return async (path, body) => {
    if (!Object.hasOwn(operations, path)) fail('COZE_NATIVE_OPERATION_INVALID')
    let response
    try {
      response = await fetcher(cozeBase + path, {
        method: 'POST', headers: { 'Content-Type': 'application/json', ...(state.cookie ? { Cookie: state.cookie } : {}) },
        body: JSON.stringify(body), redirect: 'error', signal: AbortSignal.timeout(90000),
      })
    } catch { fail('COZE_NATIVE_UNREACHABLE') }
    const data = await boundedJSON(response, 'COZE_NATIVE')
    if (data.code !== 0) fail(`COZE_NATIVE_REJECTED_${operations[path]}_${Number.isSafeInteger(data.code) && data.code >= 0 ? data.code : 'UNKNOWN'}`)
    const cookies = response.headers.getSetCookie()
    if (cookies.length) state.cookie = cookies.map(cookie => cookie.split(';')[0]).join('; ')
    return data
  }
}

export async function main(mode = 'Prepare') {
  const gatewayMode=['Apply','ApplyGateway','VerifyGateway','PrepareGateway'].includes(mode)
  const envPath = resolve(import.meta.dirname, gatewayMode?'.env.deepseek-gateway.local':'.env.deepseek.local')
  if (mode === 'Prepare'||mode==='PrepareGateway') {
    const example = await readFile(resolve(import.meta.dirname, gatewayMode?'.env.deepseek-gateway.example':'.env.deepseek.example'), 'utf8')
    try { await writeFile(envPath, example, { flag: 'wx', mode: 0o600 }) } catch (error) { if (error.code !== 'EEXIST') throw error }
    console.log('DeepSeek private configuration prepared; existing values preserved. No provider or Coze API called.')
    return
  }
  if (!['Verify','Apply','ApplyGateway','VerifyGateway','ApplyDirectUnbudgeted'].includes(mode)) fail('DEEPSEEK_MODE_INVALID')
  let env
  try { env = parseEnv(await readFile(envPath, 'utf8')) } catch (error) { if (error.message === 'DEEPSEEK_ENV_INVALID') throw error; fail('DEEPSEEK_ENV_MISSING') }
  const config = gatewayMode?buildGatewayConfig(env):buildConfig(env)
  const proof = gatewayMode?await verifyGateway(config):null
  const models = gatewayMode?[config.model]:await verifyModels(config)
  if (mode === 'Verify'||mode==='VerifyGateway') {
    console.log(JSON.stringify({ provider: 'DeepSeek', model: config.model, available_models: models, gateway_enforcement:proof,paid_chat_called: false, coze_modified: false }))
    return
  }
  const logLevel = containerLogLevel()
  const statePath = resolve(root, 'output/coze-local-private.json')
  let state
  try { state = JSON.parse(await readFile(statePath, 'utf8')) } catch { fail('COZE_PRIVATE_STATE_INVALID') }
  if (!state.email || !state.password) fail('COZE_PRIVATE_STATE_INVALID')
  const call = createNativeCall(state)
  await call('/api/passport/web/email/login/', { email: state.email, password: state.password })
  const persist = () => atomicJSON(statePath, state)
  await persist()
  const runID = randomUUID()
  const backupDirectory = resolve(root, 'output/deepseek-migration', runID)
  await mkdir(backupDirectory, { recursive: true })
  const report = await applyDeepSeek({
    config, state, call, persist, logLevel,
    backup: snapshot => atomicJSON(resolve(backupDirectory, `${snapshot.role}.previous.json`), snapshot),
  })
  report.native_log_level = logLevel
  report.gateway_enforcement = proof
  report.direct_unbudgeted = !gatewayMode
  report.provider_models_verified = true
  report.live_agent_validation_passed = false
  await atomicJSON(resolve(root, 'output/deepseek-config-result.json'), report)
  console.log(JSON.stringify(report))
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main(process.argv[2] || 'Prepare').catch(error => {
    const safeCode = /^(DEEPSEEK|COZE|MODEL_GATEWAY)_[A-Z0-9_]+$/.test(error.message) ? error.message : 'DEEPSEEK_CONFIGURATION_FAILED'
    console.error(safeCode)
    process.exitCode = 1
  })
}
