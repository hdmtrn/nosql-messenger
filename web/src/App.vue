<script setup>
import { onMounted, ref } from 'vue'
import { api } from './api'
import SignIn from './views/SignIn.vue'
import SgButton from './components/SgButton.vue'

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
  <div v-else style="padding:32px">
    <p>Signed in as <b>{{ me.username }}</b></p>
    <SgButton variant="outline" @click="signOut">Sign out</SgButton>
  </div>
</template>
