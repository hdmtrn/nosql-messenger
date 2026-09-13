<script setup>
import { computed, ref, watch } from 'vue'
import ChannelRow from './ChannelRow.vue'
import SgAvatar from './SgAvatar.vue'
import SgButton from './SgButton.vue'
import { channelTitle, displayName, initials, rememberName } from '../naming'
import { api } from '../api'

const props = defineProps({
  me: { type: Object, required: true },
  channels: { type: Array, default: () => [] },
  friends: { type: Array, default: () => [] },
  requests: { type: Array, default: () => [] },
  sentTo: { type: Array, default: () => [] },
  activeId: { type: String, default: null },
  unread: { type: Object, default: () => ({}) },
  profileOpen: Boolean,
})

const emit = defineEmits(['select', 'create', 'open-direct', 'add-friend', 'respond', 'profile', 'person'])

// The width is a preference of this browser, not of the account, so it is kept in
// localStorage. Storage can be unavailable (private mode, blocked site data), and
// then the rail simply starts expanded.
const COLLAPSED_KEY = 'rail-collapsed'

function storedCollapsed() {
  try {
    return localStorage.getItem(COLLAPSED_KEY) === '1'
  } catch {
    return false
  }
}

const collapsed = ref(storedCollapsed())

watch(collapsed, (value) => {
  try {
    localStorage.setItem(COLLAPSED_KEY, value ? '1' : '0')
  } catch {
    // Not remembered, but the toggle still works for this page.
  }
})

const query = ref('')
const found = ref([])
let timer = null

// The field searches and nothing else: what it finds is people, and what you do
// with them is add or message, depending on whether you are friends already.
function search() {
  clearTimeout(timer)
  timer = setTimeout(async () => {
    const q = query.value.trim()
    const hits = q ? await api.searchUsers(q).catch(() => []) : []
    for (const u of hits) rememberName(u.username, u.display_name)
    found.value = hits.filter((u) => u.id !== props.me.id)
  }, 200)
}

const named = computed(() => props.channels.filter((c) => c.kind !== 'direct'))

// Every friend belongs here, whether or not a conversation has been opened yet.
const conversations = computed(() => {
  const byUsername = new Map()
  for (const c of props.channels.filter((c) => c.kind === 'direct')) {
    byUsername.set(channelTitle(c, props.me.id), c)
  }
  return props.friends.map((f) => ({
    username: f.username,
    channel: byUsername.get(f.username) || null,
  }))
})

const isFriend = (username) => props.friends.some((f) => f.username === username)
const isPending = (username) => props.sentTo.includes(username)

const label = {
  font: 'var(--text-machine)',
  textTransform: 'uppercase',
  letterSpacing: 'var(--mono-tracking)',
  color: 'var(--text-on-blue-muted)',
}

// Avatar and name of someone found or asking: together they open that person's page.
const person = {
  display: 'flex',
  alignItems: 'center',
  gap: '12px',
  flex: 1,
  minWidth: 0,
  padding: 0,
  border: 'none',
  background: 'none',
  cursor: 'pointer',
}
const personName = {
  flex: 1,
  minWidth: 0,
  textAlign: 'left',
  font: 'var(--text-body)',
  color: '#fff',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
}

const searchField = {
  flex: 1,
  minWidth: 0,
  display: 'flex',
  alignItems: 'center',
  gap: '10px',
  height: '40px',
  padding: '0 16px',
  background: 'var(--surface-panel)',
  borderRadius: 'var(--radius-pill)',
}

const navStyle = computed(() => ({
  flex: `0 0 ${collapsed.value ? 70 : 286}px`,
  transition: 'flex-basis 0.18s ease',
  overflow: 'hidden',
  background: 'var(--surface-accent)',
  borderRadius: 'var(--radius-panel)',
  padding: collapsed.value ? '20px 12px' : '20px 16px',
  display: 'flex',
  flexDirection: 'column',
  gap: '2px',
}))

const toggleStyle = {
  flex: 'none',
  width: '40px',
  height: '40px',
  border: 'none',
  borderRadius: 'var(--radius-pill)',
  background: 'transparent',
  color: '#fff',
  font: '300 22px/1 var(--font-ui)',
  cursor: 'pointer',
}

const divider = {
  height: '1px',
  margin: '10px 8px',
  background: 'rgba(255,255,255,0.25)',
}

const footerStyle = computed(() => ({
  display: 'flex',
  alignItems: 'center',
  justifyContent: collapsed.value ? 'center' : 'flex-start',
  gap: '12px',
  width: '100%',
  padding: collapsed.value ? '6px 0' : '8px 6px',
  marginTop: '16px',
  border: 'none',
  borderRadius: 'var(--radius-pill)',
  background: props.profileOpen ? 'var(--surface-panel)' : 'transparent',
  color: props.profileOpen ? 'var(--blue)' : '#fff',
  cursor: 'pointer',
}))
</script>


