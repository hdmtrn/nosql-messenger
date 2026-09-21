<script setup>
import { computed } from 'vue'
import SgAvatar from './SgAvatar.vue'
import SgButton from './SgButton.vue'
import SgSpinner from './SgSpinner.vue'

const props = defineProps({
  own: Boolean,
  status: { type: String, default: 'delivered' },
  author: String,
  initials: String,
  avatarSrc: { type: String, default: '' },
  time: String,
  // First message of a run by the same author: it carries the avatar and the
  // header. The rest of the run is bare bubbles under it.
  head: { type: Boolean, default: true },
  // A direct conversation has two people and the side already says who wrote a
  // message, so it goes without avatars and gives their width to the text.
  avatar: { type: Boolean, default: true },
  // The author whose page is open beside the feed gets a ring, so it stays clear
  // whose page that is.
  ring: Boolean,
  // Display name of the original author when this message is a forward.
  forwarded: { type: String, default: '' },
  // Quote of the message this one answers: { author, text }, or { missing: true }
  // when the original could not be loaded.
  quote: { type: Object, default: null },
  // [{ id, width, height, preview? }]: preview is the local copy shown while sending.
  attachments: { type: Array, default: () => [] },
})
defineEmits(['retry', 'discard', 'author', 'quote', 'forwarded-author', 'picture'])

const MONO = {
  font: 'var(--text-meta)',
  textTransform: 'uppercase',
  letterSpacing: 'var(--mono-tracking)',
}

const AVATAR = 36

const shells = computed(() => ({
  delivered: props.own
    ? { background: 'var(--surface-accent)', color: '#fff' }
    : { background: 'var(--surface-sunken)', color: 'var(--text-primary)' },
  sending: { background: 'var(--surface-panel)', color: 'var(--text-primary)', boxShadow: 'inset 0 0 0 2px var(--border-active)' },
  failed: { background: 'var(--surface-panel)', color: 'var(--text-primary)', boxShadow: 'inset 0 0 0 2px var(--border-error)' },
}))

const stateLabel = computed(() =>
  props.status === 'sending' ? 'Sending' : props.status === 'failed' ? 'Not sent' : null
)
// The header is now down to the author's name, so it exists only on the first
// message of a run in a group channel — or to explain a delivery problem.
const showHeader = computed(() => (props.head && !!props.author) || stateLabel.value !== null)
const labelStyle = computed(() => ({
  ...MONO,
  color: props.status === 'failed' ? 'var(--status-error)' : 'var(--text-muted)',
}))

const showStamp = computed(() => props.status === 'sending' || !!props.time)
// The clock rides on the last line of the text, Telegram-style: an invisible copy
// at the end of the text reserves room on that line, and the visible clock is
// pinned into the bubble's corner over it. A long message keeps its full width on
// every other line; if the last line is full, the room wraps onto a line of its own.
const stampRoom = {
  ...MONO,
  display: 'inline-flex',
  marginLeft: '8px',
  visibility: 'hidden',
}
// Low contrast on purpose: the clock rides along with every message, so it has
// to stay readable without competing with the text next to it.
const stampStyle = computed(() => ({
  ...MONO,
  display: 'inline-flex',
  position: 'absolute',
  right: '12px',
  // 11, not the 8px padding: the room sits on the text's baseline, 3px above the line box bottom
  bottom: '11px',
  color: props.own ? 'rgba(255, 255, 255, 0.7)' : 'var(--text-muted)',
}))
const forwardStyle = computed(() => ({
  display: 'block',
  marginBottom: '2px',
  font: '500 13px/1.3 var(--font-ui)',
  color: props.own && props.status === 'delivered' ? 'rgba(255, 255, 255, 0.7)' : 'var(--text-muted)',
}))
// On a blue bubble the quote is a lighter band of the same blue; on a grey one it
// is white, as on the mockup.
const quoteStyle = computed(() => {
  const onBlue = props.own && props.status === 'delivered'
  return {
    display: 'flex',
    gap: '10px',
    margin: '2px 0 6px',
    padding: '6px 10px',
    borderRadius: '10px',
    background: onBlue ? 'rgba(255, 255, 255, 0.16)' : 'var(--surface-panel)',
    '--quote-accent': onBlue ? '#fff' : 'var(--blue)',
    '--quote-muted': onBlue ? 'rgba(255, 255, 255, 0.75)' : 'var(--text-muted)',
  }
})
// One picture keeps its proportions inside a 280px box; several go into a grid of
// squares, and their size is the grid's business — a tile has to be able to
// shrink with the bubble. Both paths state the proportions rather than wait for
// the file, so the feed does not jump when it arrives.
const PICTURE = 280
function pictureStyle(a) {
  if (props.attachments.length > 1) return { aspectRatio: '1 / 1' }
  const scale = Math.min(1, PICTURE / a.width, PICTURE / a.height)
  return { width: Math.round(a.width * scale) + 'px', aspectRatio: `${a.width} / ${a.height}` }
}
const pictureUrl = (a) => a.preview || `/media/${a.id}`

