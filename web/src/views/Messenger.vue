<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { api } from '../api'
import { createSocket } from '../socket'
import ChannelRow from '../components/ChannelRow.vue'
import SgAvatar from '../components/SgAvatar.vue'
import PaneHeader from '../components/PaneHeader.vue'
import MessageBubble from '../components/MessageBubble.vue'
import MessageComposer from '../components/MessageComposer.vue'
import SgButton from '../components/SgButton.vue'
import SgInput from '../components/SgInput.vue'
import SgDialog from '../components/SgDialog.vue'
import FriendsDialog from '../components/FriendsDialog.vue'
import Profile from './Profile.vue'

const props = defineProps({ me: { type: Object, required: true } })
const emit = defineEmits(['log-out', 'profile-changed'])

const channels = ref([])
const activeId = ref(null)
const messages = ref([])
const unread = ref({})
const connection = ref('offline')
const feed = ref(null)

const loadingOlder = ref(false)
const hasOlder = ref(true)
const copied = ref(false)

const dialog = ref(null)
const confirmLeave = ref(false)
const showProfile = ref(false)
const incomingRequests = ref(0)
const draftName = ref('')
const draftCode = ref('')
const dialogError = ref('')

const footerStyle = {
  display: 'flex',
  alignItems: 'center',
  gap: '12px',
  width: '100%',
  marginTop: '16px',
  padding: '8px 6px',
  border: 'none',
  borderRadius: 'var(--radius-pill)',
  background: 'transparent',
  cursor: 'pointer',
}

const friendsRowStyle = {
  display: 'flex',
  alignItems: 'center',
  gap: '12px',
  width: '100%',
  height: '38px',
  padding: '0 14px',
  cursor: 'pointer',
  border: 'none',
  borderRadius: 'var(--radius-pill)',
  background: 'transparent',
  color: '#fff',
}

const active = computed(() => channels.value.find((c) => c.id === activeId.value) || null)

function initials(name) {
  return (name || '').slice(0, 2)
}

function clock(iso) {
  return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

async function scrollToBottom() {
  await nextTick()
  if (feed.value) feed.value.scrollTop = feed.value.scrollHeight
}

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
  scrollToBottom()
}

async function createChannel() {
  dialogError.value = ''
  try {
    const ch = await api.createChannel(draftName.value)
    channels.value = [...channels.value, ch]
    dialog.value = null
    draftName.value = ''
    selectChannel(ch.id)
  } catch (e) {
    dialogError.value = `${e.message} (${e.status})`
  }
}

async function joinChannel() {
  dialogError.value = ''
  try {
    const { channel_id } = await api.joinChannel(draftCode.value.trim())
    await loadChannels()
    dialog.value = null
    draftCode.value = ''
    selectChannel(channel_id)
  } catch (e) {
    dialogError.value = `${e.message} (${e.status})`
  }
}

async function leaveChannel() {
  const id = activeId.value
  await api.leaveChannel(id).catch(() => null)

  channels.value = channels.value.filter((c) => c.id !== id)
  messages.value = []
  activeId.value = null
  if (channels.value.length) selectChannel(channels.value[0].id)
}

function copyCode() {
  if (!active.value) return
  navigator.clipboard.writeText(active.value.invite_code)
  copied.value = true
  setTimeout(() => (copied.value = false), 1500)
}

/* ---------- history ---------- */

async function loadOlder() {
  if (loadingOlder.value || !hasOlder.value || !messages.value.length) return
  loadingOlder.value = true
  const before = messages.value[0].id
  const older = (await api.messages({ channel_id: activeId.value, before })).reverse()
  if (!older.length) hasOlder.value = false
  else {
    const box = feed.value
    const keep = box.scrollHeight - box.scrollTop
    messages.value = [...older, ...messages.value]
    await nextTick()
    box.scrollTop = box.scrollHeight - keep
  }
  loadingOlder.value = false
}

function onScroll() {
  if (feed.value && feed.value.scrollTop < 80) loadOlder()
}

/* ---------- sending ---------- */

function newClientId() {
  return crypto.randomUUID()
}

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
  const entry = {
    client_msg_id: newClientId(),
    text,
    author: { id: props.me.id, username: props.me.username },
    created_at: new Date().toISOString(),
    status: 'sending',
  }
  messages.value = [...messages.value, entry]
  scrollToBottom()
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
  scrollToBottom()
}

async function loadRequestCount() {
  const r = await api.friendRequests().catch(() => null)
  if (r) incomingRequests.value = r.incoming.length
}

onMounted(async () => {
  await loadChannels()
  loadRequestCount()
  socket = createSocket({
    onMessage: receive,
    onStateChange: (s) => {
      const wasOffline = connection.value === 'offline'
      connection.value = s
      if (s === 'online' && wasOffline && activeId.value) backfill()
    },
  })
})

