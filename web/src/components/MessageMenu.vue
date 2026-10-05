<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import ChannelGlyph from './ChannelGlyph.vue'
import SgAvatar from './SgAvatar.vue'

const props = defineProps({
  // Viewport point the menu opens at: the cursor, or the bubble for the keyboard.
  x: { type: Number, required: true },
  y: { type: Number, required: true },
  // [{ id, title, direct, initials, avatar }] — chats the message can be forwarded to.
  targets: { type: Array, default: () => [] },
})
const emit = defineEmits(['reply', 'copy', 'forward', 'close'])

const WIDTH = 220
const HEIGHT = 170
const SUB_WIDTH = 260
const ROW = 46
const SUB_MAX = 340
const GAP = 8
const MARGIN = 8

const root = ref(null)
const subOpen = ref(false)

// Near the right or bottom edge the menu opens towards the inside instead of
// being cut off by the window.
const left = Math.min(props.x, window.innerWidth - WIDTH - MARGIN)
const top = Math.min(props.y, window.innerHeight - HEIGHT - MARGIN)

// The chat list unfolds beside the Forward row: to the right when it fits,
// otherwise to the left, and lifted when it would run past the bottom.
const sub = computed(() => {
  const height = Math.min(props.targets.length * ROW + 16, SUB_MAX)
  // Forward is the third row: 8px padding plus two 44px rows and their 2px gaps.
  const rowTop = 100
  const shift = Math.min(0, window.innerHeight - MARGIN - (top + rowTop + height))
  return {
    top: rowTop + shift,
    right: left + WIDTH + GAP + SUB_WIDTH <= window.innerWidth - MARGIN,
  }
})

async function openSub() {
  subOpen.value = true
  await nextTick()
  root.value?.querySelector('.sub button')?.focus()
}

function onPointer(e) {
  if (root.value && !root.value.contains(e.target)) emit('close')
}
function onKey(e) {
  if (e.key === 'Escape') emit('close')
}
// The menu is pinned to the window, so it would drift away from its message.
function onScroll(e) {
  if (!root.value?.contains(e.target)) emit('close')
}

onMounted(async () => {
  window.addEventListener('pointerdown', onPointer, true)
  window.addEventListener('keydown', onKey)
  window.addEventListener('scroll', onScroll, true)
  await nextTick()
  root.value?.querySelector('button')?.focus()
})
onUnmounted(() => {
  window.removeEventListener('pointerdown', onPointer, true)
  window.removeEventListener('keydown', onKey)
  window.removeEventListener('scroll', onScroll, true)
})
</script>

<template>
  <Teleport to="body">
    <div ref="root" class="anchor" :style="{ '--left': left + 'px', '--top': top + 'px' }">
      <div role="menu" class="panel main">
        <button type="button" role="menuitem" class="item"
                @mouseenter="subOpen = false" @click="emit('reply')">Reply</button>
        <button type="button" role="menuitem" class="item"
                @mouseenter="subOpen = false" @click="emit('copy')">Copy text</button>
        <button type="button" role="menuitem" class="item" :class="{ open: subOpen }"
                aria-haspopup="menu" :aria-expanded="subOpen"
                @mouseenter="subOpen = true" @click="openSub" @keydown.right.prevent="openSub">
          <span class="grow">Forward</span>
          <span aria-hidden="true">›</span>
        </button>
      </div>

      <div v-if="subOpen" role="menu" aria-label="Forward to" class="panel sub"
           :class="sub.right ? 'to-right' : 'to-left'" :style="{ '--sub-top': sub.top + 'px' }"
           @keydown.left.prevent="subOpen = false">
        <p v-if="!targets.length" class="note">No other chats</p>
        <button v-for="t in targets" :key="t.id" type="button" role="menuitem" class="item"
                @click="emit('forward', t.id)">
          <SgAvatar v-if="t.direct" :initials="t.initials" :src="t.avatar" :size="22" tone="onBlue" />
          <ChannelGlyph v-else :src="t.avatar" :size="22" />
          <span class="title">{{ t.title }}</span>
        </button>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
/* WIDTH, SUB_WIDTH and SUB_MAX above are the same numbers: the script needs
   them to fit the menu inside the window. */
.anchor {
  position: fixed;
  left: var(--left);
  top: var(--top);
  z-index: 20;
}
.main { width: 220px; }
.panel {
  position: relative;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px;
  background: var(--surface-inverse);
  border-radius: var(--radius-panel);
}
.sub {
  position: absolute;
  top: var(--sub-top);
  width: 260px;
  max-height: 340px;
  overflow-y: auto;
}
.to-right { left: 228px; }
.to-left { right: 228px; }
.grow { flex: 1; }
.item {
  display: flex;
  flex: none;
  align-items: center;
  gap: 14px;
  height: 44px;
  padding: 0 18px;
  border: none;
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--text-on-inverse);
  font: var(--text-body);
  text-align: left;
  cursor: pointer;
  transition: background 90ms linear;
}
.item:hover,
.item:focus-visible,
.item.open {
  background: rgba(255, 255, 255, 0.14);
  outline: none;
}
.title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.note {
  margin: 0;
  padding: 12px 18px;
  font: 500 13px/1.3 var(--font-ui);
  color: rgba(255, 255, 255, 0.72);
}

</style>
