import { ApiError, authorizationHeaders, type Schema } from './client'

export type LoginInput = { phone: string; password: string }
export type RegisterInput = LoginInput & {
  display_name?: string
  challenge_id?: string
  code?: string
}
export type SmsCapabilities = {
  sms_enabled: boolean
  sms_mode: 'disabled' | 'development'
  registration_verification_required: boolean
  code_expires_in: number
  resend_after: number
}
export type SmsChallenge = { challenge_id: string; expires_in: number; retry_after: number }
export type PhoneVerificationInput = {
  challenge_id: string
  code: string
  current_password: string
}
export type PasswordResetInput = {
  phone: string
  challenge_id: string
  code: string
  new_password: string
}
export type BrowserSession = {
  access_token: string
  expires_in: number
  user: Schema<'User'>
  phone_masked: string
  phone_verified?: boolean
  phone_verification_method?: string
}

type SessionAction = 'register' | 'login' | 'refresh' | 'logout' | 'bind'
type AuthAction =
  | SessionAction
  | 'capabilities'
  | 'sms/registration'
  | 'sms/password-reset'
  | 'sms/phone-verification'
  | 'phone/verify'
  | 'password/reset'
const sessionActions = new Set<AuthAction>(['register', 'login', 'refresh', 'bind'])
const protectedActions = new Set<AuthAction>(['bind', 'sms/phone-verification', 'phone/verify'])
const challengeActions = new Set<AuthAction>([
  'sms/registration',
  'sms/password-reset',
  'sms/phone-verification',
])

// Only these same-origin endpoints receive the HttpOnly refresh cookie.
// Financial requests and task streams continue to use the in-memory Bearer.
async function authRequest<T>(action: AuthAction, body: unknown = {}): Promise<T> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), 20000)
  try {
    const response = await fetch(`/api/v1/auth/${action}`, {
      method: action === 'capabilities' ? 'GET' : 'POST',
      credentials: action === 'capabilities' ? 'omit' : 'same-origin',
      redirect: 'error',
      cache: 'no-store',
      signal: controller.signal,
      headers: {
        Accept: 'application/json',
        ...(action !== 'capabilities' ? { 'Content-Type': 'application/json' } : {}),
        ...(protectedActions.has(action) ? authorizationHeaders() : {}),
      },
      body: action === 'capabilities' ? undefined : JSON.stringify(body),
    })
    const envelope = await response.json().catch(() => {
      throw new ApiError('INVALID_RESPONSE', '登录服务返回了无法识别的响应', '', response.status)
    })
    if (!envelope || typeof envelope !== 'object' || Array.isArray(envelope))
      throw new ApiError('INVALID_RESPONSE', '登录服务返回了无法识别的响应', '', response.status)
    if (!response.ok || envelope.code !== 'OK') {
      throw new ApiError(
        typeof envelope.code === 'string' ? envelope.code : 'AUTH_ERROR',
        typeof envelope.message === 'string' ? envelope.message : '登录请求失败，请重试',
        envelope.request_id,
        response.status,
      )
    }
    if (sessionActions.has(action)) {
      const data = envelope.data as Partial<BrowserSession> | undefined
      if (
        !data ||
        typeof data.access_token !== 'string' ||
        !data.access_token.trim() ||
        !Number.isInteger(data.expires_in) ||
        Number(data.expires_in) <= 0 ||
        Number(data.expires_in) > 86400 ||
        typeof data.user?.id !== 'string' ||
        !data.user.id ||
        typeof data.phone_masked !== 'string' ||
        (data.phone_verified !== undefined && typeof data.phone_verified !== 'boolean') ||
        (data.phone_verification_method !== undefined &&
          typeof data.phone_verification_method !== 'string')
      )
        throw new ApiError('INVALID_RESPONSE', '登录服务返回的会话无效', '', response.status)
    } else if (action === 'capabilities') {
      const data = envelope.data as Partial<SmsCapabilities> | undefined
      if (
        !data ||
        typeof data.sms_enabled !== 'boolean' ||
        !['disabled', 'development'].includes(data.sms_mode ?? '') ||
        data.sms_enabled !== (data.sms_mode === 'development') ||
        typeof data.registration_verification_required !== 'boolean' ||
        (!data.sms_enabled && data.registration_verification_required) ||
        !Number.isInteger(data.code_expires_in) ||
        Number(data.code_expires_in) < 1 ||
        Number(data.code_expires_in) > 3600 ||
        !Number.isInteger(data.resend_after) ||
        Number(data.resend_after) < 1 ||
        Number(data.resend_after) > 3600
      )
        throw new ApiError('INVALID_RESPONSE', '短信验证配置响应无效', '', response.status)
    } else if (challengeActions.has(action)) {
      const data = envelope.data as (Partial<SmsChallenge> & { code?: unknown }) | undefined
      if (
        !data ||
        typeof data.challenge_id !== 'string' ||
        !data.challenge_id ||
        data.challenge_id.length > 128 ||
        'code' in data ||
        !Number.isInteger(data.expires_in) ||
        Number(data.expires_in) < 1 ||
        Number(data.expires_in) > 3600 ||
        !Number.isInteger(data.retry_after) ||
        Number(data.retry_after) < 1 ||
        Number(data.retry_after) > 3600
      )
        throw new ApiError('INVALID_RESPONSE', '短信验证请求响应无效', '', response.status)
    } else if (envelope.data !== null) {
      throw new ApiError(
        'INVALID_RESPONSE',
        '身份验证服务返回了无法识别的响应',
        '',
        response.status,
      )
    }
    return envelope.data as T
  } catch (error) {
    if (error instanceof ApiError) throw error
    throw new ApiError(
      controller.signal.aborted ? 'TIMEOUT' : 'NETWORK_ERROR',
      controller.signal.aborted ? '登录请求超时，请重试' : '无法连接登录服务，请检查网络后重试',
    )
  } finally {
    clearTimeout(timer)
  }
}

export const auth = {
  register: (input: RegisterInput) => authRequest<BrowserSession>('register', input),
  login: (input: LoginInput) => authRequest<BrowserSession>('login', input),
  refresh: () => authRequest<BrowserSession>('refresh'),
  logout: () => authRequest<null>('logout'),
  bind: (input: LoginInput) => authRequest<BrowserSession>('bind', input),
  capabilities: () => authRequest<SmsCapabilities>('capabilities'),
  sendRegistrationCode: (phone: string) => authRequest<SmsChallenge>('sms/registration', { phone }),
  sendPasswordResetCode: (phone: string) =>
    authRequest<SmsChallenge>('sms/password-reset', { phone }),
  sendPhoneVerificationCode: () => authRequest<SmsChallenge>('sms/phone-verification'),
  verifyPhone: (input: PhoneVerificationInput) => authRequest<null>('phone/verify', input),
  resetPassword: (input: PasswordResetInput) => authRequest<null>('password/reset', input),
}
