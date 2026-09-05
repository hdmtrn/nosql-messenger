<script setup>
import { computed } from 'vue'

const props = defineProps({
  size: { type: Number, default: 22 },
  tone: { type: String, default: 'onBlue' },
})

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

const color = computed(() => (props.tone === 'blue' ? 'var(--blue)' : '#fff'))
</script>

<template>
  <span
    aria-hidden="true"
    :style="{ position: 'relative', display: 'block', flex: 'none',
              width: size + 'px', height: size + 'px' }"
  >
    <span
      v-for="d in dots"
      :key="d.key"
      :style="{ position: 'absolute', left: d.left + 'px', top: d.top + 'px',
                width: d.d + 'px', height: d.d + 'px',
                borderRadius: 'var(--radius-pill)', background: color }"
    />
  </span>
</template>
