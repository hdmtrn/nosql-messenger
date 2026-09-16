<script setup>
import { ref } from 'vue'

const props = defineProps({
  placeholder: { type: String, default: 'Message…' },
  disabled: Boolean,
  maxLength: { type: Number, default: 4000 },
  // Something above the field (a forward) can be sent with no text of its own.
  ready: Boolean,
})
const emit = defineEmits(['send'])

const text = ref('')
const focused = ref(false)
const input = ref(null)

// Picking Reply or Forward in a menu leaves focus nowhere; the chat puts it here.
defineExpose({ focus: () => input.value?.focus() })

function send() {
  if ((!text.value.trim() && !props.ready) || props.disabled) return
  emit('send', text.value)
  text.value = ''
}
</script>

<template>
  <div style="position:relative;padding:16px 28px 20px">
    <slot />
    <div style="display:flex;align-items:center;gap:16px">
      <div
        :style="{ flex: 1, display: 'flex', alignItems: 'center', height: '48px', padding: '0 24px',
                  background: 'var(--surface-sunken)', borderRadius: 'var(--radius-pill)',
                  boxShadow: focused ? 'inset 0 0 0 2px var(--border-active)' : 'none' }"
      >
        <input
          ref="input"
          v-model="text"
          :disabled="disabled"
          :placeholder="placeholder"
          :maxlength="maxLength"
          style="flex:1;min-width:0;border:none;outline:none;background:transparent;
                 font:var(--text-body);color:var(--text-primary)"
          @focus="focused = true"
          @blur="focused = false"
          @keydown.enter.exact.prevent="send"
        >
      </div>
      <button
        type="button"
        :disabled="disabled"
        :style="{ height: '48px', padding: '0 26px', border: 'none',
                  cursor: disabled ? 'not-allowed' : 'pointer', borderRadius: 'var(--radius-pill)',
                  background: disabled ? 'var(--text-muted)' : 'var(--surface-accent)', color: '#fff',
                  font: 'var(--text-machine-11)', textTransform: 'uppercase',
                  letterSpacing: 'var(--mono-tracking)' }"
        @click="send"
      >Send</button>
    </div>
  </div>
</template>
