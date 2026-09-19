<script setup>
import { onMounted, onUnmounted, ref } from 'vue'
import { api } from './api'
import SignIn from './views/SignIn.vue'
import Messenger from './views/Messenger.vue'

const me = ref(null)
const ready = ref(false)

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

// The server closed the socket because its session ended, and that socket will
// not reconnect. Messenger goes first, unconditionally, as Rocket.Chat's client
// wipes its login on force_logout: whatever the cookie holds now — nobody, or a
// newer session of the same user from another tab — then gets a fresh Messenger
// with a fresh socket. Asking first and swapping only on a different user left
// the same user with a dead socket. ready hides the gap, so the sign-in screen
// does not flash when the answer is the same person.
async function sessionEnded() {
  ready.value = false
  me.value = null
  me.value = await api.me().catch(() => null)
  ready.value = true
}

async function signOut() {
  await api.logout()
  me.value = null
}
</script>

<template>
  <div v-if="!ready" />
  <SignIn v-else-if="!me" @signed-in="me = $event" />
  <Messenger v-else :key="me.id" :me="me" @log-out="signOut" @profile-changed="reloadProfile"
             @session-ended="sessionEnded" />
</template>
