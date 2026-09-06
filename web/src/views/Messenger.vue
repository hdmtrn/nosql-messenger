<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api } from '../api'
import { createSocket } from '../socket'
import { channelTitle } from '../naming'
import ChannelRail from '../components/ChannelRail.vue'
import SgButton from '../components/SgButton.vue'
import SgDialog from '../components/SgDialog.vue'
import SgInput from '../components/SgInput.vue'
import Conversation from './Conversation.vue'
import InfoPanel from './InfoPanel.vue'
import Profile from './Profile.vue'

const props = defineProps({ me: { type: Object, required: true } })
const emit = defineEmits(['log-out', 'profile-changed'])

const channels = ref([])
const activeId = ref(null)
const messages = ref([])
const unread = ref({})
const friends = ref([])
const requests = ref([])
const sentTo = ref([])
const connection = ref('offline')
const showProfile = ref(false)
const showInfo = ref(false)

const conversation = ref(null)
const loadingOlder = ref(false)
const hasOlder = ref(true)

const dialog = ref(null)
const confirmLeave = ref(false)
const draftName = ref('')
const draftCode = ref('')
const dialogError = ref('')

const active = computed(() => channels.value.find((c) => c.id === activeId.value) || null)
const activeTitle = computed(() => channelTitle(active.value, props.me.id))

/* ---------- channels ---------- */

async function loadChannels() {
  channels.value = await api.channels()
  if (!activeId.value && channels.value.length) selectChannel(channels.value[0].id)
}

async function selectChannel(id) {
  showProfile.value = false
  activeId.value = id
  unread.value = { ...unread.value, [id]: 0 }
  hasOlder.value = true
  messages.value = (await api.messages({ channel_id: id })).reverse()
  conversation.value?.toBottom()
}

async function runDialog(action) {
  dialogError.value = ''
  try {
    const id = await action()
    dialog.value = null
    await loadChannels()
    selectChannel(id)
  } catch (e) {
    dialogError.value = `${e.message} (${e.status})`
  }
}

const createChannel = () => runDialog(async () => {
  const ch = await api.createChannel(draftName.value)
  draftName.value = ''
  return ch.id
})

const joinChannel = () => runDialog(async () => {
  const { channel_id } = await api.followInvite(draftCode.value.trim())
  draftCode.value = ''
  return channel_id
})

async function openDirect(username) {
  const ch = await api.openDirect(username).catch(() => null)
  if (!ch) return
  await loadChannels()
  selectChannel(ch.id)
}

async function leaveChannel() {
  const id = activeId.value
  await api.leaveChannel(id).catch(() => null)

  channels.value = channels.value.filter((c) => c.id !== id)
  messages.value = []
  activeId.value = null
  if (channels.value.length) selectChannel(channels.value[0].id)
}

/* ---------- history ---------- */

async function loadOlder() {
  if (loadingOlder.value || !hasOlder.value || !messages.value.length) return
  loadingOlder.value = true

  const older = (await api.messages({
    channel_id: activeId.value,
    before: messages.value[0].id,
  })).reverse()

  if (!older.length) hasOlder.value = false
  else {
    const keep = conversation.value.distanceFromBottom()
    messages.value = [...older, ...messages.value]
    conversation.value.keepPosition(keep)
  }
  loadingOlder.value = false
}

/* ---------- sending ---------- */

async function deliver(entry) {
  entry.status = 'sending'
  try {
    const saved = await api.send({
      channel_id: activeId.value,
      text: entry.text,
      client_msg_id: entry.client_msg_id,
    })
    Object.assign(entry, saved, { status: 'delivered' })
  } catch {
    entry.status = 'failed'
  }
}

function send(text) {
  messages.value = [...messages.value, {
    client_msg_id: crypto.randomUUID(),
    text,
    author: { id: props.me.id, username: props.me.username },
    created_at: new Date().toISOString(),
    status: 'sending',
  }]
  conversation.value?.toBottom()
  deliver(messages.value[messages.value.length - 1])
}

function discard(entry) {
  messages.value = messages.value.filter((m) => m !== entry)
}

/* ---------- socket ---------- */

let socket = null

function receive(msg) {
  if (msg.channel_id !== activeId.value) {
    unread.value = { ...unread.value, [msg.channel_id]: (unread.value[msg.channel_id] || 0) + 1 }
    return
  }
  const known = messages.value.some(
    (m) => m.id === msg.id || (msg.client_msg_id && m.client_msg_id === msg.client_msg_id)
  )
  if (known) return
  messages.value = [...messages.value, { ...msg, status: 'delivered' }]
  conversation.value?.toBottom()
}