<template>
  <nav :style="navStyle">

    <div v-if="collapsed" style="display:flex;justify-content:center;margin-bottom:16px">
      <button type="button" :style="toggleStyle" aria-label="Expand sidebar" title="Expand"
              aria-expanded="false" @click="collapsed = false">›</button>
    </div>
    <div v-else style="display:flex;align-items:center;gap:4px;margin-bottom:16px">
      <div :style="searchField">
        <span aria-hidden="true" style="color:var(--grey)">⌕</span>
        <input
          v-model="query"
          placeholder="Search"
          style="flex:1;min-width:0;border:none;outline:none;background:transparent;
                 font:var(--text-body);color:var(--text-primary)"
          @input="search"
        >
      </div>
      <button type="button" :style="toggleStyle" aria-label="Collapse sidebar" title="Collapse"
              aria-expanded="true" @click="collapsed = true">‹</button>
    </div>

    <div style="flex:1;min-height:0;overflow-y:auto;overflow-x:hidden;
                display:flex;flex-direction:column;gap:2px">

      <template v-if="query.trim() && !collapsed">
        <span :style="label" style="padding:0 14px 12px">Search</span>
        <p v-if="!found.length" :style="label" style="padding:0 14px 8px">Nothing found</p>
        <div v-for="u in found" :key="u.id"
             style="display:flex;align-items:center;gap:12px;height:38px;padding:0 14px">
          <button type="button" :style="person" @click="emit('person', u.username)">
            <SgAvatar :initials="initials(u.display_name)" :size="22" tone="onBlue" />
            <span :style="personName">{{ u.display_name }}</span>
          </button>
          <SgButton
            variant="outline" size="sm" on-blue
            :disabled="isPending(u.username)"
            @click="isFriend(u.username) ? emit('open-direct', u.username)
                                         : emit('add-friend', u.username)"
          >{{ isFriend(u.username) ? 'Message' : isPending(u.username) ? 'Sent' : 'Add' }}</SgButton>
        </div>
      </template>

      <template v-else>
        <template v-if="requests.length">
          <!-- Answering needs the buttons, so a collapsed request only opens the rail. -->
          <template v-if="collapsed">
            <button v-for="r in requests" :key="r.id" type="button"
                    :title="`Friend request from ${r.from.username}`"
                    :aria-label="`Friend request from ${r.from.username}`"
                    style="display:flex;justify-content:center;align-items:center;height:44px;
                           border:none;background:transparent;cursor:pointer"
                    @click="collapsed = false">
              <SgAvatar :initials="initials(displayName(r.from.username))" :size="28" tone="onBlue" />
            </button>
            <div :style="divider" />
          </template>
          <template v-else>
            <span :style="label" style="padding:0 14px 12px">Friend requests</span>
            <div v-for="r in requests" :key="r.id"
                 style="display:flex;align-items:center;gap:8px;padding:4px 14px 8px">
              <button type="button" :style="person" @click="emit('person', r.from.username)">
                <SgAvatar :initials="initials(displayName(r.from.username))" :size="22" tone="onBlue" />
                <span :style="personName">{{ displayName(r.from.username) }}</span>
              </button>
              <SgButton variant="outline" size="sm" on-blue
                        @click="emit('respond', r.id, 'accept')">Yes</SgButton>
              <SgButton variant="ghost" size="sm" on-blue
                        @click="emit('respond', r.id, 'decline')">No</SgButton>
            </div>
          </template>
        </template>

        <div v-if="!collapsed" style="display:flex;align-items:center;padding:20px 14px 12px">
          <span :style="label" style="flex:1">Channels</span>
          <SgButton
            variant="ghost" size="sm" on-blue :mono="false"
            aria-label="New channel"
            style="width:26px;padding:0;font:300 22px/1 var(--font-ui)"
            @click="emit('create')"
          >+</SgButton>
        </div>
        <ChannelRow
          v-for="c in named"
          :key="c.id"
          :name="c.name"
          :compact="collapsed"
          :active="!profileOpen && c.id === activeId"
          :unread="unread[c.id] || 0"
          @click="emit('select', c.id)"
        />
        <div v-if="collapsed" :style="divider" />
        <span v-else :style="label" style="padding:20px 14px 12px">Direct messages</span>
        <ChannelRow
          v-for="d in conversations"
          :key="d.username"
          :name="displayName(d.username)"
          :avatar="initials(displayName(d.username))"
          :compact="collapsed"
          :active="!profileOpen && d.channel && d.channel.id === activeId"
          :unread="d.channel ? unread[d.channel.id] || 0 : 0"
          @click="d.channel ? emit('select', d.channel.id) : emit('open-direct', d.username)"
        />
        <p v-if="!conversations.length && !collapsed" :style="label" style="padding:0 14px 8px">
          No friends yet — find people above
        </p>
      </template>
    </div>

    <button type="button" :style="footerStyle" :title="collapsed ? me.display_name : undefined"
            :aria-label="collapsed ? me.display_name : undefined" @click="emit('profile')">
      <SgAvatar :initials="initials(me.display_name)" :size="32"
                :tone="profileOpen ? 'blue' : 'onBlue'" />
      <span v-if="!collapsed"
            style="flex:1;min-width:0;text-align:left;font:600 13px/1.2 var(--font-ui);
                   overflow:hidden;text-overflow:ellipsis;white-space:nowrap">
        {{ me.display_name }}
      </span>
    </button>
  </nav>
</template>
