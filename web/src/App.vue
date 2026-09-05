<script setup>
import { onMounted, ref } from 'vue'
import { api } from './api'
import SignIn from './views/SignIn.vue'
import Messenger from './views/Messenger.vue'

const me = ref(null)
const ready = ref(false)

onMounted(async () => {
  try {
    me.value = await api.me()
  } catch {
    me.value = null
  }
  ready.value = true
})

async function signOut() {
  await api.logout()
  me.value = null
}
</script>

<template>
  <div v-if="!ready" />
  <SignIn v-else-if="!me" @signed-in="me = $event" />
  <Messenger v-else @log-out="signOut" />
</template>
