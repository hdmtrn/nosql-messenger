<script setup>
import { onMounted, onUnmounted, ref } from 'vue'
import { api } from './api'
import SignIn from './views/SignIn.vue'
import Messenger from './views/Messenger.vue'

const me = ref(null)
const ready = ref(false)

// Also what a revoked session ends in: the cookie may already hold a newer
// session (a sign-in in another tab), so this asks who we are rather than
// assume nobody — nobody means the sign-in screen, somebody their messenger.
async function refreshSession() {
  const who = await api.me().catch(() => null)
  if (who && me.value && who.id === me.value.id) return
  me.value = who
}

// The profile changed in this window, so re-read it even though the user is the same.
async function reloadProfile() {
  me.value = await api.me().catch(() => null)
}

// The session lives in a cookie, which belongs to the browser profile rather than
// to this window: signing in elsewhere replaces it under us. Re-check on focus so
// the header never claims an identity the server no longer agrees with.
function onFocus() {
  if (document.visibilityState === 'visible') refreshSession()
}

onMounted(async () => {
  await refreshSession()
  ready.value = true
  window.addEventListener('focus', onFocus)
  document.addEventListener('visibilitychange', onFocus)
})

onUnmounted(() => {
  window.removeEventListener('focus', onFocus)
  document.removeEventListener('visibilitychange', onFocus)
})

async function signOut() {
  await api.logout()
  me.value = null
}
</script>

<template>
  <div v-if="!ready" />
  <SignIn v-else-if="!me" @signed-in="me = $event" />
  <Messenger v-else :key="me.id" :me="me" @log-out="signOut" @profile-changed="reloadProfile"
             @session-ended="refreshSession" />
</template>
