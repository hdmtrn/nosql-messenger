<template>
  <span class="sg-spinner" />
</template>

<style scoped>
.sg-spinner {
  display: inline-block;
  flex: none;
  width: 12px;
  height: 12px;
  border: 1.5px solid var(--blue);
  border-top-color: transparent;
  border-radius: var(--radius-pill);
  animation: sg-spin 700ms linear infinite;
}
</style>
