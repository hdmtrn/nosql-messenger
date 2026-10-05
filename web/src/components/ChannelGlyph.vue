<script setup>
import { computed, ref, watch } from 'vue'

const props = defineProps({
  size: { type: Number, default: 22 },
  tone: { type: String, default: 'onBlue' },
  // The channel's own picture. The dots stay as the fallback, as initials do
  // for a person.
  src: { type: String, default: '' },
})

const failed = ref(false)
watch(() => props.src, () => { failed.value = false })

const dots = computed(() => {
  const { size } = props
  const cols = size <= 40 ? 7 : 11
  const step = size / cols
  const dot = step * (size <= 40 ? 0.78 : 0.62)
  const r = size / 2
  const out = []
  for (let y = 0; y < cols; y++) {
    for (let x = 0; x < cols; x++) {
      const cx = (x + 0.5) * step
      const cy = (y + 0.5) * step
      if (Math.hypot(cx - r, cy - r) + dot / 2 <= r) {
        out.push({ key: `${x}-${y}`, left: cx - dot / 2, top: cy - dot / 2, d: dot })
      }
    }
  }
  return out
})
</script>

<template>
  <img v-if="src && !failed" :src="src" alt="" class="channel-glyph channel-glyph--picture" :style="{ '--size': size + 'px' }"
       @error="failed = true">
  <span v-else aria-hidden="true" class="channel-glyph channel-glyph--dots" :class="{ 'channel-glyph--blue': tone === 'blue' }"
        :style="{ '--size': size + 'px' }">
    <span v-for="d in dots" :key="d.key" class="dot"
          :style="{ '--x': d.left + 'px', '--y': d.top + 'px', '--d': d.d + 'px' }" />
  </span>
</template>

<style scoped>
/* The root's classes carry the component's name: the parent's scoped styles reach it. */
.channel-glyph {
  display: block;
  flex: none;
  width: var(--size);
  height: var(--size);
}
.channel-glyph--picture {
  border-radius: var(--radius-pill);
  object-fit: cover;
}
.channel-glyph--dots { position: relative; }
.dot {
  position: absolute;
  left: var(--x);
  top: var(--y);
  width: var(--d);
  height: var(--d);
  border-radius: var(--radius-pill);
  background: #fff;
}
.channel-glyph--blue .dot { background: var(--blue); }
</style>
