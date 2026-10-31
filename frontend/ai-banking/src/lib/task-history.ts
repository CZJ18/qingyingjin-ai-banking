import { computed, toValue, type MaybeRefOrGetter } from 'vue'
import { useInfiniteQuery } from '@tanstack/vue-query'
import { agentApi } from '@/api/agent-client'
import { useSession } from './store'

// This is a read-only discovery path. Selecting history never claims or runs a task.
export function useTaskHistory(enabled: MaybeRefOrGetter<boolean> = true) {
  const session = useSession()
  const query = useInfiniteQuery({
    queryKey: computed(() => ['persisted-task-history', session.sessionEpoch, session.user?.id]),
    enabled: computed(() => session.connected && toValue(enabled)),
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) => agentApi.history(20, pageParam, signal),
    getNextPageParam: (last) => last.offset + last.items.length < last.total && last.items.length
      ? last.offset + last.items.length : undefined,
    retry: false,
  })
  const items = computed(() => {
    const seen = new Set<string>()
    return (query.data.value?.pages.flatMap((p) => p.items) || []).filter((item) => {
      if (seen.has(item.task_id)) return false
      seen.add(item.task_id)
      return true
    })
  })
  return { ...query, items, total: computed(() => query.data.value?.pages[0]?.total) }
}