onUnmounted(() => socket && socket.close())

async function backfill() {
  const latest = await api.messages({ channel_id: activeId.value })
  const have = new Set(messages.value.map((m) => m.id))
  const missing = latest.reverse().filter((m) => !have.has(m.id))
  if (missing.length) {
    messages.value = [...messages.value, ...missing]
    scrollToBottom()
  }
}

watch(activeId, () => (copied.value = false))
</script>

<template>
  <div style="height:100vh;display:flex;flex-direction:column;background:var(--paper);
              padding:16px;box-sizing:border-box">
    <div style="flex:1;min-height:0;display:flex;gap:16px">
      <nav style="flex:0 0 286px;background:var(--surface-accent);border-radius:var(--radius-panel);
                  padding:20px 16px;display:flex;flex-direction:column">
        <div style="display:flex;gap:8px;padding:0 8px 16px">
          <SgButton variant="outline" size="sm" on-blue @click="dialog = 'create'">+ New</SgButton>
          <SgButton variant="outline" size="sm" on-blue @click="dialog = 'join'">Join by code</SgButton>
        </div>

        <button
          type="button"
          :style="friendsRowStyle"
          @click="dialog = 'friends'"
        >
          <span style="flex:1;min-width:0;font:var(--text-body);text-align:left">Friends</span>
          <span
            v-if="incomingRequests"
            style="font:var(--text-machine);text-transform:uppercase;letter-spacing:var(--mono-tracking);
                   background:#fff;color:var(--blue);border-radius:var(--radius-pill);padding:2px 7px"
          >{{ incomingRequests }}</span>
        </button>

        <div style="height:1px;background:rgba(255,255,255,0.18);margin:12px 14px"></div>

        <div style="flex:1;min-height:0;overflow-y:auto;display:flex;flex-direction:column;gap:2px">
          <ChannelRow
            v-for="c in channels"
            :key="c.id"
            :name="c.name"
            :active="c.id === activeId"
            :unread="unread[c.id] || 0"
            @click="selectChannel(c.id)"
          />
          <p v-if="!channels.length" class="sg-mono"
             style="color:var(--text-on-blue-muted);padding:8px 14px">No channels yet</p>
        </div>

        <button
          type="button"
          :style="footerStyle"
          @click="showProfile = true"
        >
          <SgAvatar :initials="initials(me.display_name)" :size="32" tone="onBlue" />
          <span style="flex:1;min-width:0;text-align:left;font:600 13px/1.2 var(--font-ui);
                       color:#fff;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">
            {{ me.display_name }}
          </span>
        </button>
      </nav>

      <Profile
        v-if="showProfile"
        :me="me"
        @saved="emit('profile-changed')"
        @log-out="emit('log-out')"
      />

      <section v-else style="flex:1;min-width:0;background:var(--surface-panel);
                      border-radius:var(--radius-panel);display:flex;flex-direction:column;overflow:hidden">
        <template v-if="active">
          <PaneHeader
            :title="active.name"
            :code="active.invite_code ? active.invite_code.slice(0, 10) + '…' : ''"
            :copied="copied"
            @copy="copyCode"
          >
            <template #actions>
              <SgButton variant="outline" size="sm" @click="confirmLeave = true">Leave</SgButton>
            </template>
          </PaneHeader>

          <div ref="feed" class="feed" @scroll="onScroll">
            <p v-if="!messages.length" class="sg-mono"
               style="margin:auto;color:var(--text-muted)">No messages yet</p>

            <MessageBubble
              v-for="m in messages"
              :key="m.id || m.client_msg_id"
              :own="m.author.id === me.id"
              :status="m.status || 'delivered'"
              :author="m.author.username"
              :initials="initials(m.author.username)"
              :time="m.status && m.status !== 'delivered' ? '' : clock(m.created_at)"
              @retry="deliver(m)"
              @discard="discard(m)"
            >{{ m.text }}</MessageBubble>
          </div>

          <MessageComposer :placeholder="`Message #${active.name}…`" @send="send" />
        </template>


        <p v-else class="sg-mono" style="margin:auto;color:var(--text-muted)">
          Create a channel or join one by code
        </p>
      </section>
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

    <FriendsDialog
      v-if="dialog === 'friends'"
      @close="dialog = null"
      @changed="incomingRequests = $event"
    />

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

<style scoped>
.feed {
  flex: 1;
  overflow-y: auto;
  padding: 22px 28px;
  display: flex;
  flex-direction: column;
  gap: 18px;
}
/* short conversations hug the bottom; long ones still scroll from the top */
.feed > :first-child { margin-top: auto; }
</style>
