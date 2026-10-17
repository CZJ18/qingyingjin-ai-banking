<script setup lang="ts">
import { ref, computed, watch, onBeforeUnmount } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { agentApi } from '@/api/agent-client'
import { useSession, useUI } from '@/lib/store'
import { money, minor } from '@/lib/utils'
import Button from '@/components/ui/button/Button.vue'
import State from '@/components/State.vue'
import JsonDetails from '@/components/JsonDetails.vue'
const s = useSession(),
  ui = useUI(),
  reserve = ref(''),
  busy = ref(false),
  error = ref('')
let active = true
onBeforeUnmount(() => {
  active = false
})
const profile = useQuery({
  queryKey: computed(() => ['product-profile', s.sessionEpoch]),
  enabled: computed(() => s.connected),
  queryFn: ({ signal }) => agentApi.profile(signal),
})
const recommendations = useQuery({
  queryKey: computed(() => ['product-recommendations', s.sessionEpoch]),
  enabled: computed(() => s.connected),
  queryFn: ({ signal }) => agentApi.recommendations(signal),
})
watch(
  () => profile.data.value,
  (value) => {
    if (value) reserve.value = String(value.safety_reserve_minor / 100)
  },
)
async function save() {
  if (busy.value || !profile.data.value) return
  busy.value = true
  const epoch = s.sessionEpoch
  const current = () => active && s.connected && s.sessionEpoch === epoch
  error.value = ''
  try {
    await agentApi.saveProfile({
      expected_version: profile.data.value.revision,
      timezone: profile.data.value.timezone,
      safety_reserve_minor: minor(reserve.value),
    })
    if (!current()) return
    await profile.refetch()
    if (!current()) return
    ui.notify('资金安全偏好已保存')
  } catch (e) {
    if (!current()) return
    error.value = (e as Error).message
    await profile.refetch()
  } finally {
    busy.value = false
  }
}
function explanation(payload: Record<string, unknown>) {
  return typeof payload.explanation === 'string'
    ? payload.explanation
    : '查看方案中的完整建议及数据依据。'
}
</script>
<template>
  <section class="bank-panel product-profile" aria-label="个人资金偏好与建议">
    <header class="bank-section-heading">
      <div>
        <h2>个人资金偏好与建议</h2>
        <p>把正常生活和资金安全放在目标之前。</p>
      </div>
    </header>
    <State
      :connected="s.connected"
      :loading="profile.isLoading.value"
      :error="profile.error.value"
      @retry="profile.refetch()"
    >
      <form v-if="profile.data.value" class="p6-profile-form" @submit.prevent="save">
        <label
          >资金安全预留偏好 / 元<input
            v-model="reserve"
            inputmode="decimal"
            required
            :disabled="busy"
        /></label>
        <div>
          <small
            >当前偏好：{{ money(profile.data.value.safety_reserve_minor) }} ·
            {{ profile.data.value.timezone }}</small
          >
          <p class="form-hint">
            保存偏好后，可在资金规划中主动应用；已创建的计划和余额由各自业务规则管理。
          </p>
        </div>
        <Button type="submit" :disabled="busy">{{ busy ? '正在保存…' : '保存资金偏好' }}</Button>
      </form>
      <p v-if="error" class="form-error" role="alert">{{ error }}</p>
      <State
        :connected="s.connected"
        :loading="recommendations.isLoading.value"
        :error="recommendations.error.value"
        :empty="!recommendations.data.value?.length"
        compact
        title="暂无已同步建议"
        description="在健康目标页编译方案并同步后，可以在这里查看建议来源。"
        @retry="recommendations.refetch()"
      >
        <div class="p6-recommendations">
          <article v-for="r in recommendations.data.value?.slice(0, 3)" :key="r.source_id">
            <b>生活目标建议 #{{ r.source_id }}</b>
            <p>{{ explanation(r.payload) }}</p>
            <small
              >来源版本 {{ r.source_version }} ·
              {{ new Date(r.observed_at).toLocaleString('zh-CN') }}</small
            ><JsonDetails label="查看数据依据" :value="r.evidence" /><RouterLink to="/goals"
              >查看方案与反馈 →</RouterLink
            >
          </article>
        </div>
      </State>
    </State>
  </section>
</template>
<style scoped>
.product-profile {
  margin: 24px 0;
  padding: 24px;
}
.p6-profile-form {
  display: grid;
  grid-template-columns: minmax(160px, 240px) 1fr auto;
  gap: 20px;
  align-items: center;
  margin: 20px 0;
}
.p6-recommendations {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}
.p6-recommendations article {
  border: 1px solid var(--border, #e0e8f2);
  padding: 18px;
  border-radius: 10px;
  min-width: 0;
}
.p6-recommendations small {
  color: #6b7c93;
}
.p6-recommendations p {
  line-height: 1.7;
}
.p6-recommendations a {
  color: #008b91;
}
@media (max-width: 900px) {
  .p6-profile-form,
  .p6-recommendations {
    grid-template-columns: 1fr;
  }
  .p6-profile-form button {
    justify-self: start;
  }
}
</style>
