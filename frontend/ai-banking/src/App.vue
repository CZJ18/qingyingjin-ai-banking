<script setup lang="ts">
import { ref, watch, computed, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import { useQueryClient } from '@tanstack/vue-query'
import {
  DialogRoot,
  DialogPortal,
  DialogOverlay,
  DialogContent,
  DialogTitle,
  DialogDescription,
  DialogClose,
} from 'reka-ui'
import {
  LayoutDashboard,
  Sparkles,
  Network,
  ChartNoAxesCombined,
  Repeat2,
  Heart,
  Workflow,
  ShieldCheck,
  PanelLeftClose,
  PanelLeftOpen,
  Menu,
  ArrowUpRight,
  UserRound,
  LogOut,
  X,
  Wallet,
} from 'lucide-vue-next'
import Button from '@/components/ui/button/Button.vue'
import Notifications from '@/components/Notifications.vue'
import { useSession, useUI } from '@/lib/store'
import { useAccounts } from '@/lib/queries'
import { auth, type SmsCapabilities, type SmsChallenge } from '@/api/auth'
const session = useSession(),
  ui = useUI(),
  route = useRoute(),
  qc = useQueryClient(),
  accounts = useAccounts()
const nav = [
  { path: '/', title: 'AI Banking 总览', icon: LayoutDashboard },
  { path: '/assistant', title: 'AI 智能金融助手', icon: Sparkles },
  { path: '/agents', title: '多智能体协同', icon: Network },
  { path: '/billing', title: '智能账单分析', icon: ChartNoAxesCombined },
  { path: '/subscriptions', title: '订阅与代扣', icon: Repeat2 },
  { path: '/goals', title: '健康目标与目标金', icon: Heart },
  { path: '/planning', title: '动态资金规划', icon: Workflow },
  { path: '/security', title: '可信执行与安全', icon: ShieldCheck },
]
const active = computed(() => nav.find((n) => n.path === route.path))
const connection = ref(false),
  authMode = ref<'login' | 'register' | 'reset'>('login'),
  phone = ref(''),
  password = ref(''),
  confirmation = ref(''),
  displayName = ref(''),
  restoring = ref(true),
  busy = ref(false),
  error = ref(''),
  capabilities = ref<SmsCapabilities | null>(null),
  capabilityError = ref(''),
  capabilityLoading = ref(true),
  challenge = ref<SmsChallenge | null>(null),
  code = ref(''),
  currentPassword = ref(''),
  sendingCode = ref(false),
  now = ref(Date.now()),
  resendAt = ref(0),
  challengeExpiresAt = ref(0)
let codeAttempt = 0
const clock = setInterval(() => (now.value = Date.now()), 1000)
onUnmounted(() => clearInterval(clock))
const smsEnabled = computed(() => capabilities.value?.sms_enabled === true)
const codeRequired = computed(
  () =>
    authMode.value === 'reset' ||
    (authMode.value === 'register' &&
      capabilities.value?.registration_verification_required === true),
)
const retrySeconds = computed(() => Math.max(0, Math.ceil((resendAt.value - now.value) / 1000)))
const validCode = computed(
  () => !!challenge.value && /^\d{6}$/.test(code.value) && challengeExpiresAt.value > now.value,
)
const phoneFormatValid = computed(() => /^(?:\+?86)?1[3-9]\d{9}$/.test(phone.value.trim()))
const needsPhoneVerification = computed(
  () =>
    smsEnabled.value &&
    (!session.phoneVerified || session.phoneVerificationMethod !== capabilities.value?.sms_mode),
)
const phoneStatus = computed(() =>
  !session.phoneVerified
    ? '未验证'
    : session.phoneVerificationMethod === 'development'
      ? '开发验证（非真实手机号认证）'
      : session.phoneVerificationMethod === 'sms'
        ? '已验证'
        : '验证方式需重新确认',
)
const sendCodeLabel = computed(() =>
  sendingCode.value
    ? '正在发送…'
    : retrySeconds.value > 0
      ? `${retrySeconds.value} 秒后重发`
      : '发送验证码',
)
function clearChallenge() {
  codeAttempt++
  challenge.value = null
  code.value = ''
  resendAt.value = 0
  challengeExpiresAt.value = 0
}
async function loadCapabilities() {
  capabilityLoading.value = true
  capabilityError.value = ''
  try {
    capabilities.value = await auth.capabilities()
  } catch {
    capabilities.value = null
    capabilityError.value = '短信验证配置暂不可用，请稍后重试。'
  } finally {
    capabilityLoading.value = false
  }
}
watch(
  () => route.path,
  () => (session.mobileOpen = false),
)
watch(
  () => accounts.data.value,
  (rows) => {
    if (rows?.length && !session.accountId) session.accountId = rows[0]!.id
  },
)
watch(connection, (open) => {
  if (!open) {
    error.value = ''
    password.value = ''
    confirmation.value = ''
    currentPassword.value = ''
    clearChallenge()
  }
})
watch(authMode, () => {
  error.value = ''
  password.value = ''
  confirmation.value = ''
  clearChallenge()
})
watch(phone, clearChallenge)
watch(
  () => session.connected,
  (connected) => {
    if (!connected) {
      qc.clear()
      currentPassword.value = ''
      clearChallenge()
    }
  },
)
watch(
  () => session.authError,
  (message) => {
    if (message) ui.notify(message)
  },
)
onMounted(async () => {
  void loadCapabilities()
  try {
    await session.restoreSession()
  } catch (e) {
    ui.notify((e as Error).message)
  } finally {
    restoring.value = false
  }
})
async function authenticate() {
  if (busy.value) return
  error.value = ''
  if (!phoneFormatValid.value) {
    error.value = '请输入正确的中国大陆手机号'
    return
  }
  if (authMode.value !== 'login') {
    const length = Array.from(password.value).length
    if (length < 8 || length > 128 || new TextEncoder().encode(password.value).length > 512) {
      error.value = '密码须为 8–128 个字符'
      return
    }
    if (password.value !== confirmation.value) {
      error.value = '两次输入的密码不一致'
      return
    }
  }
  if (authMode.value === 'register' && !capabilities.value) {
    error.value = '请先获取短信验证配置后再注册'
    return
  }
  if (codeRequired.value && (!smsEnabled.value || !validCode.value)) {
    error.value = '请先获取并填写有效验证码'
    return
  }
  busy.value = true
  qc.clear()
  try {
    const input = { phone: phone.value.trim(), password: password.value }
    if (authMode.value === 'reset') {
      await session.resetPassword({
        phone: input.phone,
        challenge_id: challenge.value!.challenge_id,
        code: code.value,
        new_password: password.value,
      })
      qc.clear()
      authMode.value = 'login'
      password.value = ''
      confirmation.value = ''
      clearChallenge()
      ui.notify('密码已重置，请使用新密码登录')
      return
    } else if (authMode.value === 'register') {
      await session.register({
        ...input,
        ...(codeRequired.value
          ? { challenge_id: challenge.value!.challenge_id, code: code.value }
          : {}),
        ...(displayName.value.trim() ? { display_name: displayName.value.trim() } : {}),
      })
    } else {
      await session.login(input)
    }
    password.value = ''
    confirmation.value = ''
    await qc.invalidateQueries()
    connection.value = false
    ui.notify(authMode.value === 'register' ? '注册成功，已登录轻融金' : '登录成功')
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    busy.value = false
  }
}
async function sendCode(purpose: 'registration' | 'password-reset' | 'phone-verification') {
  if (sendingCode.value || busy.value || retrySeconds.value > 0 || !smsEnabled.value) return
  if (purpose !== 'phone-verification' && !phoneFormatValid.value) {
    error.value = '请输入正确的中国大陆手机号'
    return
  }
  const attempt = ++codeAttempt,
    owner = session.user?.id
  sendingCode.value = true
  error.value = ''
  try {
    const result =
      purpose === 'registration'
        ? await auth.sendRegistrationCode(phone.value.trim())
        : purpose === 'password-reset'
          ? await auth.sendPasswordResetCode(phone.value.trim())
          : await auth.sendPhoneVerificationCode()
    if (
      attempt !== codeAttempt ||
      !connection.value ||
      (purpose === 'phone-verification' && session.user?.id !== owner)
    )
      return
    challenge.value = result
    code.value = ''
    now.value = Date.now()
    resendAt.value = now.value + result.retry_after * 1000
    challengeExpiresAt.value = now.value + result.expires_in * 1000
    ui.notify('验证码已写入本机开发短信收件箱')
  } catch (e) {
    if (attempt === codeAttempt) error.value = (e as Error).message
  } finally {
    sendingCode.value = false
  }
}
async function verifyPhone() {
  if (busy.value || sendingCode.value || !validCode.value || !currentPassword.value) return
  busy.value = true
  error.value = ''
  try {
    await session.verifyPhone({
      challenge_id: challenge.value!.challenge_id,
      code: code.value,
      current_password: currentPassword.value,
    })
    currentPassword.value = ''
    clearChallenge()
    ui.notify('已完成开发验证；这不代表真实手机号认证')
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    busy.value = false
  }
}
async function disconnect() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    await session.logout()
    qc.clear()
    connection.value = false
    authMode.value = 'login'
    ui.notify('已退出登录')
  } catch (e) {
    error.value = (e as Error).message
    ui.notify('退出登录未完成，请重试')
    connection.value = true
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <a class="skip-link" href="#main">跳至主内容</a>
  <div class="app-shell" :class="{ collapsed: session.collapsed }">
    <div v-if="session.mobileOpen" class="mobile-shade" @click="session.mobileOpen = false" />
    <aside
      class="sidebar"
      :class="{ open: session.mobileOpen }"
      @keydown.esc="session.mobileOpen = false"
    >
      <RouterLink to="/" class="brand"
        ><span class="brand-mark"><Wallet :size="24" /></span
        ><span class="brand-copy"
          ><b>轻融金<span class="brand-dot">.</span></b
          ><small>AI BANKING</small></span
        ></RouterLink
      >
      <div class="sidebar-section">工作空间</div>
      <nav aria-label="主导航">
        <RouterLink
          v-for="n in nav"
          :key="n.path"
          :to="n.path"
          :class="{ active: route.path === n.path }"
          :title="n.title"
          :aria-label="n.title"
          :aria-current="route.path === n.path ? 'page' : undefined"
          @click="session.mobileOpen = false"
          ><component :is="n.icon" :size="19" /><span>{{ n.title }}</span
          ><span v-if="n.path === '/assistant'" class="nav-ai" aria-hidden="true"
            >AI</span
          ></RouterLink
        >
      </nav>
      <div class="sidebar-bottom">
        <div class="trust-note">
          <ShieldCheck :size="22" /><b>每一步，都有据可循</b>
          <p>授权执行 · 风险校验 · 全程审计</p>
          <RouterLink to="/security">查看安全中心 <ArrowUpRight :size="14" /></RouterLink>
        </div>
        <button
          class="collapse-button"
          @click="session.collapsed = !session.collapsed"
          :aria-label="session.collapsed ? '展开导航' : '收起导航'"
        >
          <component :is="session.collapsed ? PanelLeftOpen : PanelLeftClose" :size="18" /><span
            >收起导航</span
          >
        </button>
        <div class="sidebar-footer">轻融金 · AI Banking <span>v2.0</span></div>
      </div>
    </aside>
    <div class="workspace">
      <header class="topbar">
        <button
          class="icon-button mobile-menu"
          @click="session.mobileOpen = true"
          aria-label="打开导航"
        >
          <Menu />
        </button>
        <div class="breadcrumb">
          工作空间 <span>/</span><b>{{ active?.title }}</b>
        </div>
        <div class="toolbar-actions">
          <span class="connection-state" :class="{ online: session.connected }"
            ><i />{{
              session.connected ? '已登录' : restoring ? '正在恢复登录…' : '尚未登录'
            }}</span
          ><select v-if="session.connected" v-model="session.accountId" aria-label="当前账户">
            <option value="">全部账户</option>
            <option v-for="a in accounts.data.value" :key="a.id" :value="a.id">
              {{ a.display_name }} {{ a.masked_account_number }}
            </option></select
          ><Button
            v-if="!session.connected"
            variant="outline"
            :disabled="restoring"
            @click="connection = true"
            ><UserRound :size="15" />登录 / 注册</Button
          ><button
            v-else
            class="icon-button"
            @click="disconnect"
            :disabled="busy"
            aria-label="退出登录"
          >
            <LogOut :size="18" /></button
          ><Notifications /><span class="toolbar-divider" /><button
            class="user-avatar"
            @click="connection = true"
            :disabled="restoring || busy"
            aria-label="账户资料"
          >
            {{ session.user?.display_name?.slice(0, 1) || '轻' }}
          </button>
        </div>
      </header>
      <main id="main" tabindex="-1"><RouterView :key="session.sessionEpoch" /></main>
      <footer class="workspace-footer">
        <span><ShieldCheck :size="13" />模拟银行环境 · 非真实资金交易</span
        ><span>健康生活目标，让金融更有温度</span>
      </footer>
    </div>
  </div>
  <DialogRoot v-model:open="connection"
    ><DialogPortal
      ><DialogOverlay class="dialog-overlay" /><DialogContent class="dialog-content"
        ><DialogTitle>{{
          session.connected
            ? '账户资料'
            : authMode === 'login'
              ? '登录轻融金'
              : authMode === 'reset'
                ? '找回密码'
                : '注册轻融金'
        }}</DialogTitle
        ><DialogDescription>{{
          session.connected
            ? '管理你的登录身份。金融操作仍需单独核对和授权。'
            : '使用手机号和密码，管理你的健康生活目标与模拟银行账户。'
        }}</DialogDescription>
        <p
          v-if="
            capabilities?.sms_mode === 'development' && (session.connected || authMode !== 'login')
          "
          class="sms-development-notice"
          role="note"
        >
          开发短信不会发送到手机，请使用本机开发短信收件箱读取验证码。
        </p>
        <template v-if="session.connected">
          <dl class="auth-profile">
            <div>
              <dt>昵称</dt>
              <dd>{{ session.user?.display_name || '轻融金用户' }}</dd>
            </div>
            <div>
              <dt>手机号</dt>
              <dd>{{ session.phoneMasked || '当前身份未绑定手机号' }}</dd>
            </div>
            <div>
              <dt>验证状态</dt>
              <dd>{{ phoneStatus }}</dd>
            </div>
          </dl>
          <form v-if="needsPhoneVerification" @submit.prevent="verifyPhone">
            <label
              >当前密码<input
                v-model="currentPassword"
                type="password"
                autocomplete="current-password"
                required
                :disabled="busy || sendingCode"
            /></label>
            <div class="sms-code-row">
              <label
                >验证码<input
                  v-model="code"
                  type="text"
                  inputmode="numeric"
                  autocomplete="one-time-code"
                  pattern="[0-9]{6}"
                  maxlength="6"
                  placeholder="6 位验证码"
                  required
                  :disabled="busy || sendingCode"
              /></label>
              <Button
                type="button"
                variant="outline"
                :disabled="busy || sendingCode || retrySeconds > 0 || !session.phoneMasked"
                @click="sendCode('phone-verification')"
                >{{ sendCodeLabel }}</Button
              >
            </div>
            <p class="form-hint">验证当前账户已绑定的手机号；需要当前密码及本次验证码。</p>
            <Button
              type="submit"
              :disabled="busy || sendingCode || !validCode || !currentPassword"
              >{{ busy ? '正在验证…' : '确认验证手机号' }}</Button
            >
          </form>
          <p v-if="capabilityError" class="form-error" role="alert">
            {{ capabilityError
            }}<button type="button" class="sms-retry" @click="loadCapabilities">重试</button>
          </p>
          <p class="form-hint">当前为模拟银行环境。登录不会授予智能体自动转账权限。</p>
          <p v-if="error" class="form-error" role="alert">{{ error }}</p>
          <div class="dialog-actions">
            <DialogClose as-child
              ><Button variant="outline" :disabled="busy">关闭</Button></DialogClose
            >
            <Button :disabled="busy" @click="disconnect">{{
              busy ? '正在退出…' : '退出登录'
            }}</Button>
          </div>
        </template>
        <form v-else @submit.prevent="authenticate">
          <label
            >手机号<input
              v-model="phone"
              type="tel"
              inputmode="tel"
              autocomplete="username"
              placeholder="请输入手机号"
              maxlength="14"
              required
              :disabled="busy || sendingCode"
          /></label>
          <label v-if="authMode === 'register'"
            >昵称（选填）<input
              v-model="displayName"
              autocomplete="nickname"
              maxlength="60"
              placeholder="如何称呼你"
              :disabled="busy || sendingCode"
          /></label>
          <label
            >{{ authMode === 'reset' ? '新密码' : '密码'
            }}<input
              v-model="password"
              type="password"
              :autocomplete="authMode === 'login' ? 'current-password' : 'new-password'"
              :placeholder="authMode === 'login' ? '请输入密码' : '设置 8–128 个字符的密码'"
              required
              :disabled="busy || sendingCode"
          /></label>
          <label v-if="authMode !== 'login'"
            >{{ authMode === 'reset' ? '确认新密码' : '确认密码'
            }}<input
              v-model="confirmation"
              type="password"
              autocomplete="new-password"
              placeholder="再次输入密码"
              required
              :disabled="busy || sendingCode"
          /></label>
          <div v-if="codeRequired && smsEnabled" class="sms-code-row">
            <label
              >验证码<input
                v-model="code"
                type="text"
                inputmode="numeric"
                autocomplete="one-time-code"
                pattern="[0-9]{6}"
                maxlength="6"
                placeholder="6 位验证码"
                required
                :disabled="busy || sendingCode"
            /></label>
            <Button
              type="button"
              variant="outline"
              :disabled="busy || sendingCode || retrySeconds > 0 || !phoneFormatValid"
              @click="sendCode(authMode === 'reset' ? 'password-reset' : 'registration')"
              >{{ sendCodeLabel }}</Button
            >
          </div>
          <p v-if="challenge && now >= challengeExpiresAt" class="form-error" role="alert">
            验证码已过期，请重新获取。
          </p>
          <p v-if="!smsEnabled && !capabilityLoading && !capabilityError" class="form-hint">
            短信验证未启用，暂不能找回密码。
          </p>
          <p v-if="capabilityError" class="form-error" role="alert">
            {{ capabilityError
            }}<button type="button" class="sms-retry" @click="loadCapabilities">重试</button>
          </p>
          <p v-if="authMode === 'reset'" class="form-hint">
            重置成功后需使用新密码重新登录；原有登录会话将失效。
          </p>
          <p v-if="error" class="form-error" role="alert">{{ error }}</p>
          <div class="dialog-actions">
            <DialogClose as-child
              ><Button variant="outline" :disabled="busy">取消</Button></DialogClose
            ><Button
              type="submit"
              :disabled="
                busy ||
                sendingCode ||
                !phone.trim() ||
                !password ||
                (authMode === 'register' && !capabilities) ||
                (codeRequired && !validCode)
              "
              >{{
                busy
                  ? '正在处理…'
                  : authMode === 'login'
                    ? '登录'
                    : authMode === 'reset'
                      ? '重置密码'
                      : '注册并登录'
              }}</Button
            >
          </div>
          <p v-if="authMode === 'login'" class="auth-switch">
            <button
              type="button"
              :disabled="busy || sendingCode || !smsEnabled"
              @click="authMode = 'reset'"
            >
              忘记密码
            </button>
          </p>
          <p class="auth-switch">
            {{ authMode === 'login' ? '还没有账号？' : '已有账号？'
            }}<button
              type="button"
              :disabled="busy || sendingCode"
              @click="authMode = authMode === 'login' ? 'register' : 'login'"
            >
              {{ authMode === 'login' ? '立即注册' : '去登录' }}
            </button>
          </p>
        </form>
        <DialogClose class="dialog-x" aria-label="关闭" :disabled="busy"
          ><X :size="18" /></DialogClose></DialogContent></DialogPortal
  ></DialogRoot>
  <Transition name="toast"
    ><div v-if="ui.toast" class="toast" role="status">
      <ShieldCheck :size="18" />{{ ui.toast
      }}<button @click="ui.toast = ''" aria-label="关闭通知"><X :size="15" /></button></div
  ></Transition>
</template>
<style scoped>
.auth-profile {
  margin: 24px 0;
}
.auth-profile > div {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 0;
  border-bottom: 1px solid var(--border, #e5ebe9);
}
.auth-profile dt {
  color: var(--muted, #6b7f78);
}
.auth-profile dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.auth-switch {
  margin: 20px 0 0;
  text-align: center;
  font-size: 13px;
  color: #6b7f78;
}
.auth-switch button {
  border: 0;
  background: none;
  color: var(--teal, #0d9488);
  cursor: pointer;
  margin-left: 6px;
  font: inherit;
}
.auth-switch button:disabled {
  cursor: wait;
  opacity: 0.6;
}
.sms-development-notice {
  background: #fff8e8;
  border: 1px solid #ead7aa;
  border-radius: 8px;
  padding: 12px;
  color: #74551c;
  font-size: 12px;
  line-height: 1.6;
}
.sms-code-row {
  display: flex;
  align-items: flex-end;
  gap: 12px;
  margin: 12px 0;
}
.sms-code-row > label {
  flex: 1;
  min-width: 0;
  margin: 0;
}
.sms-code-row > button {
  flex-shrink: 0;
}
.sms-retry {
  border: 0;
  background: transparent;
  color: var(--teal, #0d9488);
  cursor: pointer;
  margin-left: 8px;
}
</style>
