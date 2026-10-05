<script setup>
import { computed, ref } from 'vue'
import { api } from '../api'
import SgButton from '../components/SgButton.vue'
import SgInput from '../components/SgInput.vue'
import ChannelGlyph from '../components/ChannelGlyph.vue'

const emit = defineEmits(['signed-in'])

const mode = ref('sign-in')
const username = ref('')
const password = ref('')
const repeat = ref('')
const reveal = ref(false)
const busy = ref(false)

// Errors land on the field they are about: a taken name belongs to the name,
// a rejected credential to the password.
const usernameError = ref('')
const passwordError = ref('')

const registering = computed(() => mode.value === 'register')

function switchTo(next) {
  mode.value = next
  usernameError.value = ''
  passwordError.value = ''
  repeat.value = ''
}

// Route by status, not by wording: "invalid username or password" names both
// fields and belongs to neither in particular — it is the credential that failed.
function place(message, status, retryAfter) {
  if (status === 429) {
    passwordError.value = `Too many attempts, try again in ${waitText(retryAfter)}`
    return
  }
  const text = `${message} (${status})`
  if (status === 409 || /^username/i.test(message)) usernameError.value = text
  else passwordError.value = text
}

function waitText(seconds) {
  return seconds < 60 ? `${seconds} s` : `${Math.ceil(seconds / 60)} min`
}

async function submit() {
  usernameError.value = ''
  passwordError.value = ''

  if (registering.value && password.value !== repeat.value) {
    passwordError.value = 'Passwords do not match'
    return
  }

  busy.value = true
  try {
    await api[registering.value ? 'register' : 'login'](username.value, password.value)
    emit('signed-in', await api.me())
  } catch (e) {
    place(e.message, e.status, e.retryAfter)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="screen">

    <section class="brand">
      <!-- the brand is a mark, not a heading: it must not compete with "Sign in" -->
      <span class="brand-name">
        Messenger
      </span>
      <div class="brand-mark">
        <ChannelGlyph :size="190" tone="onBlue" />
      </div>
    </section>

    <section class="pane">
      <form class="form" @submit.prevent="submit">
        <!-- 24px gap + 8px margin: the title sits further from the form than the fields sit from each other -->
        <h1 class="heading">
          {{ registering ? 'Register' : 'Sign in' }}
        </h1>

        <!-- lg: the fields match the 48px button below them -->
        <SgInput
          v-model="username"
          size="lg"
          label="Username"
          :hint="registering ? '3-32 letters, digits, _ or -' : ''"
          :error="usernameError"
        />

        <SgInput
          v-model="password"
          size="lg"
          label="Password"
          :type="reveal ? 'text' : 'password'"
          :action="reveal ? 'Hide' : 'Show'"
          :hint="registering ? '8 characters minimum' : ''"
          :error="passwordError"
          @action="reveal = !reveal"
        />

        <SgInput v-if="registering" v-model="repeat" size="lg" label="Repeat password" type="password" />

        <div class="submit">
          <SgButton variant="primary" size="lg" type="submit" :disabled="busy">
            {{ registering ? 'Register' : 'Sign in' }}
          </SgButton>

          <span class="switch">
            <template v-if="registering">
              Have an account?
              <a href="#" class="link" @click.prevent="switchTo('sign-in')">Sign in</a>
            </template>
            <template v-else>
              No account?
              <a href="#" class="link" @click.prevent="switchTo('register')">Register</a>
            </template>
          </span>
        </div>
      </form>
    </section>
  </div>
</template>

<style scoped>
.screen {
  height: 100vh;
  display: flex;
  gap: 16px;
  padding: 16px;
  background: var(--surface-page);
  box-sizing: border-box;
}
.brand {
  flex: 0 0 44%;
  background: var(--surface-accent);
  border-radius: var(--radius-panel);
  padding: 32px;
  display: flex;
  flex-direction: column;
}
.brand-name {
  font: var(--weight-bold) var(--size-20)/var(--leading-tight) var(--font-ui);
  letter-spacing: -0.01em;
  color: #fff;
}
.brand-mark {
  flex: 1;
  display: grid;
  place-items: center;
}
.pane {
  flex: 1;
  background: var(--surface-panel);
  border-radius: var(--radius-panel);
  display: grid;
  place-items: center;
  padding: 32px;
}
.form {
  width: min(380px, 100%);
  display: flex;
  flex-direction: column;
  gap: 24px;
}
.heading {
  margin: 0 0 8px;
  font: var(--text-display);
  letter-spacing: -0.01em;
}
.submit {
  display: flex;
  flex-direction: column;
  gap: 16px;
  margin-top: 8px;
}
.switch {
  font: var(--text-meta);
  color: var(--grey);
}
.link { color: var(--blue); }
</style>
