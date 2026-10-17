<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ArrowRight, Clock3 } from 'lucide-vue-next'
import type { Schema } from '@/api/client'
import { money, date } from '@/lib/utils'
import Chart from '@/components/Chart.vue'
import JsonDetails from '@/components/JsonDetails.vue'
const props = defineProps<{ versions: Schema<'FinancialPlanVersion'>[] }>()
const chosen = ref('')
const ordered = computed(() => [...props.versions].sort((a, b) => b.version - a.version))
watch(
  () => ordered.value[0]?.id,
  (next, old) => {
    if (old && chosen.value === old && next !== old) chosen.value = ''
  },
)
const current = computed(() => ordered.value.find((v) => v.id === chosen.value) || ordered.value[0])
const previous = computed(() =>
  ordered.value.find((v) => v.version === current.value?.previous_version),
)
const fields = [
  { key: 'balance_minor', label: '账户资金' },
  { key: 'necessary_reserve_minor', label: '必要支出预留' },
  { key: 'disposable_minor', label: '可支配资金' },
  { key: 'suggested_savings_minor', label: '建议储蓄' },
] as const
const sources = computed(() => current.value?.snapshot.sources || [])
const chart = computed(() => {
  const ascending = [...ordered.value].reverse()
  return {
    tooltip: { trigger: 'axis' },
    legend: { bottom: 0, icon: 'circle', textStyle: { fontSize: 13 } },
    grid: { left: 8, right: 16, top: 16, bottom: 40, containLabel: true },
    xAxis: { type: 'category', data: ascending.map((v) => 'v' + v.version) },
    yAxis: { type: 'value', axisLabel: { formatter: (n: number) => '¥' + n } },
    series: [
      {
        name: '可支配资金',
        type: 'line',
        smooth: false,
        symbolSize: 6,
        data: ascending.map((v) => v.result.disposable_minor / 100),
      },
      {
        name: '建议储蓄',
        type: 'line',
        smooth: false,
        symbolSize: 6,
        data: ascending.map((v) => v.result.suggested_savings_minor / 100),
      },
    ],
  }
})
function delta(key: (typeof fields)[number]['key']) {
  const before = previous.value?.result[key],
    after = current.value?.result[key]
  return typeof before === 'number' &&
    typeof after === 'number' &&
    Number.isFinite(before) &&
    Number.isFinite(after)
    ? after - before
    : null
}
</script>
<template>
  <div class="plan-history-workspace">
    <section class="history-timeline">
      <header>
        <h3>资金计划时间线</h3>
        <span>版本记录</span>
      </header>
      <ol>
        <li v-for="v in ordered" :key="v.id">
          <button
            :class="{ active: current?.id === v.id }"
            :aria-pressed="current?.id === v.id"
            @click="chosen = v.id"
          >
            <i />
            <div>
              <div class="timeline-title">
                <b>v{{ v.version }}<span v-if="v.id === ordered[0]?.id">最新</span></b
                ><small>{{ date(v.created_at) }}</small>
              </div>
              <p>{{ v.reason }}</p>
              <span class="version-state">{{
                v.needs_confirmation ? '待确认规划建议' : '规划建议已确认'
              }}</span>
            </div>
          </button>
        </li>
      </ol>
    </section>
    <section v-if="current" class="version-inspection">
      <header>
        <div>
          <h3>
            方案变化 · v{{ current.previous_version }} <ArrowRight :size="14" /> v{{
              current.version
            }}
          </h3>
          <p>{{ current.reason }}</p>
        </div>
        <span><Clock3 :size="14" />{{ date(current.created_at) }}</span>
      </header>
      <div class="comparison-wrap">
        <table>
          <thead>
            <tr>
              <th>资金指标</th>
              <th>原方案</th>
              <th>新方案</th>
              <th>变化</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="field in fields" :key="field.key">
              <td>{{ field.label }}</td>
              <td>{{ money(previous?.result[field.key]) }}</td>
              <td>{{ money(current.result[field.key]) }}</td>
              <td :class="{ changed: delta(field.key) !== null && delta(field.key) !== 0 }">
                {{
                  delta(field.key) == null
                    ? '—'
                    : (delta(field.key)! > 0 ? '+' : '') + money(delta(field.key))
                }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="!previous" class="history-note">上一个版本未在当前记录中返回，暂不计算差额。</p>
      <div class="trigger-details">
        <div>
          <span>触发事件</span
          ><b>{{ current.event_id ? '#' + current.event_id : '未关联独立事件' }}</b>
        </div>
        <div>
          <span>服务端事件影响</span><b>{{ money(current.impact_minor) }}</b>
        </div>
        <div>
          <span>确认状态</span
          ><b>{{ current.needs_confirmation ? '需要确认建议' : '建议已确认' }}</b>
        </div>
      </div>
      <details class="source-references">
        <summary>对应交易与数据来源 · {{ sources.length }}</summary>
        <div v-for="source in sources" :key="source.type + source.id" class="source-row">
          <span>{{ source.type }}</span
          ><b>#{{ source.id }}</b
          ><span>版本 {{ source.version }}</span>
        </div>
        <p v-if="!sources.length">此版本未返回直接数据引用。</p>
        <RouterLink to="/billing">查看交易流水<ArrowRight :size="13" /></RouterLink>
      </details>
      <JsonDetails :value="current.snapshot" label="查看触发数据与完整快照" />
      <div class="version-trend">
        <h4>历史计划趋势</h4>
        <Chart :option="chart" label="服务端历史规划版本的可支配资金与建议储蓄" />
      </div>
    </section>
  </div>
</template>
<style scoped>
.plan-history-workspace {
  display: grid;
  grid-template-columns: 280px minmax(0, 1fr);
  font-size: 14px;
  color: #172033;
}
.history-timeline {
  border-right: 1px solid #e7edf3;
  max-height: 780px;
  overflow: auto;
}
.history-timeline > header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 20px;
}
.history-timeline h3,
.version-inspection h3 {
  font-size: 15px;
  margin: 0;
  font-weight: 600;
}
.history-timeline header span {
  font-size: 12px;
  color: #64748b;
}
.history-timeline ol {
  padding: 0 12px 16px;
  margin: 0;
  list-style: none;
}
.history-timeline button {
  display: flex;
  gap: 12px;
  padding: 16px 12px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  width: 100%;
  text-align: left;
  cursor: pointer;
  transition: background 0.18s;
}
.history-timeline button.active {
  background: #edf6f4;
}
.history-timeline button:hover {
  background: #f3f7f9;
}
.history-timeline button > i {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #94a3b8;
  flex: none;
  margin-top: 6px;
}
.history-timeline button.active > i {
  background: #0d9488;
}
.history-timeline button > div {
  min-width: 0;
  flex: 1;
}
.timeline-title {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  align-items: center;
}
.timeline-title b {
  font-size: 14px;
  font-weight: 600;
  color: #142d4e;
}
.timeline-title b span {
  font-size: 11px;
  padding: 2px 5px;
  background: #d8eee7;
  color: #0d766e;
  border-radius: 4px;
  margin-left: 6px;
  font-weight: 400;
}
.timeline-title small {
  font-size: 12px;
  color: #64748b;
}
.history-timeline p {
  font-size: 14px;
  line-height: 1.7;
  color: #475569;
  margin: 8px 0;
  overflow-wrap: anywhere;
}
.version-state {
  font-size: 12px;
  color: #64748b;
}
.version-inspection {
  padding: 24px;
  min-width: 0;
}
.version-inspection > header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
  margin-bottom: 20px;
}
.version-inspection h3 {
  display: flex;
  align-items: center;
  gap: 8px;
}
.version-inspection header p {
  color: #475569;
  font-size: 14px;
  line-height: 1.7;
  margin: 10px 0 0;
  overflow-wrap: anywhere;
}
.version-inspection header > span {
  display: flex;
  gap: 5px;
  align-items: center;
  font-size: 12px;
  color: #64748b;
  white-space: nowrap;
}
.comparison-wrap {
  overflow-x: auto;
}
.comparison-wrap table {
  width: 100%;
  border-collapse: collapse;
  font-size: 14px;
}
.comparison-wrap th {
  background: #f7f9fb;
  color: #64748b;
  font-size: 13px;
  font-weight: 500;
  padding: 12px;
  text-align: right;
  white-space: nowrap;
}
.comparison-wrap td {
  padding: 14px 12px;
  border-bottom: 1px solid #edf1f5;
  text-align: right;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.comparison-wrap th:first-child,
.comparison-wrap td:first-child {
  text-align: left;
}
.comparison-wrap td:nth-child(3) {
  font-weight: 600;
  color: #142d4e;
}
.comparison-wrap td.changed {
  color: #a16207;
  background: #fffcf4;
}
.history-note {
  font-size: 13px;
  color: #64748b;
  margin: 12px 0;
}
.trigger-details {
  display: flex;
  flex-wrap: wrap;
  gap: 24px;
  padding: 20px 0;
}
.trigger-details > div {
  display: grid;
  gap: 8px;
}
.trigger-details span {
  font-size: 13px;
  color: #64748b;
}
.trigger-details b {
  font-size: 14px;
  font-weight: 500;
}
.source-references {
  padding: 14px;
  background: #f8fafc;
  border-radius: 8px;
  font-size: 14px;
}
.source-references summary {
  cursor: pointer;
  color: #475569;
}
.source-row {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  font-size: 13px;
  padding: 10px 0;
  border-bottom: 1px solid #e7edf3;
}
.source-row span {
  color: #64748b;
}
.source-row b {
  font-weight: 500;
  overflow-wrap: anywhere;
}
.source-references a {
  display: flex;
  gap: 6px;
  align-items: center;
  color: #0d766e;
  margin-top: 12px;
  font-size: 13px;
}
.version-inspection :deep(pre) {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.version-trend {
  margin-top: 24px;
  border-top: 1px solid #e7edf3;
  padding-top: 20px;
}
.version-trend h4 {
  font-size: 14px;
  margin: 0 0 12px;
  font-weight: 600;
}
.version-trend :deep(.chart) {
  height: 200px;
}
.history-timeline button:focus-visible {
  outline: 2px solid #0d9488;
  outline-offset: 2px;
}
@media (max-width: 1050px) {
  .plan-history-workspace {
    grid-template-columns: 240px minmax(0, 1fr);
  }
  .version-inspection {
    padding: 20px;
  }
  .version-inspection > header {
    flex-direction: column;
    gap: 8px;
  }
  .comparison-wrap th,
  .comparison-wrap td {
    padding: 12px 8px;
    font-size: 13px;
  }
}
@media (max-width: 750px) {
  .plan-history-workspace {
    grid-template-columns: 1fr;
  }
  .history-timeline {
    border-right: 0;
    border-bottom: 1px solid #e7edf3;
    max-height: 300px;
  }
  .version-inspection {
    padding: 20px 16px;
  }
  .comparison-wrap table {
    min-width: 420px;
  }
  .trigger-details {
    gap: 20px;
  }
  .version-inspection h3 {
    font-size: 14px;
  }
  .version-trend :deep(.chart) {
    height: 190px;
  }
}
</style>
