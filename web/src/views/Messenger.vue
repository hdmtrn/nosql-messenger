<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { api, patiently, waitText } from '../api'
import { createSocket } from '../socket'
import { avatarUrl, channelAvatarUrl, channelTitle, displayName, initials, rememberUser } from '../naming'
import { takePendingInvite } from '../pending'
import ChannelRail from '../components/ChannelRail.vue'
import SgButton from '../components/SgButton.vue'
import SgDialog from '../components/SgDialog.vue'
import SgInput from '../components/SgInput.vue'
import Conversation from './Conversation.vue'
import InfoPanel from './InfoPanel.vue'
import Profile from './Profile.vue'
import RequestsPanel from './RequestsPanel.vue'
import UserPanel from './UserPanel.vue'

const props = defineProps({ me: { type: Object, required: true } })
const emit = defineEmits(['log-out', 'profile-changed', 'session-ended'])

const channels = ref([])
const activeId = ref(null)
const messages = ref([])
// Every message has a number in its channel. What is unread is the newest number
// minus how far this person has read, both kept on the server, so a jump in the
// numbers counts the messages that never reached this tab too.
const lastSeq = ref({})
const readSeq = ref({})
const unread = computed(() => Object.fromEntries(
  Object.keys(lastSeq.value).map((id) => [id, Math.max(0, lastSeq.value[id] - (readSeq.value[id] || 0))])
))
const friends = ref([])
const requests = ref([])
const sentTo = ref([])
const connection = ref('offline')
const showProfile = ref(false)
const showInfo = ref(false)
// Username of the person whose page is open. It takes the same slot as the
// channel info and the friend requests, so opening one closes the others.
const person = ref('')
const showRequests = ref(false)
// When a friend request refused for coming too often may go again, as read.
const tooMany = ref('')
// False while the panes from the address bar are being put back after a reload.
// The chat's info panel waits for the channel list, so it mounts after the page
// does and Transition would take that for an opening; with this off it just
// stands there, as it did before the reload.
const panesRestored = ref(false)

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

// The open pane lives in the address bar, so a reload lands back on it: the chat
// as /c/{id}, and a side panel after it — /info for the chat's own panel,
// /u/{username} for a person's page and /requests for the friend requests, both
// of which can also stand without a chat. The
// prefix is /c/ and not /channels/ because GET /channels/{id} is an API route:
// the dev proxy would answer a reload with JSON instead of the page. /u/ does
// not clash with /users, since the proxy matches whole prefixes.
function paneFromUrl() {
  if (location.pathname === '/profile') {
    showProfile.value = true
    return
  }
  const match = location.pathname.match(/^(?:\/c\/([^/]+))?(?:\/(info)|\/(requests)|\/u\/([^/]+))?$/)
  if (!match) return
  const [, chat, info, requests, username] = match
  if (chat) activeId.value = chat
  if (info) showInfo.value = true
  if (requests) showRequests.value = true
  if (username) person.value = decodeURIComponent(username)
}

function paneToUrl() {
  if (showProfile.value) return '/profile'
  const chat = activeId.value ? `/c/${activeId.value}` : ''
  // The info panel shows only with its chat; a person's page shows either way.
  if (person.value) return `${chat}/u/${encodeURIComponent(person.value)}`
  if (showRequests.value) return `${chat}/requests`
  if (showInfo.value && chat) return `${chat}/info`
  return chat || '/'
}

// replaceState rather than pushState: switching chats should not pile up
// entries that the back button would then have to walk through.
watch([activeId, showProfile, showInfo, person, showRequests], () => {
  const path = paneToUrl()
  if (location.pathname !== path) history.replaceState(null, '', path)
})

/* ---------- channels ---------- */

async function loadChannels() {
  channels.value = await api.channels()
  // Max, not overwrite: a message may have arrived while the list was on its way.
  const last = { ...lastSeq.value }
  const read = { ...readSeq.value }
  for (const c of channels.value) {
    last[c.id] = Math.max(last[c.id] || 0, c.last_seq || 0)
    read[c.id] = Math.max(read[c.id] || 0, c.read_seq || 0)
  }
  lastSeq.value = last
  readSeq.value = read
}

