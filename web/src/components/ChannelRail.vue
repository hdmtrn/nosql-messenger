<script setup>
import { computed } from 'vue'
import ChannelRow from './ChannelRow.vue'
import SgAvatar from './SgAvatar.vue'
import SgButton from './SgButton.vue'
import { channelTitle, initials } from '../naming'

const props = defineProps({
  me: { type: Object, required: true },
  channels: { type: Array, default: () => [] },
  activeId: { type: String, default: null },
  unread: { type: Object, default: () => ({}) },
  incomingRequests: { type: Number, default: 0 },
  profileOpen: Boolean,
})

const emit = defineEmits(['select', 'create', 'join', 'friends', 'profile', 'log-out'])

// Channels and conversations are different kinds of place, and the rail says so.
const named = computed(() => props.channels.filter((c) => c.kind !== 'direct'))
const direct = computed(() => props.channels.filter((c) => c.kind === 'direct'))

const label = {
  font: 'var(--text-machine)',
  textTransform: 'uppercase',
  letterSpacing: 'var(--mono-tracking)',
  color: 'var(--text-on-blue-muted)',
}

const footerStyle = computed(() => ({
  display: 'flex',
  alignItems: 'center',
  gap: '12px',
  width: '100%',
  padding: '8px 6px',
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

    <div style="display:flex;gap:8px;padding:0 8px 16px">
      <SgButton variant="outline" size="sm" on-blue @click="emit('create')">+ New</SgButton>
      <SgButton variant="outline" size="sm" on-blue @click="emit('join')">Join by code</SgButton>
    </div>

    <ChannelRow name="Friends" :unread="incomingRequests" @click="emit('friends')" />

    <div style="flex:1;min-height:0;overflow-y:auto;display:flex;flex-direction:column;gap:2px">
      <span :style="label" style="padding:20px 14px 12px">Channels</span>
      <ChannelRow
        v-for="c in named"
        :key="c.id"
        :name="c.name"
        :active="!profileOpen && c.id === activeId"
        :unread="unread[c.id] || 0"
        @click="emit('select', c.id)"
      />
      <p v-if="!named.length" :style="label" style="padding:0 14px 8px">None yet</p>

      <span :style="label" style="padding:20px 14px 12px">Direct messages</span>
      <ChannelRow
        v-for="c in direct"
        :key="c.id"
        :name="channelTitle(c, me.id)"
        :avatar="initials(channelTitle(c, me.id))"
        :active="!profileOpen && c.id === activeId"
        :unread="unread[c.id] || 0"
        @click="emit('select', c.id)"
      />
      <p v-if="!direct.length" :style="label" style="padding:0 14px 8px">None yet</p>
    </div>

    <button type="button" :style="footerStyle" style="margin-top:16px" @click="emit('profile')">
      <SgAvatar :initials="initials(me.display_name)" :size="32"
                :tone="profileOpen ? 'blue' : 'onBlue'" />
      <span style="flex:1;min-width:0;text-align:left;font:600 13px/1.2 var(--font-ui);
                   overflow:hidden;text-overflow:ellipsis;white-space:nowrap">
        {{ me.display_name }}
      </span>
    </button>
  </nav>
</template>
