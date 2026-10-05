<script setup>
defineProps({
  variant: { type: String, default: 'primary' },
  size: { type: String, default: 'md' },
  disabled: Boolean,
  fullWidth: Boolean,
  mono: { type: Boolean, default: true },
  onBlue: Boolean,
  type: { type: String, default: 'button' },
})
</script>

<template>
  <button :type="type" :disabled="disabled" class="sg-button"
          :class="[`sg-button--${size}`, `sg-button--${variant}`,
                   { 'sg-button--mono': mono, 'sg-button--on-blue': onBlue, 'sg-button--full': fullWidth }]"><slot /></button>
</template>

<style scoped>
/* Every class here sits on the root, which the parent's scoped styles reach too,
   so they all carry the component's name. */
.sg-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  border: none;
  border-radius: var(--radius-pill);
  cursor: pointer;
  font: var(--text-ui);
  letter-spacing: 0;
  white-space: nowrap;
}
.sg-button:disabled { cursor: not-allowed; }
.sg-button--mono {
  font: var(--text-label);
  text-transform: uppercase;
  letter-spacing: var(--mono-tracking);
}
.sg-button--mono.sg-button--sm { font: var(--text-machine); }
.sg-button--full { width: 100%; }

.sg-button--sm { height: 26px; padding: 0 12px; }
.sg-button--md { height: 36px; padding: 0 18px; }
.sg-button--lg { height: 48px; padding: 0 26px; }

.sg-button--primary { background: var(--surface-accent); color: #fff; }
.sg-button--outline { background: transparent; color: var(--text-primary); box-shadow: inset 0 0 0 1px var(--ink); }
.sg-button--outline.sg-button--on-blue { color: #fff; box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.7); }
.sg-button--ghost { background: transparent; color: var(--text-primary); }
.sg-button--ghost.sg-button--on-blue { color: #fff; }
.sg-button--danger { background: var(--status-error); color: #fff; }
.sg-button.sg-button--mutedText { background: transparent; color: var(--text-muted); padding: 0; height: auto; }

.sg-button--primary:disabled,
.sg-button--danger:disabled { background: var(--text-muted); color: #fff; box-shadow: none; }
.sg-button--outline:disabled,
.sg-button--ghost:disabled,
.sg-button.sg-button--mutedText:disabled { background: transparent; color: var(--text-disabled); box-shadow: none; }
.sg-button--outline:disabled { box-shadow: inset 0 0 0 1px var(--border-muted); }
</style>
