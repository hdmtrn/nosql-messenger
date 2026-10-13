<script setup>
import { computed } from 'vue'
import ChannelGlyph from './ChannelGlyph.vue'
import SgAvatar from './SgAvatar.vue'
import SgButton from './SgButton.vue'
import { DELETED_NAME, avatarUrl, channelAvatarUrl, displayName, initials, otherMember } from '../naming'
import { usePeopleSearch } from '../search'

// The phone's first screen, in place of the rail: one list of every chat, newest
// first, as Telegram's mobile client shows it.
const props = defineProps({
  me: { type: Object, required: true },
  channels: { type: Array, default: () => [] },
  friends: { type: Array, default: () => [] },
  requests: { type: Array, default: () => [] },
  sentTo: { type: Array, default: () => [] },
  unread: { type: Object, default: () => ({}) },
})

const emit = defineEmits(['select', 'create', 'open-direct', 'add-friend', 'requests', 'profile', 'person'])

const { query, found, search } = usePeopleSearch(() => props.me.id)

const time = (c) => Date.parse(c.last_message?.created_at || c.created_at) || 0

const chats = computed(() => props.channels
  .map((c) => {
    const direct = c.kind === 'direct'
    // A conversation whose other side deleted the account has nobody to show.
    const username = direct ? otherMember(c, props.me.id)?.username || DELETED_NAME : ''
    return { c, direct, username, title: direct ? displayName(username) : c.name }
  })
  .sort((a, b) => time(b.c) - time(a.c)))

// Friends never written to have no chat yet, so they follow the chats on their own.
const quietFriends = computed(() => {
  const chatted = new Set(chats.value.filter((r) => r.direct).map((r) => r.username))
  return props.friends.filter((f) => !chatted.has(f.username))
})

// Who wrote the last line: in a channel anyone, in a conversation only "You" needs
// saying. A first name, as Telegram gives it, leaves the line to the text.
function sender({ c, direct }) {
  const last = c.last_message
  if (!last) return ''
  if (last.author.id === props.me.id) return 'You'
  return direct ? '' : displayName(last.author.username).split(/\s+/)[0]
}

function lastLine(c) {
  const last = c.last_message
  if (!last) return 'No messages yet'
  if (last.text) return last.text
  const n = last.attachments || 0
  return n > 1 ? `${n} photos` : n ? 'Photo' : ''
}

const DAY = 24 * 60 * 60 * 1000

// Telegram's stamp: the time today, the weekday within a week, the date before that.
function stamp(c) {
  if (!c.last_message) return ''
  const at = new Date(c.last_message.created_at)
  const now = new Date()
  if (at.toDateString() === now.toDateString()) {
    return at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
  }
  if (now - at < 6 * DAY) return at.toLocaleDateString([], { weekday: 'short' })
  return at.toLocaleDateString([], { day: '2-digit', month: '2-digit', year: '2-digit' })
}

const isFriend = (username) => props.friends.some((f) => f.username === username)
const isPending = (username) => props.sentTo.includes(username)
</script>

<template>
  <nav class="chat-list" aria-label="Chats">
    <header class="top">
      <button type="button" class="round me" aria-label="Profile" @click="emit('profile')">
        <SgAvatar :initials="initials(me.display_name)" :src="avatarUrl(me.username)" :size="36" />
      </button>
      <div class="search">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor"
             stroke-width="1.8" stroke-linecap="round" aria-hidden="true" class="search-icon">
          <circle cx="11" cy="11" r="7" />
          <path d="M20 20l-4-4" />
        </svg>
        <input v-model="query" type="search" placeholder="Search people" aria-label="Search people"
               enterkeyhint="search" class="search-input" @input="search">
      </div>
      <button type="button" class="round" aria-label="Friend requests" @click="emit('requests')">
        <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor"
             stroke-width="1.8" stroke-linecap="round" aria-hidden="true">
          <circle cx="9" cy="8" r="4" />
          <path d="M2 20c0-3.6 3.1-6 7-6s7 2.4 7 6M19 8v6M16 11h6" />
        </svg>
        <span v-if="requests.length" class="corner">{{ requests.length > 99 ? '99+' : requests.length }}</span>
      </button>
    </header>

    <div class="scroll">
      <template v-if="query.trim()">
        <p v-if="!found.length" class="note">Nothing found</p>
        <div v-for="u in found" :key="u.id" class="found">
          <button type="button" class="who" @click="emit('person', u.username)">
            <SgAvatar :initials="initials(u.display_name)" :src="avatarUrl(u.username)" :size="40" />
            <span class="who-name">{{ u.display_name }}</span>
          </button>
          <SgButton
            variant="outline" size="sm"
            :disabled="isPending(u.username)"
            @click="isFriend(u.username) ? emit('open-direct', u.username) : emit('add-friend', u.username)"
          >{{ isFriend(u.username) ? 'Message' : isPending(u.username) ? 'Sent' : 'Add' }}</SgButton>
        </div>
      </template>

      <template v-else>
        <button v-for="r in chats" :key="r.c.id" type="button" class="chat" @click="emit('select', r.c.id)">
          <SgAvatar v-if="r.direct" :initials="initials(r.title)" :src="avatarUrl(r.username)" :size="54" />
          <ChannelGlyph v-else :src="channelAvatarUrl(r.c)" :size="54" tone="blue" />
          <span class="lines">
            <span class="line">
              <span class="title">{{ r.title }}</span>
              <span class="stamp">{{ stamp(r.c) }}</span>
            </span>
            <span class="line">
              <span class="preview"><span v-if="sender(r)" class="sender">{{ sender(r) }}: </span>{{ lastLine(r.c) }}</span>
              <span v-if="unread[r.c.id]" class="badge">{{ unread[r.c.id] > 99 ? '99+' : unread[r.c.id] }}</span>
            </span>
          </span>
        </button>

        <template v-if="quietFriends.length">
          <h2 class="label">Friends</h2>
          <button v-for="f in quietFriends" :key="f.username" type="button" class="chat"
                  @click="emit('open-direct', f.username)">
            <SgAvatar :initials="initials(displayName(f.username))" :src="avatarUrl(f.username)" :size="54" />
            <span class="lines">
              <span class="line"><span class="title">{{ displayName(f.username) }}</span></span>
              <span class="line"><span class="preview">@{{ f.username }}</span></span>
            </span>
          </button>
        </template>

        <p v-if="!chats.length && !quietFriends.length" class="note">
          No chats yet. Find people above, or start a channel with the button below.
        </p>
      </template>
    </div>

    <button type="button" class="fab" aria-label="New channel" @click="emit('create')">
      <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor"
           stroke-width="2" stroke-linecap="round" aria-hidden="true">
        <path d="M12 5v14M5 12h14" />
      </svg>
    </button>
  </nav>
