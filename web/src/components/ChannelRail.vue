<script setup>
import { computed, ref } from 'vue'
import ChannelRow from './ChannelRow.vue'
import SgAvatar from './SgAvatar.vue'
import SgButton from './SgButton.vue'
import { channelTitle, initials } from '../naming'
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

const emit = defineEmits(['select', 'create', 'join', 'open-direct', 'add-friend', 'respond', 'profile'])

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

const searchField = {
  display: 'flex',
  alignItems: 'center',
  gap: '10px',
  height: '40px',
  padding: '0 16px',
  background: 'var(--surface-panel)',
  borderRadius: 'var(--radius-pill)',
  marginBottom: '16px',
}

const footerStyle = computed(() => ({
  display: 'flex',
  alignItems: 'center',
  gap: '12px',
  width: '100%',
  padding: '8px 6px',
  marginTop: '16px',
  border: 'none',
  borderRadius: 'var(--radius-pill)',
  background: props.profileOpen ? 'var(--surface-panel)' : 'transparent',
  color: props.profileOpen ? 'var(--blue)' : '#fff',
  cursor: 'pointer',
}))
</script>

<template>
  <nav style="flex:0 0 286px;background:var(--surface-accent);border-radius:var(--radius-panel);
              padding:20px 16px;display:flex;flex-direction:column;gap:2px">

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

    <div style="flex:1;min-height:0;overflow-y:auto;display:flex;flex-direction:column;gap:2px">

      <template v-if="query.trim()">
        <span :style="label" style="padding:0 14px 12px">Search</span>
        <p v-if="!found.length" :style="label" style="padding:0 14px 8px">Nothing found</p>
        <div v-for="u in found" :key="u.id"
             style="display:flex;align-items:center;gap:12px;height:38px;padding:0 14px">
          <SgAvatar :initials="initials(u.display_name)" :size="22" tone="onBlue" />
          <span style="flex:1;min-width:0;font:var(--text-body);color:#fff;overflow:hidden;
                       text-overflow:ellipsis;white-space:nowrap">{{ u.display_name }}</span>
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
          <span :style="label" style="padding:0 14px 12px">Friend requests</span>
          <div v-for="r in requests" :key="r.id"
               style="display:flex;align-items:center;gap:8px;padding:4px 14px 8px">
            <SgAvatar :initials="initials(r.from.username)" :size="22" tone="onBlue" />
            <span style="flex:1;min-width:0;font:var(--text-body);color:#fff;overflow:hidden;
                         text-overflow:ellipsis;white-space:nowrap">{{ r.from.username }}</span>
            <SgButton variant="outline" size="sm" on-blue
                      @click="emit('respond', r.id, 'accept')">Yes</SgButton>
            <SgButton variant="ghost" size="sm" on-blue
                      @click="emit('respond', r.id, 'decline')">No</SgButton>
          </div>
        </template>

        <span :style="label" style="padding:20px 14px 12px">Channels</span>
        <ChannelRow
          v-for="c in named"
          :key="c.id"
          :name="c.name"
          :active="!profileOpen && c.id === activeId"
          :unread="unread[c.id] || 0"
          @click="emit('select', c.id)"
        />
        <div style="display:flex;gap:8px;padding:6px 8px 0">
          <SgButton variant="outline" size="sm" on-blue @click="emit('create')">+ New</SgButton>
          <SgButton variant="outline" size="sm" on-blue @click="emit('join')">Join by link</SgButton>
        </div>

        <span :style="label" style="padding:20px 14px 12px">Direct messages</span>
        <ChannelRow
          v-for="d in conversations"
          :key="d.username"
          :name="d.username"
          :avatar="initials(d.username)"
          :active="!profileOpen && d.channel && d.channel.id === activeId"
          :unread="d.channel ? unread[d.channel.id] || 0 : 0"
          @click="d.channel ? emit('select', d.channel.id) : emit('open-direct', d.username)"
        />
        <p v-if="!conversations.length" :style="label" style="padding:0 14px 8px">
          No friends yet — find people above
        </p>
      </template>
    </div>

    <button type="button" :style="footerStyle" @click="emit('profile')">
      <SgAvatar :initials="initials(me.display_name)" :size="32"
                :tone="profileOpen ? 'blue' : 'onBlue'" />
      <span style="flex:1;min-width:0;text-align:left;font:600 13px/1.2 var(--font-ui);
                   overflow:hidden;text-overflow:ellipsis;white-space:nowrap">
        {{ me.display_name }}
      </span>
    </button>
  </nav>
</template>