const avatarTone = computed(() =>
  props.own && props.status !== 'delivered' ? props.status : 'blue'
)
// The flat corner is the tail pointing at the avatar, so only the head has one.
const corners = computed(() => {
  const r = 'var(--radius-bubble)'
  if (!props.head || !props.avatar) return r
  return props.own ? `${r} 0 ${r} ${r}` : `0 ${r} ${r} ${r}`
})
const bubbleStyle = computed(() => ({
  position: 'relative',
  // ~65 characters per line is the readable measure and caps the bubble on a wide
  // pane; on a narrow one the 85% leaves the other side a visible margin without
  // wrapping short messages early
  maxWidth: 'min(65ch, 85%)',
  padding: '8px 12px',
  borderRadius: corners.value,
  font: 'var(--text-body)',
  // a word longer than the bubble (a link, a code) breaks instead of pushing the
  // bubble past the edge of the pane
  overflowWrap: 'anywhere',
  ...shells.value[props.status],
}))
</script>

<template>
  <div :style="{ display: 'flex', gap: '12px', alignItems: 'flex-start',
                 flexDirection: own ? 'row-reverse' : 'row' }">
    <template v-if="avatar">
      <!-- someone else's avatar and name open their page; your own lead nowhere -->
      <component :is="own ? 'span' : 'button'" v-if="head" :type="own ? undefined : 'button'"
                 class="who" :class="{ ring }" :aria-label="own ? undefined : 'Open profile'"
                 @click="own || $emit('author')">
        <SgAvatar :initials="initials" :src="avatarSrc" :size="AVATAR" :tone="avatarTone" />
      </component>
      <!-- keeps the bubbles of a run flush with the head above them -->
      <div v-else :style="{ flex: `0 0 ${AVATAR}px` }" />
    </template>

    <!-- flex: 1 gives the column the full row width, so the bubble's 70% is of the pane, not of itself -->
    <div :style="{ flex: 1, display: 'flex', flexDirection: 'column',
                   alignItems: own ? 'flex-end' : 'flex-start', gap: '4px', minWidth: 0 }">
      <div v-if="showHeader" style="display:flex;gap:8px;align-items:baseline">
        <component :is="own ? 'span' : 'button'" v-if="head && author" :type="own ? undefined : 'button'"
                   class="who name" @click="own || $emit('author')">{{ author }}</component>
        <span v-if="stateLabel" :style="labelStyle">{{ stateLabel }}</span>
      </div>

      <div :style="bubbleStyle">
        <!-- a line of its own above the text, so the clock still rides on the text's last line -->
        <!-- a quote leads to its original; one that could not be loaded leads nowhere -->
        <button v-if="quote" type="button" class="quote" :style="quoteStyle"
                :disabled="quote.missing" @click="$emit('quote')">
          <span class="quote-bar" />
          <span style="display:flex;flex-direction:column;min-width:0">
            <template v-if="quote.missing">
              <span class="quote-text">Message not available</span>
            </template>
            <template v-else>
              <span class="quote-author">{{ quote.author }}</span>
              <span class="quote-text">{{ quote.text }}</span>
            </template>
          </span>
        </button>
        <span v-if="forwarded" :style="forwardStyle">Forwarded from
          <button type="button" class="source" @click="$emit('forwarded-author')">{{ forwarded }}</button>
        </span>
        <div v-if="attachments.length" class="pictures"
             :class="{ grid: attachments.length > 1 }">
          <button v-for="(a, i) in attachments" :key="a.id" type="button" class="picture-open"
                  aria-label="Open picture" @click="$emit('picture', i)">
            <img :src="pictureUrl(a)" alt="" loading="lazy" class="picture" :style="pictureStyle(a)">
          </button>
        </div>
        <slot />
        <template v-if="showStamp">
          <span aria-hidden="true" :style="stampRoom">
            <SgSpinner v-if="status === 'sending'" />
            <template v-else>{{ time }}</template>
          </span>
          <span :style="stampStyle">
            <SgSpinner v-if="status === 'sending'" />
            <template v-else>{{ time }}</template>
          </span>
        </template>
      </div>

      <div v-if="status === 'failed'" style="display:flex;align-items:center;gap:12px">
        <SgButton variant="danger" size="sm" @click="$emit('retry')">Retry</SgButton>
        <SgButton variant="mutedText" @click="$emit('discard')">Discard</SgButton>
      </div>
    </div>
  </div>
