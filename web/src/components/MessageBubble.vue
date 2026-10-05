<script setup>
import { computed } from 'vue'
import SgAvatar from './SgAvatar.vue'
import SgButton from './SgButton.vue'
import SgSpinner from './SgSpinner.vue'
import { albumLayout } from '../album'

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

const AVATAR = 36

const stateLabel = computed(() =>
  props.status === 'sending' ? 'Sending' : props.status === 'failed' ? 'Not sent' : null
)
// The header is now down to the author's name, so it exists only on the first
// message of a run in a group channel — or to explain a delivery problem.
const showHeader = computed(() => (props.head && !!props.author) || stateLabel.value !== null)

const showStamp = computed(() => props.status === 'sending' || !!props.time)

// One picture keeps its proportions inside a 280px box. Several become an album
// laid out by their proportions, the way Telegram does it. Both state the shape
// before the files arrive, so the feed does not jump when they do.
const PICTURE = 280
const pictureWidth = (a) =>
  Math.round(a.width * Math.min(1, PICTURE / a.width, PICTURE / a.height))

function pictureVars(a) {
  return { '--width': pictureWidth(a) + 'px', '--ratio': `${a.width} / ${a.height}` }
}

const album = computed(() =>
  props.attachments.length > 1 ? albumLayout(props.attachments) : null
)

// The layout comes back in the coordinates of a 280px album; here it turns into
// percentages, and that is the whole of the responsiveness — the album keeps its
// proportions and shrinks with the bubble, with nothing measured at runtime.
const percent = (value, total) => ((100 * value) / total).toFixed(3) + '%'

// Only the corners of the album itself are round, as in a real album. The inner
// ones are not square as Telegram leaves them: our gap is wider, and a hairline
// takes the sharpness off without reading as a separate tile.
const ALBUM_RADIUS = 12
const SEAM_RADIUS = 2

function tileVars(tile) {
  const { width, height } = album.value
  return {
    '--x': percent(tile.x, width),
    '--y': percent(tile.y, height),
    '--w': percent(tile.w, width),
    '--h': percent(tile.h, height),
    '--radius': tile.corners.map((c) => (c ? ALBUM_RADIUS : SEAM_RADIUS) + 'px').join(' '),
  }
}
const pictureUrl = (a) => a.preview || `/media/${a.id}`

const avatarTone = computed(() =>
  props.own && props.status !== 'delivered' ? props.status : 'blue'
)
// Pictures decide how wide the bubble is, and the text under them wraps inside
// that width — the rule every messenger follows. The other way round, a long
// caption stretched the bubble and left the album sitting in the corner of it.
const mediaWidth = computed(() => {
  if (album.value) return album.value.width
  return props.attachments.length === 1 ? pictureWidth(props.attachments[0]) : 0
})

const PADDING_X = 12

const bubbleClass = computed(() => [props.status, {
  own: props.own,
  // a blue bubble: quotes, forwards and the clock are drawn in white on it
  'on-blue': props.own && props.status === 'delivered',
  media: mediaWidth.value > 0,
  // The flat corner is the tail pointing at the avatar, so only the head has one.
  tail: props.head && props.avatar,
}])
// the pictures' own width plus the padding they sit in, since the box is border-box
const bubbleVars = computed(() =>
  mediaWidth.value ? { '--media-width': mediaWidth.value + 2 * PADDING_X + 'px' } : null
)
</script>

<template>
  <div class="message-bubble" :class="{ 'message-bubble--own': own }">
    <template v-if="avatar">
      <!-- someone else's avatar and name open their page; your own lead nowhere -->
      <component :is="own ? 'span' : 'button'" v-if="head" :type="own ? undefined : 'button'"
                 class="who" :class="{ ring }" :aria-label="own ? undefined : 'Open profile'"
                 @click="own || $emit('author')">
        <SgAvatar :initials="initials" :src="avatarSrc" :size="AVATAR" :tone="avatarTone" />
      </component>
      <div v-else class="spacer" />
    </template>

    <div class="column">
      <div v-if="showHeader" class="header">
        <component :is="own ? 'span' : 'button'" v-if="head && author" :type="own ? undefined : 'button'"
                   class="who name" @click="own || $emit('author')">{{ author }}</component>
        <span v-if="stateLabel" class="mono state" :class="status">{{ stateLabel }}</span>
      </div>

      <div class="bubble" :class="bubbleClass" :style="bubbleVars">
        <!-- a line of its own above the text, so the clock still rides on the text's last line -->
        <!-- a quote leads to its original; one that could not be loaded leads nowhere -->
        <button v-if="quote" type="button" class="quote"
                :disabled="quote.missing" @click="$emit('quote')">
          <span class="quote-bar" />
          <span class="quote-body">
            <template v-if="quote.missing">
              <span class="quote-text">Message not available</span>
            </template>
            <template v-else>
              <span class="quote-author">{{ quote.author }}</span>
              <span class="quote-text">{{ quote.text }}</span>
            </template>
          </span>
        </button>
        <span v-if="forwarded" class="forwarded">Forwarded from
          <button type="button" class="source" @click="$emit('forwarded-author')">{{ forwarded }}</button>
        </span>
        <div v-if="attachments.length" class="pictures" :class="{ album: !!album }"
             :style="album ? { '--album-width': album.width + 'px', '--album-ratio': `${album.width} / ${album.height}` } : null">
          <button v-for="(a, i) in attachments" :key="a.id" type="button" class="picture-open"
                  :style="album ? tileVars(album.tiles[i]) : null"
                  aria-label="Open picture" @click="$emit('picture', i)">
            <img :src="pictureUrl(a)" alt="" loading="lazy" class="picture"
                 :class="{ single: !album }" :style="album ? null : pictureVars(a)">
          </button>
        </div>
        <slot />
        <template v-if="showStamp">
          <span aria-hidden="true" class="mono stamp-room">
            <SgSpinner v-if="status === 'sending'" />
            <template v-else>{{ time }}</template>
          </span>
          <span class="mono stamp">
            <SgSpinner v-if="status === 'sending'" />
            <template v-else>{{ time }}</template>
          </span>
        </template>
      </div>

      <div v-if="status === 'failed'" class="failed-actions">
        <SgButton variant="danger" size="sm" @click="$emit('retry')">Retry</SgButton>
        <SgButton variant="mutedText" @click="$emit('discard')">Discard</SgButton>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* The root's classes carry the component's name: the parent's scoped styles reach it. */