</template>

<style scoped>
/* The root's class carries the component's name: the parent's scoped styles reach it. */
.chat-list {
  position: relative;
  display: flex;
  flex-direction: column;
  background: var(--surface-panel);
}
/* 64px, Telegram's --column-header-height, with 48px targets on both sides. */
.top {
  flex: none;
  display: flex;
  align-items: center;
  gap: 4px;
  min-height: 64px;
  padding: max(8px, env(safe-area-inset-top)) max(8px, env(safe-area-inset-right)) 8px max(8px, env(safe-area-inset-left));
}
.round {
  position: relative;
  flex: none;
  display: grid;
  place-items: center;
  width: 48px;
  height: 48px;
  padding: 0;
  border: none;
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--text-primary);
  cursor: pointer;
}
.corner {
  position: absolute;
  top: 4px;
  right: 2px;
  min-width: 20px;
  padding: 0 6px;
  border-radius: var(--radius-pill);
  background: var(--surface-accent);
  color: var(--text-on-accent);
  font: var(--weight-medium) var(--size-12)/20px var(--font-ui);
  text-align: center;
}
.search {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  height: 44px;
  padding: 0 16px 0 12px;
  background: var(--surface-sunken);
  border-radius: var(--radius-pill);
}
.search-icon { flex: none; color: var(--text-muted); }
.search-input {
  flex: 1;
  min-width: 0;
  border: none;
  outline: none;
  background: transparent;
  font: var(--text-body);
  color: var(--text-primary);
  -webkit-appearance: none;
  appearance: none;
}
.search-input::-webkit-search-cancel-button { display: none; }
.scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  /* the last row clears the round button and the home indicator */
  padding: 0 max(8px, env(safe-area-inset-right)) calc(88px + env(safe-area-inset-bottom)) max(8px, env(safe-area-inset-left));
}
/* Telegram's row: 72px, a 54px avatar, two lines beside it. */
.chat {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  height: 72px;
  padding: 0 8px;
  border: none;
  border-radius: var(--radius-bubble);
  background: transparent;
  color: var(--text-primary);
  text-align: left;
  cursor: pointer;
}
.chat:active { background: var(--surface-sunken); }
.lines {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.line {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.title {
  flex: 1;
  min-width: 0;
  font: var(--text-heading);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.stamp {
  flex: none;
  font: var(--text-meta);
  color: var(--text-muted);
}
.preview {
  flex: 1;
  min-width: 0;
  font: var(--text-body);
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sender { color: var(--text-primary); }
.badge {
  flex: none;
  min-width: 22px;
  padding: 0 7px;
  border-radius: var(--radius-pill);
  background: var(--surface-accent);
  color: var(--text-on-accent);
  font: var(--weight-medium) var(--size-12)/22px var(--font-ui);
  text-align: center;
}
.label {
  margin: 16px 8px 4px;
  font: var(--text-label);
  color: var(--text-muted);
}
.note {
  margin: 24px 8px;
  font: var(--text-meta);
  color: var(--text-muted);
  text-align: center;
}
.found {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 56px;
  padding: 0 8px;
}
.who {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 48px;
  padding: 0;
  border: none;
  background: none;
  color: var(--text-primary);
  text-align: left;
  cursor: pointer;
}
.who-name {
  flex: 1;
  min-width: 0;
  font: var(--text-body);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
/* Material's floating action button: 56px, 16px from the corner. */
.fab {
  position: absolute;
  right: calc(16px + env(safe-area-inset-right));
  bottom: calc(16px + env(safe-area-inset-bottom));
  display: grid;
  place-items: center;
  width: 56px;
  height: 56px;
  padding: 0;
  border: none;
  border-radius: var(--radius-pill);
  background: var(--surface-accent);
  color: var(--text-on-accent);
  cursor: pointer;
}
</style>
