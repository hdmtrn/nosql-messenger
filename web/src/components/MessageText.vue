<script setup>
import { computed } from 'vue'
import { external, parts, inviteCode } from '../links'

const props = defineProps({
  text: { type: String, default: '' },
})
const emit = defineEmits(['invite'])

// Every link opens in this tab, ours and foreign alike: a page decides where
// its own links go, a new tab is the reader's to ask for, and the browser gives
// them that on a modified click or the context menu. What being foreign still
// decides is the referrer — see the template.
const pieces = computed(() =>
  parts(props.text).map((p) => (p.href ? { ...p, away: external(p.href) } : p))
)

// An invite of ours is a route of this application, not a page to load: the
// href stays, so the link can still be copied or opened in a new tab by hand,
// but a plain click joins in place instead of reloading the whole app.
function follow(piece, event) {
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
  const code = inviteCode(piece.href)
  if (!code) return
  event.preventDefault()
  emit('invite', code)
}
</script>

<template><template v-for="(p, i) in pieces" :key="i"><!-- noreferrer on a foreign link only: without it the address we are on travels to that site, and ours carries the id of the open channel --><a v-if="p.href" class="link" :href="p.href" :rel="p.away ? 'noreferrer' : null" @click="follow(p, $event)">{{ p.text }}</a><template v-else>{{ p.text }}</template></template></template>

<style scoped>
.link {
  color: inherit;
  text-decoration: underline;
  text-underline-offset: 2px;
  /* anywhere, not break-all: a long link first tries the next line whole and
     only then breaks, and it lets the bubble shrink below the link's width */
  overflow-wrap: anywhere;
}
</style>
