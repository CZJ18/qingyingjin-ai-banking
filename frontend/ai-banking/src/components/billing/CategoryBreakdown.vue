<script setup lang="ts">
import { computed, ref, watch, onMounted, onBeforeUnmount } from 'vue'
import * as echarts from 'echarts/core'
import { PieChart } from 'echarts/charts'
import { TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { Schema } from '@/api/client'
import { money } from '@/lib/utils'
echarts.use([PieChart, TooltipComponent, CanvasRenderer])
const props = defineProps<{
  categories: Schema<'BillingReport'>['category_breakdown']
  total: number
}>()
defineEmits<{ filter: [category: string] }>()
const root = ref<HTMLDivElement>(),
  hovered = ref('')
let chart: echarts.ECharts | undefined, observer: ResizeObserver
const palette = [
  '#0D9488',
  '#142D4E',
  '#6E9AAF',
  '#D9A15D',
  '#8596AE',
  '#5B8C78',
  '#9A8172',
  '#748394',
]
function color(name: string) {
  const known: Record<string, number> = {
    dining: 0,
    餐饮: 0,
    food: 0,
    transport: 1,
    交通: 1,
    health: 2,
    健康运动: 2,
    education: 3,
    教育学习: 3,
    subscription: 4,
    订阅: 4,
    日常生活: 5,
    shopping: 5,
  }
  let hash = 0
  for (const c of name) hash = (hash * 31 + c.charCodeAt(0)) >>> 0
  return palette[known[name] ?? hash % palette.length]
}
const rows = computed(() =>
  props.categories
    .filter((c) => c.expense_minor > 0)
    .sort((a, b) => b.expense_minor - a.expense_minor),
)
function render() {
  chart?.setOption(
    {
      animationDuration: 200,
      tooltip: {
        trigger: 'item',
        confine: true,
        backgroundColor: '#142D4E',
        borderWidth: 0,
        textStyle: { color: '#fff', fontSize: 13 },
        formatter: (p: { name: string; value: number }) =>
          `${echarts.format.encodeHTML(p.name)}<br/>${money(p.value * 100)} · ${props.total > 0 ? (((p.value * 100) / props.total) * 100).toFixed(1) : '0.0'}%`,
      },
      series: [
        {
          type: 'pie',
          radius: ['68%', '87%'],
          center: ['50%', '50%'],
          label: { show: false },
          emphasis: { scale: true, scaleSize: 4 },
          itemStyle: { borderColor: '#fff', borderWidth: 3, borderRadius: 3 },
          data: rows.value.map((c) => ({
            name: c.category,
            value: c.expense_minor / 100,
            itemStyle: { color: color(c.category) },
          })),
        },
      ],
    },
    true,
  )
}
function highlight(name: string) {
  hovered.value = name
  chart?.dispatchAction({ type: 'downplay', seriesIndex: 0 })
  if (name) chart?.dispatchAction({ type: 'highlight', seriesIndex: 0, name })
}
onMounted(() => {
  chart = echarts.init(root.value!)
  render()
  chart.on('mouseover', (p) => {
    hovered.value = p.name
  })
  chart.on('globalout', () => {
    hovered.value = ''
  })
  observer = new ResizeObserver(() => {
    chart?.resize()
  })
  observer.observe(root.value!)
})
watch(() => [props.categories, props.total], render, { deep: true })
onBeforeUnmount(() => {
  observer?.disconnect()
  chart?.dispose()
})
</script>
<template>
  <section class="category-analysis">
    <header>
      <h2>消费分类</h2>
      <span>按周期实际支出</span>
    </header>
    <div class="category-content">
      <div class="donut-wrap">
        <div
          ref="root"
          class="category-chart"
          role="img"
          aria-label="各消费分类占周期总支出的比例"
        />
        <div class="donut-center">
          <span>周期总支出</span><strong>{{ money(total) }}</strong>
        </div>
      </div>
      <div class="category-list">
        <div v-if="!rows.length" class="category-empty">本周期没有分类支出。</div>
        <button
          v-for="row in rows"
          :key="row.category"
          :class="{ highlighted: hovered === row.category }"
          :aria-label="'查看' + row.category + '交易'"
          @mouseenter="highlight(row.category)"
          @mouseleave="highlight('')"
          @focus="highlight(row.category)"
          @blur="highlight('')"
          @click="$emit('filter', row.category)"
        >
          <i :style="{ background: color(row.category) }" /><span>{{ row.category }}</span
          ><b>{{ money(row.expense_minor) }}</b
          ><small>{{ total > 0 ? ((row.expense_minor / total) * 100).toFixed(1) : '0.0' }}%</small>
        </button>
      </div>
    </div>
    <footer>点击分类查看对应交易 · 占比以周期总支出为分母</footer>
  </section>
</template>
<style scoped>
.category-analysis {
  background: #fff;
  border: 1px solid #e7edf3;
  border-radius: 12px;
  min-width: 0;
  overflow: hidden;
}
.category-analysis > header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 22px 24px 16px;
}
.category-analysis h2 {
  font-size: 17px;
  margin: 0;
}
.category-analysis header > span {
  font-size: 13px;
  color: #596779;
}
.category-content {
  display: grid;
  grid-template-columns: minmax(160px, 0.85fr) minmax(0, 1.15fr);
  gap: 8px;
  padding: 0 16px;
  align-items: center;
  min-height: 270px;
}
.donut-wrap {
  position: relative;
  min-width: 0;
}
.category-chart {
  height: 238px;
  min-width: 0;
}
.donut-center {
  position: absolute;
  left: 50%;
  top: 50%;
  transform: translate(-50%, -50%);
  text-align: center;
  pointer-events: none;
  width: 70%;
  display: grid;
  gap: 8px;
}
.donut-center span {
  font-size: 12px;
  color: #596779;
}
.donut-center strong {
  font-size: 18px;
  font-weight: 600;
  letter-spacing: -0.3px;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
.category-list {
  max-height: 276px;
  overflow-y: auto;
  min-width: 0;
}
.category-list button {
  display: grid;
  grid-template-columns: 7px minmax(0, 1fr) auto 46px;
  gap: 8px;
  align-items: center;
  width: 100%;
  text-align: left;
  border: 0;
  border-bottom: 1px solid #edf1f5;
  background: #fff;
  padding: 13px 8px;
  cursor: pointer;
  transition: background 0.18s;
}
.category-list button.highlighted,
.category-list button:hover {
  background: #f0f7f5;
}
.category-list i {
  width: 7px;
  height: 7px;
  border-radius: 50%;
}
.category-list span {
  font-size: 14px;
  color: #172033;
  overflow-wrap: anywhere;
}
.category-list b {
  font-size: 14px;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.category-list small {
  font-size: 12px;
  color: #596779;
  text-align: right;
  font-variant-numeric: tabular-nums;
}
.category-analysis footer {
  font-size: 12px;
  color: #596779;
  padding: 16px 24px 20px;
  line-height: 1.7;
}
.category-empty {
  font-size: 14px;
  line-height: 1.8;
  color: #596779;
  padding: 16px;
}
.category-list button:focus-visible {
  outline: 2px solid #0d9488;
  outline-offset: -2px;
}
@media (max-width: 1250px) {
  .category-content {
    grid-template-columns: 1fr;
    min-height: 0;
  }
  .category-chart {
    height: 188px;
  }
  .category-list {
    max-height: 160px;
  }
  .category-analysis footer {
    padding-top: 12px;
  }
  .donut-center strong {
    font-size: 20px;
  }
}
@media (max-width: 600px) {
  .category-analysis > header {
    padding: 20px 16px;
  }
  .category-content {
    padding: 0 12px;
  }
  .category-chart {
    height: 200px;
  }
  .category-list {
    max-height: 260px;
  }
  .category-list span,
  .category-list b {
    font-size: 14px;
  }
  .category-analysis footer {
    padding: 16px;
  }
}
</style>
