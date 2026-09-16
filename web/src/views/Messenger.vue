<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { api } from '../api'
import { createSocket } from '../socket'
import { avatarUrl, channelTitle, displayName, initials, rememberUser } from '../naming'
import { takePendingInvite } from '../pending'
import ChannelRail from '../components/ChannelRail.vue'
import SgButton from '../components/SgButton.vue'
import SgDialog from '../components/SgDialog.vue'
import SgInput from '../components/SgInput.vue'
import Conversation from './Conversation.vue'
import InfoPanel from './InfoPanel.vue'
import Profile from './Profile.vue'
import UserPanel from './UserPanel.vue'

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
// Username of the person whose page is open. It takes the same slot as the
// channel info, so opening one closes the other.
const person = ref('')

const conversation = ref(null)
const loadingOlder = ref(false)
const hasOlder = ref(true)

const dialog = ref(null)
const confirmLeave = ref(false)
const draftName = ref('')
const dialogError = ref('')

const active = computed(() => channels.value.find((c) => c.id === activeId.value) || null)
const activeTitle = computed(() => channelTitle(active.value, props.me.id))

// Our own messages are looked up by username like anyone else's; a rename in the
// profile has to reach them without a reload.
watch(() => [props.me.display_name, props.me.avatar_id], () => rememberUser(props.me), { immediate: true })

/* ---------- address bar ---------- */

// The open pane lives in the address bar, so a reload lands back on it. The
// prefix is /c/ and not /channels/ because GET /channels/{id} is an API route:
// the dev proxy would answer a reload with JSON instead of the page.
function paneFromUrl() {
  if (location.pathname === '/profile') showProfile.value = true
  const match = location.pathname.match(/^\/c\/([^/]+)$/)
  if (match) activeId.value = match[1]
}

// replaceState rather than pushState: switching chats should not pile up
// entries that the back button would then have to walk through.
watch([activeId, showProfile], () => {
  const path = showProfile.value ? '/profile' : activeId.value ? `/c/${activeId.value}` : '/'
  if (location.pathname !== path) history.replaceState(null, '', path)
})

/* ---------- channels ---------- */

async function loadChannels() {
  channels.value = await api.channels()
}

