<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'

// Every picture of the chat in feed order, as Telegram's media viewer flips
// through the whole conversation rather than one message.
const props = defineProps({
  // [{ key, url }]
  pictures: { type: Array, required: true },
  start: { type: Number, default: 0 },
})
const emit = defineEmits(['close'])

const index = ref(props.start)

const current = computed(() => props.pictures[index.value])
const hasPrev = computed(() => index.value > 0)
const hasNext = computed(() => index.value < props.pictures.length - 1)

function prev() { if (hasPrev.value) index.value-- }
function next() { if (hasNext.value) index.value++ }

// On the window rather than the box: an arrow button that disappears at either
// end takes the focus with it, and the keys would stop reaching the box.
function key(e) {
  if (e.key === 'Escape') emit('close')
  else if (e.key === 'ArrowLeft') prev()
  else if (e.key === 'ArrowRight') next()
  else return
  e.preventDefault()
}
onMounted(() => window.addEventListener('keydown', key))
onUnmounted(() => window.removeEventListener('keydown', key))

let touchX = null
function touchStart(e) { touchX = e.touches[0].clientX }
function touchEnd(e) {
  if (touchX === null) return
  const dx = e.changedTouches[0].clientX - touchX
  touchX = null
  if (dx > 50) prev()
  else if (dx < -50) next()
}
</script>

<template>
  <Teleport to="body">
    <div class="viewer" role="dialog" aria-modal="true" aria-label="Picture"
         @click.self="emit('close')"
         @touchstart.passive="touchStart" @touchend="touchEnd">
      <header class="bar">
        <span class="count">{{ pictures.length > 1 ? `${index + 1} / ${pictures.length}` : '' }}</span>
        <button type="button" class="round" aria-label="Close" @click="emit('close')">×</button>
      </header>

      <button v-if="hasPrev" type="button" class="nav left" aria-label="Previous picture" @click="prev">‹</button>
      <img :key="current.key" :src="current.url" alt="" class="picture" @click="next">
      <button v-if="hasNext" type="button" class="nav right" aria-label="Next picture" @click="next">›</button>
    </div>
  </Teleport>
</template>

<style scoped>
.viewer {
  position: fixed;
  inset: 0;
  z-index: 20;
  display: grid;
  place-items: center;
  padding: 64px 72px 24px;
  background: rgba(10, 10, 10, 0.92);
}
.bar {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 12px 16px 12px 24px;
  color: #fff;
}
.count {
  flex: 1;
  font: var(--text-machine-11);
  letter-spacing: var(--mono-tracking);
  opacity: 0.8;
}
.round {
  width: 40px;
  height: 40px;
  padding: 0;
  border: none;
  border-radius: var(--radius-pill);
  background: rgba(255, 255, 255, 0.12);
  color: #fff;
  font: 300 26px/40px var(--font-ui);
  cursor: pointer;
}
.round:hover { background: rgba(255, 255, 255, 0.24); }
.picture {
  max-width: 100%;
  max-height: calc(100vh - 88px);
  object-fit: contain;
  border-radius: 8px;
  cursor: pointer;
  user-select: none;
}
.nav {
  position: absolute;
  top: 50%;
  width: 48px;
  height: 48px;
  margin-top: -24px;
  padding: 0;
  border: none;
  border-radius: var(--radius-pill);
  background: rgba(255, 255, 255, 0.12);
  color: #fff;
  font: 300 32px/44px var(--font-ui);
  cursor: pointer;
}
.nav:hover { background: rgba(255, 255, 255, 0.24); }
.left { left: 12px; }
.right { right: 12px; }
@media (max-width: 600px) {
  .viewer { padding: 64px 8px 16px; }
}
/* touch screens flip with a swipe; a narrow window with a mouse keeps the arrows */
@media (hover: none) {
  .nav { display: none; }
}
</style>
