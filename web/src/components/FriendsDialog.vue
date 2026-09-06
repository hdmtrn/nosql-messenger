<script setup>
import { onMounted, ref } from 'vue'
import { api } from '../api'
import SgAvatar from './SgAvatar.vue'
import SgButton from './SgButton.vue'
import SgInput from './SgInput.vue'
import SgDialog from './SgDialog.vue'

const emit = defineEmits(['close', 'changed'])

const incoming = ref([])
const outgoing = ref([])
const friends = ref([])
const username = ref('')
const error = ref('')
const busy = ref(false)

const mono = {
  font: 'var(--text-machine)',
  textTransform: 'uppercase',
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

const send = () => run(async () => {
  await api.sendFriendRequest(username.value.trim())
  username.value = ''
})

onMounted(load)
</script>

<template>
  <SgDialog title="Friends" @close="emit('close')">
    <div style="display:flex;gap:12px;align-items:flex-end">
      <SgInput v-model="username" label="Add by username" :error="error"
               style="flex:1" @keyup.enter="send" />
      <SgButton variant="primary" :disabled="busy || !username.trim()" @click="send">Send</SgButton>
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
        <span style="font:var(--text-body)">{{ f.username }}</span>
      </div>
    </section>

    <SgButton variant="outline" @click="emit('close')">Close</SgButton>
  </SgDialog>
</template>
