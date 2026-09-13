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
  // The collapsed rail keeps the mark alone; the name moves into the tooltip.
  compact: Boolean,
})
defineEmits(['click'])

const mark = computed(() => (props.compact ? 28 : 22))

const style = computed(() => ({
  position: 'relative',
  display: 'flex',
  alignItems: 'center',
  justifyContent: props.compact ? 'center' : 'flex-start',
  gap: '12px',
  width: '100%',
  height: props.compact ? '44px' : '38px',
  padding: props.compact ? '0' : '0 14px',
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
    <SgAvatar v-if="avatar" :initials="avatar" :size="mark" :tone="active ? 'blue' : 'onBlue'" />
    <ChannelGlyph v-else :size="mark" :tone="active ? 'blue' : 'onBlue'" />
    <span v-if="!compact"
          style="flex:1;min-width:0;font:var(--text-body);overflow:hidden;
                 text-overflow:ellipsis;white-space:nowrap">{{ name }}</span>
    <span v-if="unread > 0 && !active" :style="badge">{{ unread > 99 ? '99+' : unread }}</span>
  </button>
</template>
