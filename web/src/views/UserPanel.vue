<script setup>
import { computed, ref, watch } from 'vue'
import { api } from '../api'
import ChannelGlyph from '../components/ChannelGlyph.vue'
import SgAvatar from '../components/SgAvatar.vue'
import SgButton from '../components/SgButton.vue'
import MediaViewer from '../components/MediaViewer.vue'
import { DELETED_NAME, avatarUrl, channelAvatarUrl, initials } from '../naming'

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

// The author of messages whose account is gone: nothing to show, nothing to offer.
const gone = computed(() => !!person.value?.deleted)
const name = computed(() => (gone.value ? DELETED_NAME : person.value ? person.value.display_name : props.username))

</script>

<template>
  <aside class="user-panel">
    <div class="top">
      <h2 class="heading">About</h2>
      <SgButton variant="outline" size="sm" @click="emit('close')">Close</SgButton>
    </div>

    <!-- laid out like your own profile card: avatar on the left, everything flush left -->
    <div class="card person">
      <div class="identity">
        <button type="button" class="avatar" :disabled="!avatarUrl(username)"
                :aria-label="avatarUrl(username) ? 'Open photo' : undefined" @click="viewing = true">
          <SgAvatar :initials="initials(name)" :src="avatarUrl(username)" :size="56" />
        </button>
        <MediaViewer v-if="viewing && avatarUrl(username)" :pictures="[{ key: username, url: avatarUrl(username) }]"
                     @close="viewing = false" />
        <div class="names">
          <div class="name">{{ name }}</div>
          <div v-if="!gone" class="handle">@{{ username }}</div>
        </div>
      </div>

      <p v-if="person && person.bio" class="bio">{{ person.bio }}</p>

      <p v-if="missing" class="label note">User not found</p>
      <p v-else-if="gone" class="label note">This account was deleted</p>

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
      <span class="label section">Channels in common</span>
      <div class="card list">
        <p v-if="!common.length" class="row none">None</p>
        <button v-for="c in common" :key="c.id" type="button" class="row channel"
                @click="emit('select', c.id)">
          <ChannelGlyph :src="channelAvatarUrl(c)" :size="22" tone="blue" />
          <span class="channel-name">{{ c.name }}</span>
        </button>
      </div>
    </template>
  </aside>
</template>

<style scoped>
.user-panel {
  flex: 0 0 340px;
  display: flex;
  flex-direction: column;
  gap: 16px;
  overflow-y: auto;
}
.top {
  display: flex;
  align-items: center;
  gap: 12px;
}
.heading {
  margin: 0;
  flex: 1;
  font: 600 20px/1.2 var(--font-ui);
}
.card {
  background: var(--surface-panel);
  border-radius: var(--radius-panel);
}
.person {
  padding: 24px;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 16px;
}
.identity {
  display: flex;
  align-items: center;
  gap: 16px;
  min-width: 0;
  max-width: 100%;
}
.names { min-width: 0; }
.name {
  font: 600 20px/1.2 var(--font-ui);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.handle {
  margin-top: 4px;
  font: var(--text-meta);
  color: var(--grey);
}
.bio {
  margin: 0;
  font: var(--text-body);
  color: var(--text-muted);
}
.label {
  font: var(--text-label);
  color: var(--grey);
}
.note { margin: 0; }
.section { padding: 0 24px; }
.list { padding: 4px 0; }
.row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 0 24px;
  height: 52px;
}
.none {
  margin: 0;
  color: var(--text-muted);
}
.channel {
  width: 100%;
  border: none;
  background: none;
  cursor: pointer;
  font: var(--text-body);
}
.channel-name {
  flex: 1;
  text-align: left;
}
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
