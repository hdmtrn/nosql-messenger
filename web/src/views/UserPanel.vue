<script setup>
import { ref, watch } from 'vue'
import { api } from '../api'
import ChannelGlyph from '../components/ChannelGlyph.vue'
import SgAvatar from '../components/SgAvatar.vue'
import SgButton from '../components/SgButton.vue'
import MediaViewer from '../components/MediaViewer.vue'
import { avatarUrl, initials } from '../naming'

const props = defineProps({
  username: { type: String, required: true },
  // Friendship is owned by the messenger, which already holds the lists; the
  // panel only needs to know which button to offer.
  relation: { type: String, default: 'none' }, // friend | incoming | sent | none
})
const emit = defineEmits(['close', 'message', 'befriend', 'select'])

const person = ref(null)
const missing = ref(false)
const common = ref([])
const viewing = ref(false)

// Same guard as the info panel: clicking two authors in a row leaves two loads
// in flight, and only the newest one may write.
let asked = 0

async function load() {
  const mine = ++asked
  person.value = null
  missing.value = false
  common.value = []

  const [who, shared] = await Promise.all([
    api.user(props.username).catch(() => null),
    api.channelsInCommon(props.username).catch(() => []),
  ])
  if (mine !== asked) return
  person.value = who
  missing.value = !who
  common.value = shared
}

watch(() => props.username, load, { immediate: true })

const mono = {
  font: 'var(--text-machine)',
  textTransform: 'uppercase',
  letterSpacing: 'var(--mono-tracking)',
  color: 'var(--grey)',
}
const handle = { font: 'var(--text-machine-11)', letterSpacing: 'var(--mono-tracking)', color: 'var(--grey)' }
const panel = { background: 'var(--surface-panel)', borderRadius: 'var(--radius-panel)' }
const row = { display: 'flex', alignItems: 'center', gap: '12px', padding: '0 24px', height: '52px' }
</script>

<template>
  <aside style="flex:0 0 340px;display:flex;flex-direction:column;gap:16px;overflow-y:auto">
    <div style="display:flex;align-items:center;gap:12px">
      <h2 style="margin:0;flex:1;font:600 20px/1.2 var(--font-ui)">About</h2>
      <SgButton variant="outline" size="sm" @click="emit('close')">Close</SgButton>
    </div>

    <!-- laid out like your own profile card: avatar on the left, everything flush left -->
    <div :style="panel" style="padding:24px;display:flex;flex-direction:column;
                               align-items:flex-start;gap:16px">
      <div style="display:flex;align-items:center;gap:16px;min-width:0;max-width:100%">
        <button type="button" class="avatar" :disabled="!avatarUrl(username)"
                :aria-label="avatarUrl(username) ? 'Open photo' : undefined" @click="viewing = true">
          <SgAvatar :initials="initials(person ? person.display_name : username)" :src="avatarUrl(username)" :size="56" />
        </button>
        <MediaViewer v-if="viewing && avatarUrl(username)" :pictures="[{ key: username, url: avatarUrl(username) }]"
                     @close="viewing = false" />
        <div style="min-width:0">
          <div style="font:600 20px/1.2 var(--font-ui);overflow:hidden;text-overflow:ellipsis;
                      white-space:nowrap">{{ person ? person.display_name : username }}</div>
          <div :style="handle" style="margin-top:4px">@{{ username }}</div>
        </div>
      </div>

      <p v-if="person && person.bio"
         style="margin:0;font:var(--text-body);color:var(--text-muted)">{{ person.bio }}</p>

      <p v-if="missing" :style="mono" style="margin:0">User not found</p>

      <template v-else>
        <SgButton v-if="relation === 'friend'" variant="primary"
                  @click="emit('message', username)">Send message</SgButton>
        <SgButton v-else-if="relation === 'sent'" variant="outline" disabled>Request sent</SgButton>
        <!-- an open request the other way round is accepted by sending one back -->
        <SgButton v-else variant="primary" @click="emit('befriend', username)">
          {{ relation === 'incoming' ? 'Accept friend request' : 'Add friend' }}
        </SgButton>
      </template>
    </div>

    <template v-if="!missing">
      <span :style="mono" style="padding:0 24px">Channels in common</span>
      <div :style="panel" style="padding:4px 0">
        <p v-if="!common.length" :style="row" style="margin:0;color:var(--text-muted)">None</p>
        <button v-for="c in common" :key="c.id" type="button"
                :style="{ ...row, width: '100%', border: 'none', background: 'none',
                          cursor: 'pointer', font: 'var(--text-body)' }"
                @click="emit('select', c.id)">
          <ChannelGlyph :size="22" tone="blue" />
          <span style="flex:1;text-align:left">{{ c.name }}</span>
        </button>
      </div>
    </template>
  </aside>
</template>

<style scoped>
.avatar {
  flex: none;
  padding: 0;
  border: none;
  background: none;
  border-radius: var(--radius-pill);
  cursor: zoom-in;
}
.avatar:disabled { cursor: default; }
</style>
