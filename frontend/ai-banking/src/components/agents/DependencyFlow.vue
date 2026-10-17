<script setup lang="ts">
import { computed } from 'vue'
import type { TaskRow } from './task-view'
import { roleNames } from './task-view'
import Status from '@/components/Status.vue'
const props = defineProps<{
  tasks: TaskRow[]
  dependencies: Array<{ id: string; task_id: string; depends_on: string }>
  selected: string
  strategy: string
  dependenciesKnown?: boolean
}>()
defineEmits<{ select: [id: string] }>()
const layout = computed(() => {
  const levels = new Map<string, number>(),
    pending = new Set(props.tasks.map((t) => t.task_id))
  for (let pass = 0; pass < props.tasks.length; pass++)
    for (const t of props.tasks) {
      if (!pending.has(t.task_id)) continue
      const deps = props.dependencies
        .filter((d) => d.task_id === t.task_id)
        .map((d) => d.depends_on)
      if (deps.every((id) => levels.has(id))) {
        levels.set(t.task_id, deps.length ? Math.max(...deps.map((id) => levels.get(id)!)) + 1 : 0)
        pending.delete(t.task_id)
      }
    }
  const unresolved = [...pending]
  const rows = props.tasks.map((t) => ({
    ...t,
    level: levels.get(t.task_id) ?? (levels.size ? Math.max(...levels.values()) + 1 : 0),
  }))
  const max = Math.max(0, ...rows.map((t) => t.level))
  const columns = Array.from({ length: max + 1 }, (_, level) =>
    rows.filter((t) => t.level === level),
  )
  const height = Math.max(220, ...columns.map((c) => c.length * 120 + 40))
  const width = Math.max(580, columns.length * 252 + 32)
  const nodes = columns.flatMap((column, i) =>
    column.map((task, j) => ({
      task,
      x: 20 + i * 252,
      y: (height - column.length * 120) / 2 + j * 120,
    })),
  )
  const edges = props.dependencies.flatMap((d) => {
    const from = nodes.find((n) => n.task.task_id === d.depends_on),
      to = nodes.find((n) => n.task.task_id === d.task_id)
    return from && to
      ? [
          {
            id: d.id,
            path: `M ${from.x + 216} ${from.y + 48} C ${from.x + 234} ${from.y + 48}, ${to.x - 18} ${to.y + 48}, ${to.x} ${to.y + 48}`,
          },
        ]
      : []
  })
  return { nodes, edges, width, height, unresolved }
})
</script>
<template>
  <div class="dependency-flow">
    <header>
      <div><b>Agent 协同流程</b><span>Supervisor · 调度与依赖管理</span></div>
      <span>{{ strategy === 'parallel_read' ? '只读并行调度' : '顺序调度' }}</span>
    </header>
    <p v-if="layout.unresolved.length" class="graph-warning">
      部分依赖无法分层，请核对服务端依赖记录。
    </p>
    <p v-if="dependenciesKnown === false" class="graph-warning">
      当前展示已返回的专业节点；任务依赖尚未返回。
    </p>
    <div class="graph-scroll" tabindex="0" aria-label="任务依赖图，可横向滚动查看">
      <div
        class="graph-stage"
        :style="{ width: layout.width + 'px', height: layout.height + 'px' }"
      >
        <svg :width="layout.width" :height="layout.height" aria-hidden="true">
          <defs>
            <marker
              id="dependency-arrow"
              markerWidth="6"
              markerHeight="6"
              refX="5"
              refY="3"
              orient="auto"
            >
              <path d="M0,0 L6,3 L0,6" fill="#8fa5ba" />
            </marker>
          </defs>
          <path
            v-for="edge in layout.edges"
            :key="edge.id"
            :d="edge.path"
            fill="none"
            stroke="#8fa5ba"
            stroke-width="1.5"
            marker-end="url(#dependency-arrow)"
          /></svg
        ><button
          v-for="node in layout.nodes"
          :key="node.task.task_id"
          class="dependency-node"
          :class="{ active: selected === node.task.task_id }"
          :style="{ left: node.x + 'px', top: node.y + 'px' }"
          :aria-pressed="selected === node.task.task_id"
          @click="$emit('select', node.task.task_id)"
        >
          <div>
            <b>{{ roleNames[node.task.agent_type] || node.task.agent_type }}</b
            ><Status :value="node.task.status" />
          </div>
          <span>{{ node.task.key }}</span
          ><small>{{ node.task.tool }}</small>
        </button>
        <p v-if="!tasks.length" class="graph-empty">服务端尚未返回执行节点。</p>
      </div>
    </div>
    <footer>
      <span>{{
        dependenciesKnown === false ? '专业节点来自服务端返回' : '连线表示真实前置依赖'
      }}</span
      ><span>实际执行顺序以任务事件为准</span>
    </footer>
  </div>
</template>
<style scoped>
.dependency-flow {
  background: #142d4e;
  color: #fff;
  border-radius: 12px;
  overflow: hidden;
}
.dependency-flow header {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  padding: 22px 24px;
  border-bottom: 1px solid #ffffff14;
}
.dependency-flow header > div {
  display: grid;
  gap: 8px;
}
.dependency-flow b {
  font-size: 15px;
  font-weight: 600;
}
.dependency-flow header span {
  font-size: 13px;
  color: #c2d0df;
}
.graph-scroll {
  overflow: auto;
}
.graph-stage {
  position: relative;
}
.graph-stage svg {
  position: absolute;
  inset: 0;
  pointer-events: none;
}
.dependency-node {
  position: absolute;
  width: 216px;
  height: 96px;
  border: 1px solid #b4c4d5;
  background: #fff;
  border-radius: 9px;
  padding: 14px;
  text-align: left;
  color: #172033;
  cursor: pointer;
  transition:
    border-color 0.18s,
    box-shadow 0.18s;
}
.dependency-node:hover,
.dependency-node.active {
  border-color: #2bb6a6;
  box-shadow: 0 0 0 2px #2bb6a633;
}
.dependency-node > div {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
}
.dependency-node b {
  font-size: 14px;
  text-transform: capitalize;
}
.dependency-node > span,
.dependency-node small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dependency-node > span {
  font-size: 13px;
  color: #475569;
  margin-top: 9px;
}
.dependency-node small {
  font-size: 12px;
  color: #64748b;
  margin-top: 4px;
}
.dependency-node :deep(.status) {
  font-size: 11px;
  padding: 3px 5px;
}
.dependency-flow footer {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 14px 24px;
  background: #ffffff06;
  font-size: 12px;
  color: #c2d0df;
}
.graph-empty,
.graph-warning {
  font-size: 14px;
  padding: 16px;
  color: #e8d2a2;
}
.dependency-node:focus-visible {
  outline: 2px solid #2bb6a6;
  outline-offset: 3px;
}
@media (max-width: 600px) {
  .dependency-flow header {
    padding: 20px 16px;
    flex-direction: column;
    gap: 10px;
  }
  .dependency-flow footer {
    padding: 14px 16px;
    flex-direction: column;
    gap: 6px;
  }
}
</style>