async function selectChannel(id) {
  if (pendingAction.value && pendingAction.value.channelId !== id) pendingAction.value = null
  showProfile.value = false
  activeId.value = id
  hasOlder.value = true
  const page = (await api.messages({ channel_id: id })).reverse()
  // Clicked elsewhere while this was loading: the answer belongs to another chat.
  if (activeId.value !== id) return
  messages.value = page
  conversation.value?.toBottom()
  markRead()
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

  const channelId = activeId.value
  const oldest = messages.value.find((m) => m.seq)?.seq
  if (!oldest) {
    loadingOlder.value = false
    return
  }
  const older = (await api.messages({ channel_id: channelId, before_seq: oldest })).reverse()
  if (activeId.value !== channelId) {
    loadingOlder.value = false
    return
  }

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
    // The chat it was written in, not the one open now. A repeat carries the
    // same client_msg_id, so it cannot store the message twice.
    const channelId = entry.channel_id
    const saved = await patiently(() => entry.sourceId
      ? api.forward(entry.sourceId, channelId, entry.client_msg_id)
      : api.send({
        channel_id: channelId,
        text: entry.text,
        client_msg_id: entry.client_msg_id,
        reply_to: entry.reply_to,
        attachments: (entry.attachments || []).map((a) => a.id),
      }))
    Object.assign(entry, saved, { status: 'delivered' })
  } catch {
    entry.status = 'failed'
  }
}

// The server takes at most this many pictures per message; more go as several
// messages in a row, as Telegram splits albums.
const PICTURES_PER_MESSAGE = 10

