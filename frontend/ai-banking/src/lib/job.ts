import { ref, watch, onBeforeUnmount, computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { api, streamJob, request, type Schema } from '@/api/client'
import { useSession } from './store'
export function useJob() {
  const s = useSession(),
    connection = ref('等待任务'),
    controller = ref<AbortController>(),
    refreshing = ref(false)
  const job = useQuery({
    queryKey: computed(() => ['job', s.selectedJob]),
    enabled: computed(() => s.connected && !!s.selectedJob),
    queryFn: ({ signal }) => api.job(s.selectedJob, signal),
    refetchInterval: 5000,
  })
  const trace = useQuery({
    queryKey: computed(() => ['trace', s.selectedJob]),
    enabled: computed(() => s.connected && !!s.selectedJob),
    queryFn: ({ signal }) =>
      request<Schema<'OrchestrationTraceView'>>(`/api/v1/orchestrations/${s.selectedJob}/trace`, {
        signal,
      }),
    refetchInterval: 5000,
  })
  watch(
    () => [s.selectedJob, s.connected],
    () => {
      controller.value?.abort()
      if (!s.selectedJob || !s.connected) return
      const c = new AbortController()
      controller.value = c
      void streamJob(
        s.selectedJob,
        c.signal,
        () => {
          if (refreshing.value) return
          refreshing.value = true
          void Promise.all([job.refetch(), trace.refetch()]).finally(
            () => (refreshing.value = false),
          )
        },
        (v) => (connection.value = v),
      )
    },
    { immediate: true },
  )
  watch(
    () => job.data.value?.job.status,
    (status) => {
      if (status && ['succeeded', 'failed', 'cancelled'].includes(status)) {
        controller.value?.abort()
        connection.value = '任务已结束'
      }
    },
  )
  onBeforeUnmount(() => controller.value?.abort())
  return { job, trace, connection }
}