</template>

<style scoped>
.who {
  display: flex;
  flex: none;
  padding: 0;
  border: none;
  border-radius: var(--radius-pill);
  background: none;
  color: inherit;
}
button.who { cursor: pointer; }
.name { font: var(--text-name); }
.pictures {
  display: flex;
  margin: 4px -4px 4px;
}
.pictures.grid {
  display: grid;
  /* A tile is at most 136px and may be less. Neither half is optional: an auto
     track is stretched by the default content distribution, which drove the two
     columns apart and left a hole where the fourth tile of an odd set would go,
     while a track that cannot shrink hangs outside the bubble — the bubble is
     only as wide as the text under the pictures, and on a phone that is narrower
     than two tiles. */
  grid-template-columns: repeat(2, minmax(0, 136px));
  justify-content: start;
  gap: 4px;
}
.pictures.grid .picture {
  width: 100%;
  height: auto;
}
.picture-open {
  display: block;
  max-width: 100%;
  padding: 0;
  border: none;
  background: none;
  cursor: zoom-in;
}
.picture {
  display: block;
  max-width: 100%;
  object-fit: cover;
  border-radius: 12px;
  background: var(--surface-panel);
}
.quote {
  width: 100%;
  border: none;
  text-align: left;
  cursor: pointer;
}
.quote:disabled { cursor: default; }
.source {
  padding: 0;
  border: none;
  background: none;
  color: inherit;
  font: inherit;
  cursor: pointer;
}
.source:hover { text-decoration: underline; }
.quote-bar {
  flex: none;
  width: 2px;
  border-radius: 2px;
  background: var(--quote-accent);
}
.quote-author {
  font: 500 13px/1.3 var(--font-ui);
  color: var(--quote-accent);
}
/* one line, cut with an ellipsis: the quote only has to say which message it is */
.quote-text {
  font: 400 14px/1.35 var(--font-ui);
  color: var(--quote-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
button.name:hover { text-decoration: underline; }
/* the paper-coloured gap keeps the ring from merging into a blue avatar */
.ring { box-shadow: 0 0 0 2px var(--surface-panel), 0 0 0 4px var(--blue); }
</style>
