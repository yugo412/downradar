<script setup lang="ts">
const { githubRepo } = useRuntimeConfig().public

useSeoMeta({
  title: 'About',
  description:
    'What DownRadar does, where its status data comes from, and what a visitor can do with it.',
  ogTitle: 'About DownRadar',
  ogDescription:
    'Status read straight from each official status page, with email alerts and a free public API.',
  ogType: 'website',
})
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8">
    <article class="rounded-box border border-base-300 bg-base-100 p-5 md:p-8">
      <h1 class="text-3xl font-bold tracking-tight">About DownRadar</h1>
      <p class="mt-2 opacity-70">
        One page showing the current status of popular services, read directly from each service's own
        status page and checked every few minutes.
      </p>

      <section class="mt-10">
        <h2 class="text-lg font-semibold">Why it exists</h2>
        <p class="mt-3 text-sm leading-relaxed opacity-80">
          Every service keeps a status page of its own, and they are all in different places with
          different wording: one says all systems operational, another says minor service outage, a
          third says nothing at all. When something feels slow, working out whether it is your
          connection or the service itself means opening them one by one.
        </p>
        <p class="mt-3 text-sm leading-relaxed opacity-80">
          DownRadar reads those pages on a schedule instead. Each answer is turned into one of six
          states, so the whole catalogue can be compared at a glance, and each change is kept, so you
          can see when something went wrong and when it came back.
        </p>
      </section>

      <section class="mt-10">
        <h2 class="text-lg font-semibold">What you can do here</h2>
        <ul class="mt-3 list-disc space-y-3 pl-5 text-sm leading-relaxed opacity-80">
          <li>
            <strong>Look a service up.</strong> Search the list, narrow it by state, or put whatever is
            having problems at the top. Each service has a page of its own with its components, when
            its status last changed, and its recent incidents.
          </li>
          <li>
            <strong>Be emailed when it changes.</strong> The bell on a service page takes an address and
            mails you after a change settles, in either direction, so a recovery is announced as much
            as an outage. A vendor that flips back and forth gets one message for the state it ends in.
            No account is needed, and every email carries a link that stops them.
          </li>
          <li>
            <strong>Keep your alerts in one place.</strong> With an account, everything you follow is
            listed on one screen, where an alert can be paused, started again, or removed. An account
            also keeps more alerts than the single one a visitor without an account gets.
          </li>
          <li>
            <strong>Take the data elsewhere.</strong> The
            <NuxtLink to="/api" class="link">public API</NuxtLink> serves the same status as read-only
            JSON. It is free, needs no key, and is happy to be cached.
          </li>
          <li>
            <strong>Suggest a service.</strong> If something you use is missing, the search box offers
            to send it in, and a person reviews it before it starts being monitored.
          </li>
        </ul>
      </section>

      <section class="mt-10">
        <h2 class="text-lg font-semibold">Where the data comes from</h2>
        <p class="mt-3 text-sm leading-relaxed opacity-80">
          Straight from the source: each service's official status page and nothing else. There are no
          crowd-sourced reports and no user-submitted downtime, so a status here is what the service
          itself says rather than what a sample of its users is guessing.
        </p>
        <p class="mt-3 text-sm leading-relaxed opacity-80">
          That also sets the limit. A page that cannot be reached, or that words its status in a way we
          do not recognise, is shown as <StatusBadge status="unknown" size="sm" /> rather than as
          operational. A service that is slow to admit a problem is slow here too, so the data is never
          better than the page it came from, and DownRadar is a convenience rather than a contract.
        </p>
      </section>

      <section class="mt-10">
        <h2 class="text-lg font-semibold">Who runs it</h2>
        <p class="mt-3 text-sm leading-relaxed opacity-80">
          A small independent project, not affiliated with any of the services it follows. Every name
          and trademark belongs to its respective owner.
        </p>
        <p v-if="githubRepo" class="mt-3 text-sm leading-relaxed opacity-80">
          The code is open, so anyone can read how a status is read or run a copy of it themselves:
          <a
            :href="`https://github.com/${githubRepo}`"
            target="_blank"
            rel="noopener noreferrer"
            class="link"
          >{{ githubRepo }}</a>.
        </p>
        <p class="mt-3 text-sm leading-relaxed opacity-80">
          Questions, corrections, or a status page we read wrongly?
          <NuxtLink to="/contact" class="link">Write in</NuxtLink> and it lands in the inbox the site is
          run from.
        </p>
      </section>
    </article>
  </div>
</template>