.message-bubble {
  display: flex;
  gap: 12px;
  align-items: flex-start;
}
.message-bubble--own { flex-direction: row-reverse; }
/* keeps the bubbles of a run flush with the head above them: the avatar's 36px */
.spacer { flex: 0 0 36px; }
/* flex: 1 gives the column the full row width, so the bubble's 70% is of the pane, not of itself */
.column {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
  min-width: 0;
}
.message-bubble--own .column { align-items: flex-end; }
.header {
  display: flex;
  gap: 8px;
  align-items: baseline;
}
.mono {
  font: var(--text-meta);
  text-transform: uppercase;
  letter-spacing: var(--mono-tracking);
}
.state { color: var(--text-muted); }
.state.failed { color: var(--status-error); }
.bubble {
  position: relative;
  /* Without pictures: ~65 characters per line is the readable measure and caps the
     bubble on a wide pane; on a narrow one the 85% leaves the other side a visible
     margin without wrapping short messages early. */
  max-width: min(65ch, 85%);
  padding: 8px 12px;
  border-radius: var(--radius-bubble);
  font: var(--text-body);
  /* a word longer than the bubble (a link, a code) breaks instead of pushing the
     bubble past the edge of the pane */
  overflow-wrap: anywhere;
}
.bubble.media { max-width: min(var(--media-width), 85%); }
.bubble.tail { border-radius: 0 var(--radius-bubble) var(--radius-bubble) var(--radius-bubble); }
.bubble.tail.own { border-radius: var(--radius-bubble) 0 var(--radius-bubble) var(--radius-bubble); }
.bubble.delivered { background: var(--surface-sunken); color: var(--text-primary); }
.bubble.delivered.own { background: var(--surface-accent); color: #fff; }
.bubble.sending,
.bubble.failed { background: var(--surface-panel); color: var(--text-primary); }
.bubble.sending { box-shadow: inset 0 0 0 2px var(--border-active); }
.bubble.failed { box-shadow: inset 0 0 0 2px var(--border-error); }
/* The clock rides on the last line of the text, Telegram-style: an invisible copy
   at the end of the text reserves room on that line, and the visible clock is
   pinned into the bubble's corner over it. A long message keeps its full width on
   every other line; if the last line is full, the room wraps onto a line of its own. */
.stamp-room {
  display: inline-flex;
  margin-left: 8px;
  visibility: hidden;
}
/* Low contrast on purpose: the clock rides along with every message, so it has
   to stay readable without competing with the text next to it. */
.stamp {
  display: inline-flex;
  position: absolute;
  right: 12px;
  /* 11, not the 8px padding: the room sits on the text's baseline, 3px above the line box bottom */
  bottom: 11px;
  color: var(--text-muted);
}
.message-bubble--own .stamp { color: rgba(255, 255, 255, 0.7); }
.forwarded {
  display: block;
  margin-bottom: 2px;
  font: 500 13px/1.3 var(--font-ui);
  color: var(--text-muted);
}
.on-blue .forwarded { color: rgba(255, 255, 255, 0.7); }
.failed-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}
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
/* The album is a box of known proportions with the tiles placed inside it in
   percentages, so it holds its shape at any width. The width is stated in pixels
   as well, so that a message of pictures alone is 280px wide rather than as wide
   as the bubble lets it be. */
.pictures.album {
  display: block;
  position: relative;
  width: var(--album-width);
  max-width: 100%;
  aspect-ratio: var(--album-ratio);
  margin: 4px 0;
}
.pictures.album .picture-open {
  position: absolute;
  left: var(--x);
  top: var(--y);
  width: var(--w);
  height: var(--h);
  border-radius: var(--radius);
}
.pictures.album .picture {
  width: 100%;
  height: 100%;
  border-radius: inherit;
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
.picture.single {
  width: var(--width);
  aspect-ratio: var(--ratio);
}
/* On a blue bubble the quote is a lighter band of the same blue; on a grey one it
   is white, as on the mockup. */
.quote {
  display: flex;
  gap: 10px;
  width: 100%;
  margin: 2px 0 6px;
  padding: 6px 10px;
  border: none;
  border-radius: 10px;
  background: var(--surface-panel);
  text-align: left;
  cursor: pointer;
  --quote-accent: var(--blue);
  --quote-muted: var(--text-muted);
}
.on-blue .quote {
  background: rgba(255, 255, 255, 0.16);
  --quote-accent: #fff;
  --quote-muted: rgba(255, 255, 255, 0.75);
}
.quote-body {
  display: flex;
  flex-direction: column;
  min-width: 0;
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