async function selectChannel(id) {
  if (pendingAction.value && pendingAction.value.channelId !== id) pendingAction.value = null
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

// A quote whose original is further back than the loaded page: older pages are
// loaded, as scrolling up would, until it turns up. Twenty pages is where it
// stops; jumping straight to a far message would need a page around it.
async function findMessage(id) {
  const channelId = activeId.value
  const loaded = () => messages.value.some((m) => m.id === id)
  for (let page = 0; page < 20 && !loaded() && hasOlder.value; page++) {
    while (loadingOlder.value) await new Promise((r) => setTimeout(r, 50))
    if (activeId.value !== channelId) return
    await loadOlder()
  }
  if (activeId.value === channelId && loaded()) conversation.value?.highlight(id)
}

/* ---------- sending ---------- */

async function deliver(entry) {
  entry.status = 'sending'
  try {
    const saved = entry.sourceId
      ? await api.forward(entry.sourceId, activeId.value, entry.client_msg_id)
      : await api.send({
        channel_id: activeId.value,
        text: entry.text,
        client_msg_id: entry.client_msg_id,
        reply_to: entry.reply_to,
      })
    Object.assign(entry, saved, { status: 'delivered' })
  } catch {
    entry.status = 'failed'
  }
}

// With a forward waiting, the typed text is a comment: it goes first and the
// forward after it, as in Telegram. The forward waits for the comment's answer:
// sent in parallel, the server could store them the other way round.
// With a reply waiting, the typed text is the reply itself.
async function send(text) {
  const action = pendingAction.value && pendingAction.value.channelId === activeId.value ? pendingAction.value : null
  if (action) pendingAction.value = null
  const forwarding = action && action.kind === 'forward'

  if (text.trim()) {
    messages.value = [...messages.value, {
      client_msg_id: crypto.randomUUID(),
      text,
      author: { id: props.me.id, username: props.me.username },
      created_at: new Date().toISOString(),
      status: 'sending',
      reply_to: action && action.kind === 'reply' ? action.message.id : undefined,
    }]
    const comment = deliver(messages.value[messages.value.length - 1])
    conversation.value?.toBottom()
    if (forwarding) await comment
  }
  if (forwarding) {
    queueForward(action.message)
    conversation.value?.toBottom()
  }
}

function discard(entry) {
  messages.value = messages.value.filter((m) => m !== entry)
}

/* ---------- forwarding ---------- */

// Only chats that already exist: a friend without a conversation yet has no
// channel id to forward into. The open chat stays in the list: forwarding an old
// message there brings it back to the bottom.
const forwardTargets = computed(() =>
  channels.value
    .map((c) => {
      const direct = c.kind === 'direct'
      const username = direct ? channelTitle(c, props.me.id) : ''
      const title = direct ? displayName(username) : c.name
      return { id: c.id, title, direct, initials: initials(title), avatar: avatarUrl(username) }
    })
)

// { kind: 'reply' | 'forward', message, channelId }: picked in the menu and
// waiting above the field of that chat until Send. One at a time, as in Telegram:
// picking the other replaces it. Leaving the chat drops it.
const pendingAction = ref(null)

function startReply(message) {
  pendingAction.value = { kind: 'reply', message, channelId: activeId.value }
}

async function pickForward(message, channelId) {
  pendingAction.value = { kind: 'forward', message, channelId }
  // Reloading the chat that is already open would only lose the scroll position.
  if (channelId !== activeId.value) await selectChannel(channelId)
}

// Goes through deliver() like any typed message, so it shows Sending and gets
// Retry and Discard. The snapshot is filled in ahead so the bubble already reads
// "Forwarded from"; the server's answer replaces it.
function queueForward(message) {
  messages.value = [...messages.value, {
    client_msg_id: crypto.randomUUID(),
    sourceId: message.id,
    text: message.text,
    author: { id: props.me.id, username: props.me.username },
    forwarded: message.forwarded || { author: message.author },
    created_at: new Date().toISOString(),
    status: 'sending',
  }]
  deliver(messages.value[messages.value.length - 1])
}

/* ---------- reply quotes ---------- */

// Originals of replies that are not in the loaded page, by id. null means the
// server did not return it: the quote says it is not available.
const originals = ref(new Map())
const asked = new Set()

// A reply stores only the id of its original, so the quote is filled in here:
// from the loaded messages when it is among them, otherwise fetched, all the
// missing ones in one request, as Telegram does with channels.getMessages.
async function resolveReplies() {
  const channelId = activeId.value
  const loaded = new Set(messages.value.map((m) => m.id))
  const missing = [...new Set(messages.value.map((m) => m.reply_to))]
    .filter((id) => id && !loaded.has(id) && !asked.has(id))
  if (!channelId || !missing.length) return
  missing.forEach((id) => asked.add(id))

  // The server takes at most 100 ids at a time.
  for (let i = 0; i < missing.length; i += 100) {
    const part = missing.slice(i, i + 100)
    let found = []
    try {
      found = await api.messagesByIds(channelId, part)
    } catch {
      // Asked again on the next change of the feed.
      part.forEach((id) => asked.delete(id))
      continue
    }
    const next = new Map(originals.value)
    for (const id of part) next.set(id, null)
    for (const m of found) next.set(m.id, m)
    originals.value = next
  }
}

watch(messages, resolveReplies)

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

function openPerson(username) {
  showInfo.value = false
  person.value = username
}

const relation = computed(() => {
  const u = person.value
  if (friends.value.some((f) => f.username === u)) return 'friend'
  if (requests.value.some((r) => r.from.username === u)) return 'incoming'
  if (sentTo.value.includes(u)) return 'sent'
  return 'none'
})

async function respond(id, action) {
  await api[action === 'accept' ? 'acceptFriendRequest' : 'declineFriendRequest'](id).catch(() => null)
  loadPeople()
}

onMounted(async () => {
  paneFromUrl()
  const invite = takePendingInvite()
  if (invite) {
    const joined = await api.followInvite(invite).catch(() => null)
    if (joined) activeId.value = joined.channel_id
  }

  await loadChannels()
  // An id from the address bar can be stale: the channel was left since, or the
  // URL was written by another account that used this tab before.
  if (activeId.value && !active.value) activeId.value = null
  if (activeId.value) selectChannel(activeId.value)
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
  <div style="position:relative;height:100vh;display:flex;flex-direction:column;background:var(--paper);
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
        @open-direct="openDirect"
        @add-friend="addFriend"
        @respond="respond"
        @profile="showProfile = true"
        @person="openPerson"
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
        :person="person"
        :info-open="showInfo"
        @send="send"
        @retry="deliver"
        @discard="discard"
        @load-older="loadOlder"
        @info="showInfo = !showInfo; person = ''"
        :forward-targets="forwardTargets"
        :pending="pendingAction && pendingAction.channelId === activeId ? pendingAction : null"
        :originals="originals"
        @reply="startReply"
        @find="findMessage"
        @forward="pickForward"
        @cancel-pending="pendingAction = null"
        @person="openPerson"
      />

      <p v-else class="sg-mono" style="margin:auto;color:var(--text-muted)">
        {{ channels.length ? 'Pick a channel or a conversation' : 'Create a channel or join one by code' }}
      </p>

      <!-- The side panels slide like the rail collapses. v-if alone would remove them
           in one frame; Transition keeps the node until the width has closed. -->
      <Transition name="side">
        <div v-if="showInfo && active" class="side">
          <InfoPanel
            :me="me"
            :channel="active"
            :title="activeTitle"
            @close="showInfo = false"
            @leave="showInfo = false; confirmLeave = true"
            @select="selectChannel"
            @person="openPerson"
          />
        </div>
      </Transition>

      <Transition name="side">
        <div v-if="person && !showProfile" class="side">
          <UserPanel
            :username="person"
            :relation="relation"
            @close="person = ''"
            @message="openDirect"
            @befriend="addFriend"
            @select="selectChannel"
          />
        </div>
      </Transition>

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

  </div>
</template>

<style scoped>
/* The wrapper animates its width and clips; the panel inside stays 340px wide,
   so its text does not rewrap on every frame. It is pinned to the right edge,
   so it slides in from there. */
.side {
  flex: 0 0 340px;
  display: flex;
  justify-content: flex-end;
  overflow: hidden;
}
.side-enter-active,
.side-leave-active {
  /* same timing as the rail */
  transition: flex-basis 0.18s ease, margin-left 0.18s ease, opacity 0.18s ease;
}
/* the negative margin cancels the row's 16px gap, which would otherwise jump */
.side-enter-from,
.side-leave-to {
  flex-basis: 0;
  margin-left: -16px;
  opacity: 0;
}
/* Below 1100px a 340px panel beside the feed would squeeze the feed under ~400px,
   so the panel lies over the feed instead. The paper strip on its left stands in
   for the 16px gap it has in the wide layout; there it only fades. */
@media (max-width: 1100px) {
  .side {
    position: absolute;
    top: 16px;
    right: 16px;
    bottom: 16px;
    z-index: 10;
    padding-left: 16px;
    background: var(--surface-page);
  }
}
</style>
