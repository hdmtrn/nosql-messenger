<script setup>
import { ref, useId } from 'vue'

defineProps({
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
</script>

<template>
  <div class="sg-input">
    <label v-if="label" :for="inputId" class="mono-line label">
      {{ label }}
    </label>

    <div class="box" :class="[size, { focused, invalid: !!error, disabled }]">
      <input
        :id="inputId"
        :type="type"
        :disabled="disabled"
        :value="modelValue"
        class="input"
        :class="{ mono }"
        @input="emit('update:modelValue', $event.target.value)"
        @focus="focused = true"
        @blur="focused = false"
      >
      <button
        v-if="action"
        type="button"
        class="mono-line action"
        @click="emit('action')"
      >{{ action }}</button>
    </div>

    <span v-if="error" class="mono-line error">{{ error }}</span>
    <span v-else-if="hint" class="mono-line hint">{{ hint }}</span>
  </div>
</template>

<style scoped>
.sg-input {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.mono-line {
  font: var(--text-meta);
  text-transform: uppercase;
  letter-spacing: var(--mono-tracking);
}
.label {
  font: var(--text-label);
  color: var(--text-primary);
}
.box {
  display: flex;
  align-items: center;
  gap: 12px;
  height: 44px;
  padding: 0 20px;
  background: var(--surface-sunken);
  border-radius: var(--radius-pill);
}
.box.sm { height: 36px; }
.box.lg { height: 48px; }
.box.focused { box-shadow: inset 0 0 0 2px var(--border-active); }
.box.invalid { box-shadow: inset 0 0 0 2px var(--border-error); }
.box.disabled { opacity: 0.55; }
.input {
  flex: 1;
  min-width: 0;
  border: none;
  outline: none;
  background: transparent;
  color: var(--text-primary);
  font: var(--text-body);
  letter-spacing: 0;
}
.input.mono {
  font: var(--text-machine-11);
  text-transform: uppercase;
  letter-spacing: var(--mono-tracking);
}
.action {
  background: none;
  border: none;
  padding: 0;
  cursor: pointer;
  color: var(--text-primary);
  text-decoration: underline;
}
.error { color: var(--status-error); }
.hint { color: var(--text-muted); }
</style>
