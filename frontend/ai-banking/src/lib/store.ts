import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  ApiError,
  setToken,
  renewToken as renewAccessToken,
  request,
  type Schema,
} from '@/api/client'
import {
  auth,
  type BrowserSession,
  type LoginInput,
  type RegisterInput,
  type PhoneVerificationInput,
  type PasswordResetInput,
} from '@/api/auth'
const validUUID = (value: unknown): value is string =>
  typeof value === 'string' &&
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value)
const validAgentTask = (value: unknown): value is string =>
  typeof value === 'string' && value.startsWith('lg-') && validUUID(value.slice(3))
const validNumeric = (value: unknown): value is string =>
  typeof value === 'string' &&
  /^[1-9][0-9]{0,18}$/.test(value) &&
  BigInt(value) <= 9223372036854775807n
export const useSession = defineStore('session', () => {
  const connected = ref(false),
    accountId = ref(''),
    collapsed = ref(false),
    mobileOpen = ref(false)
  const user = ref<Schema<'User'> | null>(null)
  const phoneMasked = ref(''),
    phoneVerified = ref(false),
    phoneVerificationMethod = ref(''),
    authError = ref('')
  // Restore references only after the server verifies their user namespace.
  // Credentials, balances and responses remain in memory.
  const jobIds = ref<string[]>([]),
    goalIds = ref<string[]>([]),
    planId = ref(''),
    selectedJob = ref('')
  const agentTaskIds = ref<string[]>([]),
    selectedAgentTask = ref('')
  const lifestyleGoalIds = ref<string[]>([]),
    lifestyleGoalId = ref(''),
    lifestylePlanId = ref(''),
    billingReportId = ref('')
  const sessionEpoch = ref(0)
  let connectionAttempt = 0
  let refreshTimer: ReturnType<typeof setTimeout> | undefined
  function stopRefresh() {
    clearTimeout(refreshTimer)
    refreshTimer = undefined
  }
  function clearMemory() {
    connected.value = false
    user.value = null
    phoneMasked.value = ''
    phoneVerified.value = false
    phoneVerificationMethod.value = ''
    accountId.value = ''
    jobIds.value = []
    goalIds.value = []
    planId.value = ''
    selectedJob.value = ''
    agentTaskIds.value = []
    selectedAgentTask.value = ''
    lifestyleGoalIds.value = []
    lifestyleGoalId.value = ''
    lifestylePlanId.value = ''
    billingReportId.value = ''
    sessionEpoch.value++
  }
  function referenceKey() {
    return user.value ? 'qingrongjin.refs.v1.' + encodeURIComponent(user.value.id) : ''
  }
  function restore() {
    try {
      const saved = JSON.parse(localStorage.getItem(referenceKey()) || '{}')
      const ids = (name: string, validate: (value: unknown) => boolean): string[] =>
        Array.isArray(saved[name])
          ? ([...new Set(saved[name].filter(validate))].slice(0, 100) as string[])
          : []
      jobIds.value = ids('jobs', validUUID)
      goalIds.value = ids('goals', validNumeric)
      agentTaskIds.value = ids('agents', validAgentTask)
      lifestyleGoalIds.value = ids('lifestyle', validNumeric)
      planId.value = validNumeric(saved.plan) ? saved.plan : ''
      lifestylePlanId.value = validNumeric(saved.lifestylePlan) ? saved.lifestylePlan : ''
      billingReportId.value = validUUID(saved.billingReport) ? saved.billingReport : ''
      selectedJob.value = jobIds.value[0] || ''
      selectedAgentTask.value = agentTaskIds.value[0] || ''
      lifestyleGoalId.value = lifestyleGoalIds.value[0] || ''
    } catch {
      /* Unavailable storage must not prevent an authenticated session. */
    }
  }
  function persist() {
    if (!connected.value || !user.value) return
    try {
      localStorage.setItem(
        referenceKey(),
        JSON.stringify({
          jobs: jobIds.value,
          goals: goalIds.value,
          plan: planId.value,
          agents: agentTaskIds.value,
          lifestyle: lifestyleGoalIds.value,
          lifestylePlan: lifestylePlanId.value,
          billingReport: billingReportId.value,
        }),
      )
    } catch {
      /* The current session remains usable when storage is disabled. */
    }
  }
  function beginConnection() {
    const attempt = ++connectionAttempt
    stopRefresh()
    clearMemory()
    setToken('')
    authError.value = ''
    return attempt
  }
  async function verifyConnection(jwt: string, attempt: number, expectedUserId?: string) {
    if (attempt !== connectionAttempt) throw new Error('登录已被取消')
    setToken(jwt)
    try {
      const verified = await request<Schema<'User'>>('/api/v1/users/me')
      if (attempt !== connectionAttempt) throw new Error('登录已被取消')
      if (expectedUserId && verified.id !== expectedUserId)
        throw new Error('登录身份校验失败，请重新登录')
      user.value = verified
      connected.value = true
      restore()
    } catch (e) {
      if (attempt === connectionAttempt) {
        setToken('')
        clearMemory()
      }
      throw e
    }
  }
  async function connect(jwt: string) {
    const attempt = beginConnection()
    await verifyConnection(jwt, attempt)
  }
  function scheduleRefresh(expiresIn: number) {
    stopRefresh()
    const delay = Math.max(1000, (expiresIn > 60 ? expiresIn - 60 : expiresIn / 2) * 1000)
    refreshTimer = setTimeout(() => {
      refreshTimer = undefined
      void refreshSession().catch(() => {
        /* The state exposes the authentication error. */
      })
    }, delay)
  }
  async function acceptSession(result: BrowserSession, attempt: number) {
    await verifyConnection(result.access_token, attempt, result.user.id)
    setPhoneStatus(result)
    scheduleRefresh(result.expires_in)
  }
  function setPhoneStatus(result: BrowserSession) {
    phoneMasked.value = result.phone_masked
    phoneVerified.value = result.phone_verified === true
    phoneVerificationMethod.value = result.phone_verification_method ?? ''
  }
  async function authenticate(action: 'login' | 'register', input: RegisterInput) {
    const attempt = beginConnection()
    try {
      const result = await auth[action](input)
      if (attempt !== connectionAttempt) throw new Error('登录已被取消')
      await acceptSession(result, attempt)
    } catch (error) {
      if (attempt === connectionAttempt) {
        setToken('')
        clearMemory()
      }
      throw error
    }
  }
  const login = (input: LoginInput) => authenticate('login', input)
  const register = (input: RegisterInput) => authenticate('register', input)
  async function restoreSession() {
    if (connected.value) return true
    const attempt = beginConnection()
    try {
      const result = await auth.refresh()
      if (attempt !== connectionAttempt) return false
      await acceptSession(result, attempt)
      return true
    } catch (error) {
      if (attempt !== connectionAttempt) return false
      setToken('')
      clearMemory()
      if (error instanceof ApiError && error.status === 401) return false
      throw error
    }
  }
  async function renewToken(jwt: string, expectedUserId: string) {
    const attempt = connectionAttempt
    if (!connected.value || user.value?.id !== expectedUserId) throw new Error('登录已变化')
    renewAccessToken(jwt)
    const verified = await request<Schema<'User'>>('/api/v1/users/me')
    if (attempt !== connectionAttempt || user.value?.id !== expectedUserId)
      throw new Error('登录已变化')
    if (verified.id !== expectedUserId) throw new Error('登录身份校验失败，请重新登录')
    user.value = verified
  }
  async function refreshSession() {
    const attempt = connectionAttempt,
      owner = user.value?.id
    if (!connected.value || !owner) return
    try {
      const result = await auth.refresh()
      if (attempt !== connectionAttempt || user.value?.id !== owner) return
      if (result.user.id !== owner) throw new Error('登录身份已变化，请重新登录')
      await renewToken(result.access_token, owner)
      if (attempt !== connectionAttempt || user.value?.id !== owner) return
      setPhoneStatus(result)
      scheduleRefresh(result.expires_in)
    } catch (error) {
      if (attempt === connectionAttempt) {
        disconnect()
        authError.value = '登录已失效，请重新登录'
      }
      throw error
    }
  }
  async function resetPassword(input: PasswordResetInput) {
    const attempt = connectionAttempt
    await auth.resetPassword(input)
    if (attempt === connectionAttempt) disconnect()
  }
  async function verifyPhone(input: PhoneVerificationInput) {
    const attempt = connectionAttempt,
      owner = user.value?.id
    if (!connected.value || !owner) throw new Error('请先登录')
    await auth.verifyPhone(input)
    if (attempt !== connectionAttempt || user.value?.id !== owner) throw new Error('登录已变化')
    await refreshSession()
  }
  async function logout() {
    const attempt = ++connectionAttempt
    stopRefresh()
    try {
      await auth.logout()
      if (attempt === connectionAttempt) disconnect()
    } catch (error) {
      // A network failure cannot prove server revocation. Keep the identity and
      // let the user retry rather than displaying a successful logout.
      if (attempt === connectionAttempt && connected.value) scheduleRefresh(120)
      throw error
    }
  }
  function disconnect() {
    connectionAttempt++
    stopRefresh()
    setToken('')
    clearMemory()
  }
  function saveJob(id: string) {
    if (saveList(jobIds, id, validUUID)) selectedJob.value = id
  }
  function saveGoal(id: string) {
    saveList(goalIds, id, validNumeric)
  }
  function savePlan(id: string) {
    if (connected.value && validNumeric(id)) {
      planId.value = id
      persist()
    }
  }
  function saveList(list: typeof jobIds, id: string, validate: (value: unknown) => boolean) {
    if (!connected.value || !validate(id)) return false
    list.value = [id, ...list.value.filter((x) => x !== id)].slice(0, 100)
    persist()
    return true
  }
  function saveAgentTask(id: string) {
    if (saveList(agentTaskIds, id, validAgentTask)) selectedAgentTask.value = id
  }
  function saveLifestyleGoal(id: string) {
    if (!connected.value || !validNumeric(id)) return
    if (lifestyleGoalId.value !== id) lifestylePlanId.value = ''
    if (saveList(lifestyleGoalIds, id, validNumeric)) lifestyleGoalId.value = id
  }
  function saveLifestylePlan(id: string) {
    if (connected.value && validNumeric(id)) {
      lifestylePlanId.value = id
      persist()
    }
  }
  function clearLifestylePlan() {
    lifestylePlanId.value = ''
    persist()
  }
  function saveBillingReport(id: string) {
    if (connected.value && validUUID(id)) {
      billingReportId.value = id
      persist()
    }
  }
  return {
    connected,
    accountId,
    collapsed,
    mobileOpen,
    user,
    phoneMasked,
    phoneVerified,
    phoneVerificationMethod,
    authError,
    sessionEpoch,
    agentTaskIds,
    selectedAgentTask,
    lifestyleGoalIds,
    lifestyleGoalId,
    lifestylePlanId,
    billingReportId,
    jobIds,
    goalIds,
    planId,
    selectedJob,
    connect,
    login,
    register,
    restoreSession,
    renewToken,
    logout,
    resetPassword,
    verifyPhone,
    disconnect,
    saveJob,
    saveGoal,
    savePlan,
    saveAgentTask,
    saveLifestyleGoal,
    saveLifestylePlan,
    clearLifestylePlan,
    saveBillingReport,
  }
})
export const useUI = defineStore('ui', () => {
  const toast = ref('')
  let timer: ReturnType<typeof setTimeout>
  function notify(text: string) {
    toast.value = text
    clearTimeout(timer)
    timer = setTimeout(() => (toast.value = ''), 6000)
  }
  return { toast, notify }
})
