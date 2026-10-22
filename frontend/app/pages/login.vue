<script setup lang="ts">
definePageMeta({ ssr: false })

useSeoMeta({ title: 'Sign in', robots: 'noindex, nofollow' })

const route = useRoute()
const { user, loaded, signIn, sendSignInLink, signInWithLink } = useAuth()
const { providers, load: loadProviders, signInURL } = useAuthProviders()

const email = ref('')
const password = ref('')
const remember = ref(false)

// A link in the inbox is how somebody signs in by default. The password form is
// one click away, for the people who keep one.
const withPassword = ref(false)

const busy = ref(false)
const error = ref('')
const sent = ref(false)
const sentTo = ref('')

// The token from the email is spent here, in the browser, and not by the fetch a
// mail scanner makes of the link: the page only carries it as far as this form.
const token = computed(() => String(route.query.token ?? ''))
const linkState = ref<'idle' | 'busy' | 'done' | 'failed'>('idle')

// Only a path inside this site is accepted, so ?next= cannot bounce a freshly
// signed in moderator to another domain.
const next = computed(() => {
  const raw = String(route.query.next ?? '')
  return raw.startsWith('/') && !raw.startsWith('//') ? raw : '/'
})

// The callback sends a failed sign in back here with the reason, because the
// browser only followed a link and a JSON answer would be a dead end.
const oauthError = computed(() => {
  const reason = String(route.query.oauth ?? '')
  if (!reason) return ''
  const name = String(route.query.provider ?? '')
  const label = providers.value.find((p) => p.name === name)?.label ?? 'the provider'
  if (reason === 'refused') return `Signing in with ${label} was cancelled.`
  if (reason === 'disabled') return 'That account cannot sign in.'
  return `Signing in with ${label} did not work. Try again.`
})

onMounted(loadProviders)

// Only reacts to the session arriving, so that signing in does not navigate
// twice: submit() already does it. A link waiting in the address is the
// exception: it may belong to another account than the one this browser has.
watch(
  loaded,
  () => {
    if (loaded.value && user.value && !token.value) {
      navigateTo(next.value)
    }
  },
  { immediate: true },
)

onMounted(async () => {
  if (!token.value) return

  linkState.value = 'busy'
  try {
    await signInWithLink(token.value)
    linkState.value = 'done'
    await navigateTo(next.value)
  } catch (err) {
    linkState.value = 'failed'
    error.value = apiError(err, 'That sign in link could not be used.')
  }
})

async function submit() {
  busy.value = true
  error.value = ''

  try {
    if (!withPassword.value) {
      // The page the visitor asked for travels with the link, so following it on
      // a phone still lands where they were going.
      await sendSignInLink(email.value, next.value === '/' ? '' : next.value)
      sentTo.value = email.value.trim()
      sent.value = true
      return
    }

    await signIn(email.value, password.value, remember.value)
    await navigateTo(next.value)
  } catch (err) {
    error.value = apiError(
      err,
      withPassword.value ? 'Could not sign in.' : 'Could not send the link.',
    )
  } finally {
    busy.value = false
  }
}

function backToForm() {
  sent.value = false
  error.value = ''
}
</script>

<template>
  <div class="mx-auto max-w-md px-4 py-8">
    <h1 class="text-2xl font-bold tracking-tight">Sign in</h1>

    <div class="mt-6 rounded-box border border-base-300 bg-base-100 p-5 shadow-sm">
      <p v-if="oauthError" role="alert" class="mb-4 text-sm text-error">{{ oauthError }}</p>

      <p v-if="linkState === 'busy'" class="flex items-center gap-2 text-sm opacity-80">
        <span class="loading loading-spinner loading-sm" />
        Signing you in from the link in your email.
      </p>

      <template v-else-if="sent">
        <p class="text-sm">
          The link is on its way to <span class="font-medium">{{ sentTo }}</span>. Open it in this
          browser and you are in. It works once and expires in fifteen minutes.
        </p>
        <button type="button" class="link mt-3 text-sm" @click="backToForm">
          Use a different address
        </button>
      </template>

      <template v-else>
        <template v-if="providers.length">
          <div class="flex flex-col gap-2">
            <a
              v-for="p in providers"
              :key="p.name"
              :href="signInURL(p.name, next)"
              class="btn btn-outline w-full"
            >
              <ProviderIcon :name="p.name" />
              Continue with {{ p.label }}
            </a>
          </div>
          <div class="divider my-3 text-xs uppercase tracking-wide opacity-60">or</div>
        </template>

        <form class="flex flex-col gap-4" @submit.prevent="submit">
          <label class="form-control w-full">
            <span class="label-text mb-1 text-sm font-medium">Email</span>
            <input
              v-model="email"
              type="email"
              class="input input-bordered w-full"
              autocomplete="username"
              required
              autofocus
            />
          </label>

          <button
            type="button"
            class="link self-start text-sm"
            @click="withPassword = !withPassword"
          >
            {{ withPassword ? 'Email me a sign in link instead' : 'Sign in with a password instead' }}
          </button>

          <template v-if="withPassword">
            <PasswordInput
              v-model="password"
              label="Password"
              autocomplete="current-password"
              required
            />

            <label class="flex cursor-pointer items-center gap-2 text-sm">
              <input v-model="remember" type="checkbox" class="checkbox checkbox-sm" />
              <span>Remember me on this browser</span>
            </label>
          </template>

          <p v-if="error" role="alert" class="text-sm text-error">{{ error }}</p>

          <button type="submit" class="btn btn-primary" :disabled="busy">
            <span v-if="busy" class="loading loading-spinner loading-sm" />
            {{ withPassword ? 'Sign in' : 'Send the link' }}
          </button>
        </form>

        <div class="mt-4 flex flex-wrap items-center justify-between gap-2 text-sm opacity-70">
          <NuxtLink v-if="withPassword" to="/forgot" class="link">
            Forgot your password?
          </NuxtLink>
          <NuxtLink to="/register" class="link">Create an account</NuxtLink>
        </div>
      </template>
    </div>
  </div>
</template>