// A socket that was down missed messages; the REST history is what fills the gap.
async function backfill() {
  const latest = await api.messages({ channel_id: activeId.value })
  const have = new Set(messages.value.map((m) => m.id))
  const missing = latest.reverse().filter((m) => !have.has(m.id))
  if (missing.length) {
    messages.value = [...messages.value, ...missing]
    conversation.value?.toBottom()
  }
}

// Friends and their conversations are the same list in the rail, so both are
// loaded together and refreshed whenever either could have changed.
async function loadPeople() {
  const [list, pending] = await Promise.all([
    api.friends().catch(() => []),
    api.friendRequests().catch(() => ({ incoming: [] })),
  ])
  friends.value = list
  requests.value = pending.incoming
  sentTo.value = pending.outgoing.map((r) => r.to.username)
}

async function addFriend(username) {
  await api.sendFriendRequest(username).catch(() => null)
  loadPeople()
}

async function respond(id, action) {
  await api[action === 'accept' ? 'acceptFriendRequest' : 'declineFriendRequest'](id).catch(() => null)
  loadPeople()
}

onMounted(async () => {
  await loadChannels()
  loadPeople()
  socket = createSocket({
    onMessage: receive,
    onStateChange: (state) => {
      const wasOffline = connection.value === 'offline'
      connection.value = state
      if (state === 'online' && wasOffline && activeId.value) backfill()
    },
  })
})

onUnmounted(() => socket && socket.close())
</script>

<template>
  <div style="height:100vh;display:flex;flex-direction:column;background:var(--paper);
              padding:16px;box-sizing:border-box">
    <div style="flex:1;min-height:0;display:flex;gap:16px">

      <ChannelRail
        :me="me"
        :channels="channels"
        :active-id="activeId"
        :unread="unread"
        :friends="friends"
        :requests="requests"
        :sent-to="sentTo"
        :profile-open="showProfile"
        @select="selectChannel"
        @create="dialog = 'create'"
        @join="dialog = 'join'"
        @open-direct="openDirect"
        @add-friend="addFriend"
        @respond="respond"
        @profile="showProfile = true"
      />

      <Profile
        v-if="showProfile"
        :me="me"
        @saved="emit('profile-changed')"
        @log-out="emit('log-out')"
      />

      <Conversation
        v-else-if="active"
        ref="conversation"
        :me="me"
        :channel="active"
        :title="activeTitle"
        :messages="messages"
        @send="send"
        @retry="deliver"
        @discard="discard"
        @load-older="loadOlder"
        @info="showInfo = !showInfo"
      />

      <p v-else class="sg-mono" style="margin:auto;color:var(--text-muted)">
        Create a channel or join one by code
      </p>

      <InfoPanel
        v-if="showInfo && active"
        :me="me"
        :channel="active"
        :title="activeTitle"
        @close="showInfo = false"
        @leave="showInfo = false; confirmLeave = true"
        @select="selectChannel"
      />

    </div>

    <SgDialog v-if="confirmLeave" :title="`Leave #${active?.name}?`" @close="confirmLeave = false">
      <p style="margin:0;font:var(--text-body);color:var(--text-muted)">
        You stop receiving messages from this channel. Rejoin any time with the invite code.
      </p>
      <div style="display:flex;gap:12px">
        <SgButton variant="danger" @click="confirmLeave = false; leaveChannel()">Leave</SgButton>
        <SgButton variant="outline" @click="confirmLeave = false">Cancel</SgButton>
      </div>
    </SgDialog>

    <SgDialog v-if="dialog === 'create'" title="New channel" @close="dialog = null">
      <SgInput v-model="draftName" label="Name" hint="1-64 characters" :error="dialogError" />
      <div style="display:flex;gap:12px">
        <SgButton variant="primary" @click="createChannel">Create</SgButton>
        <SgButton variant="outline" @click="dialog = null">Cancel</SgButton>
      </div>
    </SgDialog>

    <SgDialog v-if="dialog === 'join'" title="Join by code" @close="dialog = null">
      <SgInput v-model="draftCode" label="Invite code" mono :error="dialogError" />
      <div style="display:flex;gap:12px">
        <SgButton variant="primary" @click="joinChannel">Join</SgButton>
        <SgButton variant="outline" @click="dialog = null">Cancel</SgButton>
      </div>
    </SgDialog>
  </div>
</template>