// With a forward waiting, the typed text is a comment: it goes first and the
// forward after it, as in Telegram. With a reply waiting, the typed text is the
// reply itself. Pictures beyond one message go as several, the text with the last
// of them, as Telegram puts the caption under the last album.
// Each message waits for the one before it: sent in parallel, the server could
// store them the other way round.
async function send(text, attachments = []) {
  rearmTyping()
  // Every part goes to the chat it was written in, even if another is opened
  // while the earlier parts are still going out.
  const channelId = activeId.value
  const action = pendingAction.value && pendingAction.value.channelId === activeId.value ? pendingAction.value : null
  if (action) pendingAction.value = null
  const forwarding = action && action.kind === 'forward'

  const groups = []
  for (let i = 0; i < attachments.length; i += PICTURES_PER_MESSAGE) {
    groups.push(attachments.slice(i, i + PICTURES_PER_MESSAGE))
  }
  if (text.trim() && !groups.length) groups.push([])

  const entries = groups.map((group, i) => ({
    client_msg_id: crypto.randomUUID(),
    channel_id: channelId,
    text: i === groups.length - 1 ? text : '',
    attachments: group,
    author: { id: props.me.id, username: props.me.username },
    created_at: new Date().toISOString(),
    status: 'sending',
    reply_to: action && action.kind === 'reply' ? action.message.id : undefined,
  }))
  if (entries.length) {
    messages.value = [...messages.value, ...entries]
    conversation.value?.toBottom()
  }
  // The entries are read back from the list: Vue wraps them there, and a change
  // made through the plain objects would not reach the screen.
  const queued = messages.value.slice(messages.value.length - entries.length)
  for (const entry of queued) await deliver(entry)

  if (forwarding) {
    queueForward(action.message, channelId)
    if (activeId.value === channelId) conversation.value?.toBottom()
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
      const avatar = direct ? avatarUrl(username) : channelAvatarUrl(c)
      return { id: c.id, title, direct, initials: initials(title), avatar }
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
function queueForward(message, channelId) {
  const entry = {
    client_msg_id: crypto.randomUUID(),
    channel_id: channelId,
    sourceId: message.id,
    text: message.text,
    attachments: message.attachments,
    author: { id: props.me.id, username: props.me.username },
    forwarded: message.forwarded || { author: message.author },
    created_at: new Date().toISOString(),
    status: 'sending',
  }
  // Another chat is open by now: the forward still goes, just not on this screen.
  if (activeId.value !== channelId) return deliver(entry)
  messages.value = [...messages.value, entry]
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

// The server can route this socket to a channel the list has never shown: a
// direct conversation somebody else opened, or a join made in another tab.
// Its messages already arrive, so the list catches up now rather than on
// reload. One request at a time — a burst in a new channel would otherwise
// fetch the same list once per message.
let catchingUp = null
function catchUpChannels() {
  catchingUp ??= loadChannels().finally(() => { catchingUp = null })
}

function receive(msg) {
  // The socket can miss nothing from here on, or the server's bus came back:
  // fetch what came in meanwhile, and the channels joined meanwhile.
  if (msg.type === 'ready' || msg.type === 'resync') {
    catchUpChannels()
    catchUp()
    return
  }
  // Typing shares the channel's stream with messages and is told apart by its
  // type; read as a message, it would become a bubble with no author.
  if (msg.type === 'typing') {
    noteTyping(msg)
    return
  }
  if (msg.type === 'presence') {
    if (msg.user_id !== props.me.id) notePresence(msg)
    return
  }
  // Somebody added us, or answered a request of ours. The event says only that
  // the two lists changed, so they are fetched rather than patched here.
  if (msg.type === 'friends') {
    loadPeople()
    return
  }
  // A changed name or picture: messages carry the username only, so without
  // this the new one would appear on the next reload.
  if (msg.type === 'profile') {
    rememberUser(msg.user)
    return
  }
  // The message is what the typing was for.
  forgetTyping(msg.channel_id, msg.author.id)
  if (!channels.value.some((c) => c.id === msg.channel_id)) catchUpChannels()
  const ch = msg.channel_id
  lastSeq.value = { ...lastSeq.value, [ch]: Math.max(lastSeq.value[ch] || 0, msg.seq || 0) }
  // What we wrote ourselves, from this tab or another, is read.
  if (msg.author.id === props.me.id) {
    readSeq.value = { ...readSeq.value, [ch]: Math.max(readSeq.value[ch] || 0, msg.seq || 0) }
  }
  if (ch !== activeId.value) return

  const known = messages.value.some(
    (m) => m.id === msg.id || (msg.client_msg_id && m.client_msg_id === msg.client_msg_id)
  )
  if (known) return
  // A number further on than the next one means some never arrived: fetch them
  // with this one rather than show it after a hole.
  const newest = newestSeq()
  if (newest && msg.seq > newest + 1) {
    catchUp()
    return
  }
  messages.value = inOrder([...messages.value, { ...msg, status: 'delivered' }])
  conversation.value?.toBottom()
  markRead()
}

/* ---------- typing ---------- */

// The typist repeats every 3 s and a listener forgets them 4 s after the last
// repeat, so one late repeat does not make it flicker. Telegram uses 5 s and
// 6 s, but also sends an explicit cancel; without one, the line would linger
// up to 6 s after the typing stopped, which is too long. Now it is at most 4 s.
const TYPING_REPEAT_MS = 3000
const TYPING_SHOWN_MS = 4000
// The server lets one typing frame per chat through each second and drops the
// rest (typingMinInterval). The margin covers the network: two frames sent a
// second apart can arrive closer than that.
const TYPING_SERVER_GAP_MS = 1100

// { [channelId]: { [userId]: username } } of the people typing right now.
const typing = ref({})
const typingTimers = new Map()

const activeTyping = computed(() => Object.values(typing.value[activeId.value] || {}))

function noteTyping(ev) {
  // Our own typing comes back from the bus like everyone else's, and to every
  // tab of ours.
  if (ev.user.id === props.me.id) return
  const key = `${ev.channel_id}/${ev.user.id}`
  clearTimeout(typingTimers.get(key))
  typingTimers.set(key, setTimeout(() => forgetTyping(ev.channel_id, ev.user.id), TYPING_SHOWN_MS))
  typing.value = {
    ...typing.value,
    [ev.channel_id]: { ...typing.value[ev.channel_id], [ev.user.id]: ev.user.username },
  }
}

function forgetTyping(channelId, userId) {
  const key = `${channelId}/${userId}`
  clearTimeout(typingTimers.get(key))
  typingTimers.delete(key)
  if (!typing.value[channelId]?.[userId]) return
  const rest = { ...typing.value[channelId] }
  delete rest[userId]
  typing.value = { ...typing.value, [channelId]: rest }
}

// Called on every keystroke; the socket hears about it once per interval and
// per chat.
let announcedAt = 0
let nextAnnounceAt = 0
let typedIn = null
function announceTyping() {
  const now = Date.now()
  if (typedIn === activeId.value && now < nextAnnounceAt) return
  typedIn = activeId.value
  announcedAt = now
  nextAnnounceAt = now + TYPING_REPEAT_MS
  socket?.send({ type: 'typing', channel_id: activeId.value })
}

// The listeners forget us when our message arrives, so the next keystroke
// should announce again without waiting out the whole interval — but not
// before the server would let it through. Announcing at once was dropped as
// too soon after the previous frame, and the listener then saw nothing until
// the next repeat, ~3 s later. Taking the earlier of the two keeps it right
// when several messages go in a row.
function rearmTyping() {
  nextAnnounceAt = Math.min(nextAnnounceAt, announcedAt + TYPING_SERVER_GAP_MS)
}

/* ---------- presence ---------- */

// { [userId]: { online, last_seen, epoch, version } }. Events about one person
// come from different server nodes and can overtake each other, so each state
// carries (epoch, version) and only a newer one replaces what we have. The
// epoch changes when the server's Redis was restarted and its versions began
// again from one.
const presence = ref({})

function newer(had, got) {
  if (!had) return true
  if (got.epoch !== had.epoch) return got.epoch > had.epoch
  return got.version >= had.version
}

function notePresence(ev) {
  const had = presence.value[ev.user_id]
  if (!newer(had, ev)) return
  presence.value = {
    ...presence.value,
    [ev.user_id]: {
      online: ev.online,
      last_seen: ev.last_seen || (ev.online ? null : had?.last_seen) || null,
      epoch: ev.epoch,
      version: ev.version,
    },
  }
}

// Events only tell what changes while the socket is up, so the state on arrival
// is asked for over REST — after a reconnect too, since anything could have
// happened while the tab was offline.
async function loadPresence() {
  const ids = channels.value
    .filter((c) => c.kind === 'direct')
    .map((c) => (c.members || []).find((m) => m.user_id !== props.me.id)?.user_id)
    .filter(Boolean)
  if (!ids.length) return

  const states = await api.presence(ids).catch(() => null)
  if (!states) return
  presence.value = Object.fromEntries(
    Object.entries(states).map(([id, s]) => [id, { online: s.online, last_seen: s.last_seen || null, epoch: s.epoch, version: s.version }])
  )
}

// The person on the other side of the open direct chat, if it is one.
const activePresence = computed(() => {
  if (!active.value || active.value.kind !== 'direct') return null
  const other = (active.value.members || []).find((m) => m.user_id !== props.me.id)
  return other ? presence.value[other.user_id] || null : null
})

// A socket that was down missed messages; the REST history is what fills the gap.
const CATCH_UP_PAGE = 100

// The newest number among the messages the open chat has from the server.
function newestSeq() {
  return messages.value.reduce((n, m) => Math.max(n, m.seq || 0), 0)
}

// Saved messages in number order, then the ones still on their way, as they were.
function inOrder(list) {
  const saved = list.filter((m) => m.seq).sort((a, b) => a.seq - b.seq)
  return [...saved, ...list.filter((m) => !m.seq)]
}

async function catchUp() {
  const channelId = activeId.value
  if (!channelId) return
  let after = newestSeq()
  if (!after) return selectChannel(channelId)

  const found = []
  for (;;) {
    const page = await api.messages({ channel_id: channelId, after_seq: after, limit: CATCH_UP_PAGE })
    if (activeId.value !== channelId) return
    found.push(...page)
    if (page.length < CATCH_UP_PAGE) break
    after = page[page.length - 1].seq
  }

  const known = (msg) => messages.value.some(
    (m) => m.id === msg.id || (msg.client_msg_id && m.client_msg_id === msg.client_msg_id)
  )
  const fresh = found.filter((m) => !known(m)).map((m) => ({ ...m, status: 'delivered' }))
  if (fresh.length) {
    messages.value = inOrder([...messages.value, ...fresh])
    conversation.value?.toBottom()
  }
  markRead()
}

// Tells the server how far the open chat has been read: only while the page is
// in front of the person, and once a second at most.
let readTimer = null
function markRead() {
  const channelId = activeId.value
  const seq = newestSeq()
  if (!channelId || !seq || document.visibilityState !== 'visible') return
  if (seq <= (readSeq.value[channelId] || 0)) return
  readSeq.value = { ...readSeq.value, [channelId]: seq }
  clearTimeout(readTimer)
  readTimer = setTimeout(() => {
    api.markRead(channelId, readSeq.value[channelId]).catch(() => {})
  }, 1000)
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
  try {
    await api.sendFriendRequest(username)
  } catch (e) {
    // Other refusals change nothing on screen; a limit is worth saying, with when it lifts.
    if (e.status === 429) tooMany.value = e.retryAfter ? `in ${waitText(e.retryAfter)}` : 'later'
  }
  loadPeople()
}

async function removeFriend(username) {
  const friend = friends.value.find((f) => f.username === username)
  if (!friend) return
  await api.removeFriend(friend.id).catch(() => null)
  loadPeople()
}

function toggleRequests() {
  const open = !showRequests.value || showProfile.value
  showProfile.value = false
  showInfo.value = false
  person.value = ''
  showRequests.value = open
}

// An invite followed from a message, by the same path as one followed from the
// address bar — the server answers with the channel whether we have just joined
// it or were in it already, so a click always ends in the conversation.
async function joinByInvite(code) {
  const joined = await api.followInvite(code).catch(() => null)
  if (!joined) return
  await loadChannels()
  selectChannel(joined.channel_id)
}

function openPerson(username) {
  showInfo.value = false
  showRequests.value = false
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

// Messages that came while the page was behind another are read once it is back.
function onVisible() {
  if (document.visibilityState === 'visible') markRead()
}

onMounted(async () => {
  document.addEventListener('visibilitychange', onVisible)
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
  // let the restored panel render without its slide before animations come back
  nextTick(() => { panesRestored.value = true })
  loadPeople()
  loadPresence()
  socket = createSocket({
    onMessage: receive,
    onSessionEnded: () => emit('session-ended'),
    onStateChange: (state) => {
      const wasOffline = connection.value === 'offline'
      connection.value = state
      if (state === 'online' && wasOffline) loadPresence()
    },
  })
})

onUnmounted(() => {
  socket && socket.close()
  typingTimers.forEach(clearTimeout)
  clearTimeout(readTimer)
  document.removeEventListener('visibilitychange', onVisible)
})
</script>

<template>
  <div class="screen">
    <div class="columns">

      <ChannelRail
        :me="me"
        :channels="channels"
        :active-id="activeId"
        :unread="unread"
        :friends="friends"
        :requests="requests"
        :sent-to="sentTo"
        :profile-open="showProfile"
        :requests-open="showRequests && !showProfile"
        @select="selectChannel"
        @create="dialog = 'create'"
        @open-direct="openDirect"
        @add-friend="addFriend"
        @requests="toggleRequests"
        @profile="showProfile = true"
        @person="openPerson"
      />

      <!-- Everything right of the rail. On a narrow window a side panel covers this
           box exactly, so the feed under it keeps its width and scroll position. -->
      <div class="stage">
      <Profile
        v-if="showProfile"
        :me="me"
        @saved="emit('profile-changed')"
        @log-out="emit('log-out')"
        @deleted="emit('log-out')"
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
        @invite="joinByInvite"
        @retry="deliver"
        @discard="discard"
        @load-older="loadOlder"
        @info="showInfo = !showInfo; person = ''; showRequests = false"
        :forward-targets="forwardTargets"
        :pending="pendingAction && pendingAction.channelId === activeId ? pendingAction : null"
        :originals="originals"
        :typing="activeTyping"
        :presence="activePresence"
        @typing="announceTyping"
        @reply="startReply"
        @find="findMessage"
        @forward="pickForward"
        @cancel-pending="pendingAction = null"
        @person="openPerson"
      />

      <p v-else class="empty">
        {{ channels.length ? 'Pick a channel or a conversation' : 'Create a channel or join one by code' }}
      </p>

      <!-- The side panels slide like the rail collapses. v-if alone would remove them
           in one frame; Transition keeps the node until the width has closed. -->
      <Transition name="side" :css="panesRestored">
        <div v-if="showInfo && active && !showProfile" class="side">
          <InfoPanel
            :me="me"
            :channel="active"
            :title="activeTitle"
            :friend="friends.some((f) => f.username === activeTitle)"
            @close="showInfo = false"
            @leave="showInfo = false; confirmLeave = true"
            @select="selectChannel"
            @person="openPerson"
            @changed="loadChannels"
            @unfriend="removeFriend"
          />
        </div>
      </Transition>

      <Transition name="side" :css="panesRestored">
        <div v-if="person && !showProfile" class="side">
          <UserPanel
            :username="person"
            :relation="relation"
            @close="person = ''"
            @message="openDirect"
            @befriend="addFriend"
            @unfriend="removeFriend"
            @select="selectChannel"
          />
        </div>
      </Transition>

      <Transition name="side" :css="panesRestored">
        <div v-if="showRequests && !showProfile" class="side">
          <RequestsPanel
            :incoming="requests"
            :sent="sentTo"
            @close="showRequests = false"
            @respond="respond"
            @person="openPerson"
          />
        </div>
      </Transition>
      </div>

    </div>

    <SgDialog v-if="confirmLeave" :title="`Leave #${active?.name}?`" @close="confirmLeave = false">
      <p class="dialog-text">
        You stop receiving messages from this channel. Rejoin any time with the invite code.
      </p>
      <div class="dialog-actions">
        <SgButton variant="danger" @click="confirmLeave = false; leaveChannel()">Leave</SgButton>
        <SgButton variant="outline" @click="confirmLeave = false">Cancel</SgButton>
      </div>
    </SgDialog>

    <SgDialog v-if="tooMany" title="Too many friend requests" @close="tooMany = ''">
      <p class="dialog-text">Try again {{ tooMany }}.</p>
      <div class="dialog-actions">
        <SgButton variant="outline" @click="tooMany = ''">Close</SgButton>
      </div>
    </SgDialog>

    <SgDialog v-if="dialog === 'create'" title="New channel" @close="dialog = null">
      <SgInput v-model="draftName" label="Name" hint="1-64 characters" :error="dialogError" />
      <div class="dialog-actions">
        <SgButton variant="primary" @click="createChannel">Create</SgButton>
        <SgButton variant="outline" @click="dialog = null">Cancel</SgButton>
      </div>
    </SgDialog>

  </div>
</template>

<style scoped>
.screen {
  position: relative;
  height: 100vh;
  display: flex;
  flex-direction: column;
  background: var(--paper);
  padding: 16px;
  box-sizing: border-box;
}
.columns {
  flex: 1;
  min-height: 0;
  display: flex;
  gap: 16px;
}
.empty {
  margin: auto;
  font: var(--text-meta);
  color: var(--text-muted);
}
.dialog-text {
  margin: 0;
  font: var(--text-body);
  color: var(--text-muted);
}
.dialog-actions {
  display: flex;
  gap: 12px;
}
.stage {
  flex: 1;
  min-width: 0;
  display: flex;
  gap: 16px;
  position: relative;
  /* The side panel asks this box, not the window, how much room there is: the
     room left of the stage depends on whether the rail is open. */
  container: stage / inline-size;
}
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
/* The panel stands beside the feed while the feed keeps at least 380px, Telegram
   Desktop's minimal chat column (columnMinimalWidthMain): 380 + the 16px gap + the
   340px panel = 736px of stage. Below that the panel covers the whole stage, as
   Telegram's info page replaces the chat on a narrow window; laying it over part
   of the feed cut bubbles in half. The feed underneath keeps its width, so
   nothing rewraps and its scroll position is still there when the panel closes.
   The threshold used to be the window's 1100px, which ignored the rail: with the
   rail folded, a 1000px window had room for both and still got the cover. */
@container stage (max-width: 735px) {
  .side {
    position: absolute;
    inset: 0;
    z-index: 10;
    background: var(--surface-page);
  }
  /* The panel sets its 340px inline; here it takes the stage's width instead,
     and may shrink below 340px on a very narrow window rather than be clipped. */
  .side > :deep(aside) {
    flex: 1 1 0 !important;
    min-width: 0;
  }
  /* covering, not sliding into a row: there is no gap to cancel */
  .side-enter-from,
  .side-leave-to {
    margin-left: 0;
  }
}
</style>
