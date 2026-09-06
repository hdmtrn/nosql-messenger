<script setup>
import { onMounted, ref } from 'vue'
import { api } from '../api'
import SgAvatar from './SgAvatar.vue'
import SgButton from './SgButton.vue'
import SgInput from './SgInput.vue'
import SgDialog from './SgDialog.vue'

const emit = defineEmits(['close', 'changed', 'message'])

const incoming = ref([])
const outgoing = ref([])
const friends = ref([])
const query = ref('')
const found = ref([])
let searchTimer = null
const error = ref('')
const busy = ref(false)

const mono = {
  font: 'var(--text-machine)',
  textTransform: 'uppercase',
  letterSpacing: 'var(--mono-tracking)',
  color: 'var(--text-muted)',
}

// A handle is data, not a label: it must read exactly as it is stored.
const handle = {
  font: 'var(--text-machine-11)',
  letterSpacing: 'var(--mono-tracking)',
  color: 'var(--text-muted)',
}

function initials(name) {
  return (name || '').slice(0, 2)
}

async function load() {
  const [requests, list] = await Promise.all([api.friendRequests(), api.friends()])
  incoming.value = requests.incoming
  outgoing.value = requests.outgoing
  friends.value = list
  emit('changed', requests.incoming.length)
}

async function run(action) {
  error.value = ''
  busy.value = true
  try {
    await action()
    await load()
  } catch (e) {
    error.value = `${e.message} (${e.status})`
  } finally {
    busy.value = false
  }
}

function search() {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(async () => {
    const q = query.value.trim()
    found.value = q ? await api.searchUsers(q).catch(() => []) : []
  }, 200)
}

const add = (name) => run(async () => {
  await api.sendFriendRequest(name)
  query.value = ''
  found.value = []
})

onMounted(load)
</script>

<template>
  <SgDialog title="Friends" @close="emit('close')">
    <div>
      <SgInput v-model="query" label="Find people" hint="Name or @handle"
               :error="error" @update:model-value="search" />
      <div v-for="u in found" :key="u.id"
           style="display:flex;align-items:center;gap:12px;padding:8px 0">
        <SgAvatar :initials="initials(u.display_name)" :size="32" />
        <span style="flex:1;min-width:0">
          <span style="font:var(--text-body)">{{ u.display_name }}</span>
          <span :style="handle" style="margin-left:8px">@{{ u.username }}</span>
        </span>
        <SgButton variant="primary" size="sm" :disabled="busy" @click="add(u.username)">Add</SgButton>
      </div>
    </div>

    <section v-if="incoming.length">
      <p :style="mono" style="margin:0 0 8px">Incoming</p>
      <div v-for="r in incoming" :key="r.id"
           style="display:flex;align-items:center;gap:12px;padding:6px 0">
        <SgAvatar :initials="initials(r.from.username)" :size="32" />
        <span style="flex:1;min-width:0;font:var(--text-body)">{{ r.from.username }}</span>
        <SgButton variant="primary" size="sm" :disabled="busy"
                  @click="run(() => api.acceptFriendRequest(r.id))">Accept</SgButton>
        <SgButton variant="outline" size="sm" :disabled="busy"
                  @click="run(() => api.declineFriendRequest(r.id))">Decline</SgButton>
      </div>
    </section>

    <section v-if="outgoing.length">
      <p :style="mono" style="margin:0 0 8px">Sent</p>
      <div v-for="r in outgoing" :key="r.id"
           style="display:flex;align-items:center;gap:12px;padding:6px 0">
        <SgAvatar :initials="initials(r.to.username)" :size="32" />
        <span style="flex:1;min-width:0;font:var(--text-body)">{{ r.to.username }}</span>
        <span :style="mono">Pending</span>
      </div>
    </section>

    <section>
      <p :style="mono" style="margin:0 0 8px">Friends · {{ friends.length }}</p>
      <p v-if="!friends.length" :style="mono" style="margin:0">No friends yet</p>
      <div v-for="f in friends" :key="f.id"
           style="display:flex;align-items:center;gap:12px;padding:6px 0">
        <SgAvatar :initials="initials(f.username)" :size="32" />
        <span style="flex:1;min-width:0;font:var(--text-body)">{{ f.username }}</span>
        <SgButton variant="primary" size="sm" @click="emit('message', f.username)">Message</SgButton>
      </div>
    </section>

    <SgButton variant="outline" @click="emit('close')">Close</SgButton>
  </SgDialog>
</template>
