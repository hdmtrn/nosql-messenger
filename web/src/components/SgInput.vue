<script setup>
import { computed, ref, useId } from 'vue'

const props = defineProps({
  modelValue: { type: String, default: '' },
  label: String,
  hint: String,
  error: String,
  action: String,
  type: { type: String, default: 'text' },
  mono: Boolean,
  size: { type: String, default: 'md' },
  disabled: Boolean,
})
const emit = defineEmits(['update:modelValue', 'action'])

const inputId = useId()
const focused = ref(false)

const monoLine = {
  font: 'var(--text-machine)',
  textTransform: 'uppercase',
  letterSpacing: 'var(--mono-tracking)',
}

const fieldStyle = computed(() => ({
  display: 'flex',
  alignItems: 'center',
  gap: '12px',
  height: props.size === 'sm' ? '36px' : props.size === 'lg' ? '48px' : '44px',
  padding: '0 20px',
  background: 'var(--surface-sunken)',
  borderRadius: 'var(--radius-pill)',
  boxShadow: props.error
    ? 'inset 0 0 0 2px var(--border-error)'
    : focused.value
      ? 'inset 0 0 0 2px var(--border-active)'
      : 'none',
  opacity: props.disabled ? 0.55 : 1,
}))

const inputStyle = computed(() => ({
  flex: 1,
  minWidth: 0,
  border: 'none',
  outline: 'none',
  background: 'transparent',
  color: 'var(--text-primary)',
  font: props.mono ? 'var(--text-machine-11)' : 'var(--text-body)',
  textTransform: props.mono ? 'uppercase' : 'none',
  letterSpacing: props.mono ? 'var(--mono-tracking)' : '0',
}))
</script>

<template>
  <div style="display:flex;flex-direction:column;gap:8px">
    <label v-if="label" :for="inputId" :style="{ ...monoLine, color: 'var(--text-primary)' }">
      {{ label }}
    </label>

    <div :style="fieldStyle">
      <input
        :id="inputId"
        :type="type"
        :disabled="disabled"
        :value="modelValue"
        :style="inputStyle"
        @input="emit('update:modelValue', $event.target.value)"
        @focus="focused = true"
        @blur="focused = false"
      >
      <button
        v-if="action"
        type="button"
        :style="{ ...monoLine, background: 'none', border: 'none', padding: 0,
                  cursor: 'pointer', color: 'var(--text-primary)', textDecoration: 'underline' }"
        @click="emit('action')"
      >{{ action }}</button>
    </div>

    <span v-if="error" :style="{ ...monoLine, color: 'var(--status-error)' }">{{ error }}</span>
    <span v-else-if="hint" :style="{ ...monoLine, color: 'var(--text-muted)' }">{{ hint }}</span>
  </div>
</template>
