<script setup lang="ts">
import { ref, watch, onMounted, onBeforeUnmount } from 'vue'
import * as echarts from 'echarts/core'
import { LineChart, PieChart, BarChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { EChartsCoreOption } from 'echarts/core'
echarts.use([
  LineChart,
  PieChart,
  BarChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  CanvasRenderer,
])
const props = defineProps<{ option: EChartsCoreOption; label: string }>()
const root = ref<HTMLDivElement>()
let chart: echarts.ECharts | undefined
let observer: ResizeObserver
const fontFamily =
  '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif'
const object = (value: unknown): Record<string, unknown> =>
  value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {}
const axisTheme = (value: unknown) => {
  const axis = object(value)
  return {
    ...axis,
    axisLine: { show: false, ...object(axis.axisLine) },
    axisTick: { show: false, ...object(axis.axisTick) },
    axisLabel: { color: '#64748B', fontSize: 12, hideOverlap: true, ...object(axis.axisLabel) },
    splitLine: {
      ...object(axis.splitLine),
      lineStyle: { color: '#EAF0F5', type: 'dashed', ...object(object(axis.splitLine).lineStyle) },
    },
  }
}
const render = () => {
  const option = props.option
  const narrow = (root.value?.clientWidth || 600) < 400
  const axes = (axis: unknown) => (Array.isArray(axis) ? axis.map(axisTheme) : axisTheme(axis))
  chart?.setOption(
    {
      ...option,
      color: ['#0D9488', '#142D4E', '#7EA9BF', '#E4AF68', '#8996B3'],
      textStyle: { fontFamily, color: '#64748B', fontSize: 12, ...object(option.textStyle) },
      animationDuration: 220,
      tooltip: {
        trigger: 'axis',
        confine: true,
        backgroundColor: '#142D4E',
        borderWidth: 0,
        padding: [12, 16],
        textStyle: { color: '#FFFFFF', fontFamily, fontSize: 13 },
        extraCssText: 'border-radius:10px;box-shadow:0 8px 24px #142d4e26;',
        valueFormatter: (value: number) =>
          new Intl.NumberFormat('zh-CN', { style: 'currency', currency: 'CNY' }).format(value),
        ...object(option.tooltip),
      },
      grid: {
        left: narrow ? 8 : 12,
        right: 12,
        top: 24,
        bottom: 44,
        containLabel: true,
        ...object(option.grid),
      },
      ...(option.legend
        ? {
            legend: {
              type: 'scroll',
              itemWidth: 10,
              itemHeight: 10,
              itemGap: 16,
              textStyle: { fontFamily, color: '#64748B', fontSize: 12 },
              ...object(option.legend),
            },
          }
        : {}),
      ...(option.xAxis ? { xAxis: axes(option.xAxis) } : {}),
      ...(option.yAxis ? { yAxis: axes(option.yAxis) } : {}),
    },
    true,
  )
}
onMounted(() => {
  chart = echarts.init(root.value!)
  render()
  observer = new ResizeObserver(() => {
    chart?.resize()
    render()
  })
  observer.observe(root.value!)
})
watch(() => props.option, render, { deep: true })
onBeforeUnmount(() => {
  observer?.disconnect()
  chart?.dispose()
})
</script>
<template><div ref="root" class="chart" role="img" :aria-label="label" /></template>
