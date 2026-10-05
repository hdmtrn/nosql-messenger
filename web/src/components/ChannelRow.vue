<script setup>
import ChannelGlyph from './ChannelGlyph.vue'
import SgAvatar from './SgAvatar.vue'

defineProps({
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
</script>

<template>
  <button
    type="button"
    :aria-current="active || undefined"
    :aria-label="compact ? name : undefined"
    :title="compact ? name : undefined"
    class="channel-row"
    :class="{ 'channel-row--active': active, 'channel-row--compact': compact }"
    @click="$emit('click')"
  >
    <span class="mark">
      <SgAvatar v-if="avatar" :initials="avatar" :src="avatarSrc" :size="MARK" :tone="active ? 'blue' : 'onBlue'" />
      <ChannelGlyph v-else :src="avatarSrc" :size="MARK" :tone="active ? 'blue' : 'onBlue'" />
    </span>
    <span v-if="!compact" class="name">{{ name }}</span>
    <span v-if="unread > 0 && !active" class="badge">{{ unread > 99 ? '99+' : unread }}</span>
  </button>
</template>

<style scoped>
/* The root's classes carry the component's name: the parent's scoped styles reach it. */
.channel-row {
  position: relative;
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  height: 40px;
  padding: 0 12px 0 0;
  text-align: left;
  cursor: pointer;
  border: none;
  border-radius: var(--radius-pill);
  background: transparent;
  color: #fff;
}
/* Collapsed, the row is exactly the 40px column, so the active pill is a circle. */
.channel-row--compact { padding: 0; }
.channel-row--active {
  background: var(--surface-panel);
  color: var(--blue);
}
.mark {
  flex: 0 0 40px;
  display: flex;
  justify-content: center;
}
.name {
  flex: 1;
  min-width: 0;
  font: var(--text-body);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.badge {
  font: var(--text-machine);
  text-transform: uppercase;
  letter-spacing: var(--mono-tracking);
  background: #fff;
  color: var(--blue);
  border-radius: var(--radius-pill);
  padding: 2px 7px;
}
/* Without the name there is no room beside the mark, so the count sits on its corner. */
.channel-row--compact .badge {
  position: absolute;
  top: 0;
  right: 0;
  padding: 1px 5px;
}
</style>
