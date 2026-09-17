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
  avatarSrc: { type: String, default: '' },
  // The collapsed rail keeps the mark alone; the name moves into the tooltip.
  compact: Boolean,
})
defineEmits(['click'])

// One layout for both rail widths: the mark keeps its size and its 40px column,
// so collapsing hides the name and nothing else moves.
const MARK = 24

const style = computed(() => ({
  position: 'relative',
  display: 'flex',
  alignItems: 'center',
  gap: '8px',
  width: '100%',
  height: '40px',
  // Collapsed, the row is exactly the 40px column, so the active pill is a circle.
  padding: props.compact ? '0' : '0 12px 0 0',
  textAlign: 'left',
  cursor: 'pointer',
  border: 'none',
  borderRadius: 'var(--radius-pill)',
  background: props.active ? 'var(--surface-panel)' : 'transparent',
  color: props.active ? 'var(--blue)' : '#fff',
}))

const badge = computed(() => ({
  font: 'var(--text-machine)',
  textTransform: 'uppercase',
  letterSpacing: 'var(--mono-tracking)',
  background: '#fff',
  color: 'var(--blue)',
  borderRadius: 'var(--radius-pill)',
  padding: props.compact ? '1px 5px' : '2px 7px',
  // Without the name there is no room beside the mark, so the count sits on its corner.
  ...(props.compact && { position: 'absolute', top: '0', right: '0' }),
}))
</script>

<template>
  <button
    type="button"
    :aria-current="active || undefined"
    :aria-label="compact ? name : undefined"
    :title="compact ? name : undefined"
    :style="style"
    @click="$emit('click')"
  >
    <span style="flex:0 0 40px;display:flex;justify-content:center">
      <SgAvatar v-if="avatar" :initials="avatar" :src="avatarSrc" :size="MARK" :tone="active ? 'blue' : 'onBlue'" />
      <ChannelGlyph v-else :src="avatarSrc" :size="MARK" :tone="active ? 'blue' : 'onBlue'" />
    </span>
    <span v-if="!compact"
          style="flex:1;min-width:0;font:var(--text-body);overflow:hidden;
                 text-overflow:ellipsis;white-space:nowrap">{{ name }}</span>
    <span v-if="unread > 0 && !active" :style="badge">{{ unread > 99 ? '99+' : unread }}</span>
  </button>
</template>
