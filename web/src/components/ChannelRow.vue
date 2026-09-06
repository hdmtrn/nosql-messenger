<script setup>
import { computed } from 'vue'
import ChannelGlyph from './ChannelGlyph.vue'
import SgAvatar from './SgAvatar.vue'

const props = defineProps({
  name: String,
  active: Boolean,
  unread: { type: Number, default: 0 },
  // A person is shown by their initials where a channel shows the glyph.
  avatar: { type: String, default: '' },
})
defineEmits(['click'])

const style = computed(() => ({
  display: 'flex',
  alignItems: 'center',
  gap: '12px',
  width: '100%',
  height: '38px',
  padding: '0 14px',
  textAlign: 'left',
  cursor: 'pointer',
  border: 'none',
  borderRadius: 'var(--radius-pill)',
  background: props.active ? 'var(--surface-panel)' : 'transparent',
  color: props.active ? 'var(--blue)' : '#fff',
}))
</script>

<template>
  <button type="button" :aria-current="active || undefined" :style="style" @click="$emit('click')">
    <SgAvatar v-if="avatar" :initials="avatar" :size="22" :tone="active ? 'blue' : 'onBlue'" />
    <ChannelGlyph v-else :size="22" :tone="active ? 'blue' : 'onBlue'" />
    <span style="flex:1;min-width:0;font:var(--text-body);overflow:hidden;
                 text-overflow:ellipsis;white-space:nowrap">{{ name }}</span>
    <span
      v-if="unread > 0 && !active"
      style="font:var(--text-machine);text-transform:uppercase;letter-spacing:var(--mono-tracking);
             background:#fff;color:var(--blue);border-radius:var(--radius-pill);padding:2px 7px"
    >{{ unread > 99 ? '99+' : unread }}</span>
  </button>
</template>
