<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Schema } from '@/api/client'
import { money } from '@/lib/utils'
import Chart from '@/components/Chart.vue'
import State from '@/components/State.vue'
const props = defineProps<{ report: Schema<'BillingReport'> }>()
const grain = ref<'daily' | 'weekly' | 'monthly'>('daily'),
  direction = ref<'expense' | 'income'>('expense')
const rows = computed(() => props.report[grain.value] || [])
const option = computed(() => ({
  tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
  grid: { top: 16, bottom: 28, left: 8, right: 16, containLabel: true },
  xAxis: {
    type: 'category',
    data: rows.value.map((d) => d.label),
    axisLabel: {
      fontSize: 12,
      hideOverlap: true,
      formatter: (label: string) => (grain.value === 'daily' ? label.slice(-5) : label),
    },
  },
  yAxis: {
    type: 'value',
    min: 0,
    splitNumber: 4,
    axisLabel: { fontSize: 12, formatter: (n: number) => '¥' + n.toLocaleString('zh-CN') },
  },
  series: [
    {
      name: direction.value === 'expense' ? '支出' : '收入',
      type: 'bar',
      barMaxWidth: grain.value === 'daily' ? 18 : 44,
      itemStyle: {
        color: direction.value === 'expense' ? '#142D4E' : '#0D9488',
        borderRadius: [4, 4, 0, 0],
      },
      data: rows.value.map(
        (d) => (direction.value === 'expense' ? d.expense_minor : d.income_minor) / 100,
      ),
    },
  ],
}))
</script>
<template>
  <section class="billing-surface expense-trends">
    <header>
      <div>
        <h2>收支趋势</h2>
        <p>以日常支出为主，收入使用独立视图。</p>
      </div>
      <div class="trend-grain" aria-label="趋势时间粒度">
        <button
          v-for="g in ['daily', 'weekly', 'monthly'] as const"
          :key="g"
          :aria-pressed="grain === g"
          :class="{ active: grain === g }"
          @click="grain = g"
        >
          {{ { daily: '日', weekly: '周', monthly: '月' }[g] }}
        </button>
      </div>
    </header>
    <div class="trend-context">
      <div class="trend-direction">
        <button
          :class="{ active: direction === 'expense' }"
          :aria-pressed="direction === 'expense'"
          @click="direction = 'expense'"
        >
          支出趋势</button
        ><button
          :class="{ active: direction === 'income' }"
          :aria-pressed="direction === 'income'"
          @click="direction = 'income'"
        >
          收入趋势
        </button>
      </div>
      <span
        >周期收入 <b>{{ money(report.total_income) }}</b></span
      >
    </div>
    <State
      :connected="true"
      :empty="!rows.length"
      title="此粒度暂无收支记录"
      description="切换粒度或日期，查看已记录的实际收支。"
      ><Chart
        :option="option"
        :label="(direction === 'expense' ? '支出' : '收入') + '趋势，使用独立金额尺度'"
    /></State>
    <footer>
      <i :class="direction" /><span
        >{{ direction === 'expense' ? '实际支出' : '实际收入' }} · 人民币元</span
      ><span>{{ rows.length }} 个时间区间</span>
    </footer>
  </section>
</template>
<style scoped>
.billing-surface {
  background: #fff;
  border: 1px solid #e7edf3;
  border-radius: 12px;
  min-width: 0;
  overflow: hidden;
}
.expense-trends > header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 22px 24px 16px;
}
.expense-trends h2 {
  font-size: 17px;
  margin: 0;
  font-weight: 600;
}
.expense-trends header p {
  font-size: 14px;
  color: #596779;
  line-height: 1.6;
  margin: 8px 0 0;
}
.trend-grain,
.trend-direction {
  display: flex;
  gap: 4px;
  flex: none;
}
.trend-grain {
  background: #f3f6f9;
  padding: 3px;
  border-radius: 7px;
}
.trend-grain button,
.trend-direction button {
  font-size: 13px;
  border: 0;
  background: none;
  padding: 6px 10px;
  border-radius: 5px;
  color: #596779;
  cursor: pointer;
}
.trend-grain button.active {
  background: #fff;
  box-shadow: 0 1px 3px #142d4e10;
  color: #142d4e;
}
.trend-direction button.active {
  background: #edf5f4;
  color: #0d766e;
  font-weight: 600;
}
.trend-context {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: center;
  padding: 0 24px 12px;
}
.trend-context > span {
  font-size: 13px;
  color: #596779;
}
.trend-context b {
  font-weight: 500;
  color: #172033;
  margin-left: 6px;
  font-variant-numeric: tabular-nums;
}
.expense-trends :deep(.chart) {
  height: 244px;
  margin: 0 12px;
}
.expense-trends footer {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 12px 24px 20px;
  font-size: 13px;
  color: #596779;
}
.expense-trends footer > span:last-child {
  margin-left: auto;
}
.expense-trends footer i {
  width: 8px;
  height: 8px;
  border-radius: 2px;
  background: #142d4e;
}
.expense-trends footer i.income {
  background: #0d9488;
}
.expense-trends button:focus-visible {
  outline: 2px solid #0d9488;
  outline-offset: 2px;
}
@media (max-width: 600px) {
  .expense-trends > header {
    padding: 20px 16px;
    align-items: flex-start;
    flex-wrap: wrap;
  }
  .expense-trends header p {
    font-size: 13px;
  }
  .trend-context {
    padding: 0 16px 12px;
    flex-wrap: wrap;
  }
  .expense-trends :deep(.chart) {
    margin: 0 4px;
    height: 220px;
  }
  .expense-trends footer {
    padding: 12px 16px 20px;
    font-size: 12px;
  }
}
</style>
