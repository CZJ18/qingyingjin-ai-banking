import { computed, onScopeDispose, ref, toValue, watch, type MaybeRefOrGetter } from 'vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import {
  agentApi,
  streamAgentTask,
  type AgentResume,
  type AgentTask,
  type AgentTaskEvent,
} from '@/api/agent-client'
import { sessionVersion } from '@/api/client'

// The P5 owner-engine contract remains separate from useJob's legacy Go history.
export function useAgentJob(taskId: MaybeRefOrGetter<string>, enabled: MaybeRefOrGetter<boolean>) {
  const queryClient = useQueryClient(),
    connection = ref('等待任务')
  let controller: AbortController | undefined
  const active = computed(() => toValue(enabled) && /^lg-/.test(toValue(taskId)))
  const epoch = computed(() => {
    toValue(enabled)
    return sessionVersion()
  })
  const taskKey = computed(() => ['langgraph-task', epoch.value, toValue(taskId)])
  const eventKey = computed(() => ['langgraph-events', epoch.value, toValue(taskId)])
  const task = useQuery({
    queryKey: taskKey,
    enabled: active,
    queryFn: ({ signal }) => agentApi.task(toValue(taskId), signal),
    refetchInterval: 5000,
    retry: false,
  })
  const events = useQuery({
    queryKey: eventKey,
    enabled: active,
    queryFn: ({ signal }) => agentApi.events(toValue(taskId), 0, signal),
    refetchInterval: 5000,
    retry: false,
  })
  function stop() {
    controller?.abort()
    controller = undefined
  }
  function start() {
    stop()
    if (!active.value) {
      connection.value = '等待任务'
      return
    }
    const ownController = new AbortController(),
      ownTaskKey = taskKey.value,
      ownEventKey = eventKey.value
    controller = ownController
    void streamAgentTask(
      toValue(taskId),
      ownController.signal,
      (snapshot) => queryClient.setQueryData(ownTaskKey, snapshot),
      (event) => {
        queryClient.setQueryData<AgentTaskEvent[]>(ownEventKey, (previous) => {
          const merged = [...(previous ?? []).filter((v) => v.seq !== event.seq), event]
          return merged.sort((a, b) => a.seq - b.seq).slice(-1000)
        })
        void queryClient.invalidateQueries({ queryKey: ownTaskKey })
      },
      (state) => {
        if (!ownController.signal.aborted) connection.value = state
      },
    ).catch(() => {
      if (!ownController.signal.aborted) connection.value = '任务事件连接失败'
    })
  }
  watch([() => toValue(taskId), () => toValue(enabled)], start, { immediate: true })
  watch(
    () => task.data.value?.status,
    (status) => {
      if (
        status &&
        ['interrupted', 'waiting_confirmation', 'succeeded', 'failed', 'cancelled'].includes(status)
      ) {
        stop()
        connection.value =
          status === 'waiting_confirmation' ? '等待用户确认准备方案' : '任务已暂停或结束'
      } else if (status && active.value && !controller) start()
    },
  )
  onScopeDispose(stop)
  async function update(operation: () => Promise<AgentTask>) {
    const ownEpoch = sessionVersion(),
      ownId = toValue(taskId),
      ownKey = taskKey.value
    const value = await operation()
    if (ownEpoch === sessionVersion() && active.value && ownId === toValue(taskId)) {
      queryClient.setQueryData(ownKey, value)
      await events.refetch()
    }
    return value
  }
  return {
    task,
    events,
    connection,
    run: (version: number) => update(() => agentApi.run(toValue(taskId), version)),
    resume: (body: AgentResume) => update(() => agentApi.resume(toValue(taskId), body)),
    cancel: (version: number) => update(() => agentApi.cancel(toValue(taskId), version)),
  }
}
