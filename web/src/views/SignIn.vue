<script setup>
import { ref } from 'vue'
import { api } from '../api'
import SgButton from '../components/SgButton.vue'
import SgInput from '../components/SgInput.vue'
import ChannelGlyph from '../components/ChannelGlyph.vue'

const emit = defineEmits(['signed-in'])

const username = ref('')
const password = ref('')
const error = ref('')
const busy = ref(false)
const reveal = ref(false)

async function submit(action) {
  error.value = ''
  busy.value = true
  try {
    await api[action](username.value, password.value)
    emit('signed-in', await api.me())
  } catch (e) {
    error.value = `${e.message} (${e.status})`
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div style="height:100vh;display:flex;gap:16px;padding:16px;
              background:var(--surface-page);box-sizing:border-box">

    <section style="flex:0 0 44%;background:var(--surface-accent);
                    border-radius:var(--radius-panel);padding:32px;
                    display:flex;flex-direction:column">
      <span style="font:700 30px/1.1 var(--font-ui);letter-spacing:-0.015em;color:#fff">
        Messenger
      </span>
      <div style="flex:1;display:grid;place-items:center">
        <ChannelGlyph :size="190" tone="onBlue" />
      </div>
    </section>

    <section style="flex:1;background:var(--surface-panel);
                    border-radius:var(--radius-panel);display:grid;
                    place-items:center;padding:32px">
      <form
        style="width:380px;display:flex;flex-direction:column;gap:24px"
        @submit.prevent="submit('login')"
      >
        <h1 style="margin:0;font:700 34px/1.1 var(--font-ui);letter-spacing:-0.02em">Sign in</h1>

        <SgInput v-model="username" label="Username" hint="3-32 characters" />
        <SgInput
          v-model="password"
          label="Password"
          :type="reveal ? 'text' : 'password'"
          :action="reveal ? 'Hide' : 'Show'"
          :error="error"
          @action="reveal = !reveal"
        />

        <div style="display:flex;gap:12px;margin-top:8px">
          <SgButton variant="primary" size="lg" type="submit" :disabled="busy">Sign in</SgButton>
          <SgButton variant="outline" size="lg" :disabled="busy" @click="submit('register')">
            Register
          </SgButton>
        </div>
      </form>
    </section>
  </div>
</template>
