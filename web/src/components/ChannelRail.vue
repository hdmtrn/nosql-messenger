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

const emit = defineEmits(['select', 'create', 'join', 'friends', 'profile'])

const rowBase = {
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

const footerStyle = computed(() => ({
  ...rowBase,
  height: 'auto',
  marginTop: '16px',
  padding: '8px 6px',
  background: props.profileOpen ? 'var(--surface-panel)' : 'transparent',
  color: props.profileOpen ? 'var(--blue)' : '#fff',
}))

const badge = {
  font: 'var(--text-machine)',
  textTransform: 'uppercase',
  letterSpacing: 'var(--mono-tracking)',
  background: '#fff',
  color: 'var(--blue)',
  borderRadius: 'var(--radius-pill)',
  padding: '2px 7px',
}
</script>

<template>
  <nav style="flex:0 0 286px;background:var(--surface-accent);border-radius:var(--radius-panel);
              padding:20px 16px;display:flex;flex-direction:column">
    <div style="display:flex;gap:8px;padding:0 8px 16px">
      <SgButton variant="outline" size="sm" on-blue @click="emit('create')">+ New</SgButton>
      <SgButton variant="outline" size="sm" on-blue @click="emit('join')">Join by code</SgButton>
    </div>

    <button type="button" :style="rowBase" @click="emit('friends')">
      <span style="flex:1;min-width:0;font:var(--text-body);text-align:left">Friends</span>
      <span v-if="incomingRequests" :style="badge">{{ incomingRequests }}</span>
    </button>

    <div style="height:1px;background:rgba(255,255,255,0.18);margin:12px 14px"></div>

    <div style="flex:1;min-height:0;overflow-y:auto;display:flex;flex-direction:column;gap:2px">
      <ChannelRow
        v-for="c in channels"
        :key="c.id"
        :name="channelTitle(c, me.id)"
        :active="!profileOpen && c.id === activeId"
        :unread="unread[c.id] || 0"
        @click="emit('select', c.id)"
      />
      <p v-if="!channels.length" class="sg-mono"
         style="color:var(--text-on-blue-muted);padding:8px 14px">No channels yet</p>
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
