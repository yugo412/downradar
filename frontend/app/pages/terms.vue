<script setup lang="ts">
const updated = '30 October 2026, 02:38 UTC'

useSeoMeta({
  title: 'Terms of Service',
  description:
    'The terms for using DownRadar, its status data, accounts, alerts and public API.',
})
</script>

<template>
  <LegalPage title="Terms of Service" :updated="updated">
    <p>
      These terms govern your use of DownRadar. By using the site, you agree to
      them. If you do not agree, please do not use the site.
    </p>

    <section>
      <h2 class="text-lg font-semibold">The service</h2>
      <p class="mt-3">
        DownRadar gathers status information published by other services and
        presents it in one place. It is not affiliated with those services, and
        their names and trademarks belong to their owners. Status data can be
        delayed, incomplete or wrong, so it is a convenience rather than a
        guarantee. A generated summary is written from that data by an AI
        provider; it can be wrong too, and the official list underneath it is the
        record.
      </p>
      <p class="mt-3">
        The site is provided as it is and may change, pause or stop at any time.
      </p>
    </section>

    <section>
      <h2 class="text-lg font-semibold">Accounts</h2>
      <p class="mt-3">
        Give accurate information, keep your password and sign-in links to
        yourself, and take responsibility for activity under your account. We may
        suspend or remove an account that breaks these terms or puts the site or
        other people at risk.
      </p>
    </section>

    <section>
      <h2 class="text-lg font-semibold">Alerts and notifications</h2>
      <p class="mt-3">
        Alert delivery depends on email providers, webhooks, Telegram, Slack,
        Discord, Lark and the networks between them, so it is not guaranteed. Do
        not rely on DownRadar for emergencies, safety-critical work or anything where
        a missed message could cause harm. You are responsible for the endpoints
        and secrets you configure.
      </p>
      <p class="mt-3">
        In-site notifications appear only in the account feed while you are signed
        in. They are not a separate delivery channel and do not replace an alert.
        Staff may send announcements to subscriber accounts, and operational
        notices may be shown to staff.
      </p>
    </section>

    <section>
      <h2 class="text-lg font-semibold">Acceptable use</h2>
      <ul class="mt-3 list-disc space-y-2 pl-5">
        <li>Do not use the site for unlawful, abusive or fraudulent activity.</li>
        <li>Do not try to break into accounts, probe or bypass access controls.</li>
        <li>Do not disrupt the site, overload it, or evade its rate limits.</li>
        <li>Do not use alerts or the contact form to send spam or harassment.</li>
      </ul>
    </section>

    <section>
      <h2 class="text-lg font-semibold">Content and ownership</h2>
      <p class="mt-3">
        The site's design, code and written content belong to DownRadar or the
        people who provided them. Status data belongs to the service that
        published it. You may use the public API, feeds and MCP server as offered;
        they do not transfer ownership of the data.
      </p>
    </section>

    <section>
      <h2 class="text-lg font-semibold">Third-party services</h2>
      <p class="mt-3">
        Links and integrations with GitHub, Google, Telegram, Slack, Discord,
        Lark and other services are governed by those services' own terms and
        privacy policies. DownRadar does not control them.
      </p>
    </section>

    <section>
      <h2 class="text-lg font-semibold">Disclaimer</h2>
      <p class="mt-3">
        To the fullest extent permitted by law, the site is provided without
        warranties of any kind, including accuracy, availability and fitness for a
        particular purpose.
      </p>
    </section>

    <section>
      <h2 class="text-lg font-semibold">Limitation of liability</h2>
      <p class="mt-3">
        To the fullest extent permitted by law, DownRadar is not liable for
        indirect, incidental or consequential loss, or for loss of data, profits
        or business, arising from use of the site or reliance on its status data.
        Nothing here excludes liability that cannot be excluded by law.
      </p>
    </section>

    <section>
      <h2 class="text-lg font-semibold">Changes</h2>
      <p class="mt-3">
        These terms may change as the site does. The date at the bottom of the
        page says when they last changed. Continuing to use the site after a
        change means you accept the updated terms.
      </p>
    </section>

    <section>
      <h2 class="text-lg font-semibold">Contact</h2>
      <p class="mt-3">
        Questions about these terms can go through the
        <NuxtLink to="/contact" class="link">contact form</NuxtLink>.
      </p>
    </section>
  </LegalPage>
</template>
