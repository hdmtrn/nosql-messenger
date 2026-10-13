import { ref } from 'vue'
import { api } from './api'
import { rememberUser } from './naming'

// The field searches and nothing else: what it finds is people, and what you do
// with them is add or message, depending on whether you are friends already.
// Shared by the rail and the phone's chat list.
export function usePeopleSearch(myId) {
  const query = ref('')
  const found = ref([])
  let timer = null

  function search() {
    clearTimeout(timer)
    timer = setTimeout(async () => {
      const q = query.value.trim()
      const hits = q ? await api.searchUsers(q).catch(() => []) : []
      for (const u of hits) rememberUser(u)
      found.value = hits.filter((u) => u.id !== myId())
    }, 200)
  }

  return { query, found, search }
}
