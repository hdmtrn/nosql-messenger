<script setup>
import { ref, watch } from 'vue'

const props = defineProps({
  initials: { type: String, default: '' },
  // A picture that fails to load falls back to the initials.
  src: { type: String, default: '' },
  size: { type: Number, default: 36 },
  tone: { type: String, default: 'blue' },
})

const failed = ref(false)
watch(() => props.src, () => { failed.value = false })
</script>

<template>
  <img v-if="src && !failed" :src="src" alt="" class="sg-avatar sg-avatar--picture" :class="`sg-avatar--${tone}`"
       :style="{ '--size': size + 'px' }" @error="failed = true">
  <span v-else class="sg-avatar sg-avatar--initials" :class="[`sg-avatar--${tone}`, { 'sg-avatar--small': size <= 24 }]"
        :style="{ '--size': size + 'px' }">{{ [...initials].slice(0, 2).join('') }}</span>
</template>

<style scoped>
/* Both branches are the root, which the parent's scoped styles reach too, so the
   classes carry the component's name. */
.sg-avatar {
  flex: none;
  width: var(--size);
  height: var(--size);
  border-radius: var(--radius-pill);
}
.sg-avatar--picture {
  display: block;
  object-fit: cover;
}
.sg-avatar--initials {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font: var(--text-machine-11);
  text-transform: uppercase;
  letter-spacing: var(--mono-tracking);
}
.sg-avatar--initials.sg-avatar--small { font: var(--text-machine); }

.sg-avatar--initials.sg-avatar--blue { background: var(--surface-accent); color: #fff; }
.sg-avatar--initials.sg-avatar--sending,
.sg-avatar--initials.sg-avatar--failed { background: var(--surface-panel); }
.sg-avatar--initials.sg-avatar--sending { color: var(--blue); }
.sg-avatar--initials.sg-avatar--failed { color: var(--status-error); }
.sg-avatar--initials.sg-avatar--onBlue { background: rgba(255, 255, 255, 0.18); color: #fff; }
.sg-avatar--initials.sg-avatar--ink { background: var(--surface-inverse); color: #fff; }

/* A picture takes only the ring of its tone, never the fill. */
.sg-avatar--sending { box-shadow: inset 0 0 0 2px var(--border-active); }
.sg-avatar--failed { box-shadow: inset 0 0 0 2px var(--border-error); }
</style>
