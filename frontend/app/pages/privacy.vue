<script setup lang="ts">
const updated = '30 October 2026, 02:38 UTC'

useSeoMeta({
  title: 'Privacy Policy',
  description:
    'What DownRadar collects, why it is needed, how it is kept, and the choices you have.',
})
</script>

<template>
  <LegalPage title="Privacy Policy" :updated="updated">
    <p>
      DownRadar is an independent status aggregator. This policy explains what
      information the site handles, why it is needed, and the choices you have.
    </p>

    <section>
      <h2 class="text-lg font-semibold">What we collect</h2>
      <ul class="mt-3 list-disc space-y-2 pl-5">
        <li>
          <strong>Account details:</strong> your name, email address, password
          hash, whether the address is confirmed, your role, and the provider
          accounts linked to it, such as GitHub or Google.
        </li>
        <li>
          <strong>Alert details:</strong> the address or webhook a message goes
          to, Telegram chat and bot details, Slack, Discord or Lark webhook URLs
          and signing secrets, the vendor and components you watch, pause state,
          and delivery attempts.
        </li>
        <li>
          <strong>Notification details:</strong> the notices sent to your
          account, which account received each one, and whether you have read
          it.
        </li>
        <li>
          <strong>Messages:</strong> the name, address, subject and message you
          send through the contact form.
        </li>
        <li>
          <strong>Technical data:</strong> IP address, request path, time and
          browser information in server logs, used for rate limits, security and
          debugging.
        </li>
      </ul>
    </section>

    <section>
      <h2 class="text-lg font-semibold">Cookies</h2>
      <p class="mt-3">
        The site uses a session cookie to keep you signed in, short-lived state
        cookies while signing in with a provider, connecting one from your
        account, or connecting Slack or Discord, and a theme cookie that
        remembers your colour preference. There are no advertising or analytics
        cookies.
      </p>
    </section>

    <section>
      <h2 class="text-lg font-semibold">How it is used</h2>
      <ul class="mt-3 list-disc space-y-2 pl-5">
        <li>To create accounts, sign you in, and send confirmation or reset links.</li>
        <li>To deliver the alerts you ask for to the channels you choose.</li>
        <li>To show in-site notifications for the alerts you follow, site-wide announcements, and operational notices to staff.</li>
        <li>To reply when you write through the contact form.</li>
        <li>To apply rate limits, prevent abuse, investigate failures and keep the site secure.</li>
      </ul>
    </section>

    <section>
      <h2 class="text-lg font-semibold">Generated summaries</h2>
      <p class="mt-3">
        A vendor page may show a short summary above its official incidents. It
        is written from the same public status data the page shows, and the page
        labels it as generated with the time it was written. To draft it, that
        status data is sent to an AI provider. It contains vendor and component
        names and incident text from the public status page, not your account
        details, alert address or any other information about you.
      </p>
    </section>

    <section>
      <h2 class="text-lg font-semibold">Sharing</h2>
      <p class="mt-3">
        Data reaches the mail provider that sends the site's email, the AI
        provider that drafts generated summaries, and the infrastructure that
        hosts it. If you sign in with or connect GitHub or Google, those
        providers take part in that flow. If you add an alert channel, the
        address or webhook you give is used to deliver through Telegram, Slack,
        Discord, Lark or your own endpoint. Notification content and read state
        stay inside the site and are not sent to a third party. We do not sell
        or rent personal data, and we disclose it to anyone else only when the
        law requires it.
      </p>
    </section>

    <section>
      <h2 class="text-lg font-semibold">How long it is kept</h2>
      <p class="mt-3">
        An account and the alerts attached to it are kept until you remove them
        or delete the account from
        <NuxtLink to="/account/profile" class="link">your profile</NuxtLink>.
        Deleting the account removes its alerts, channels, delivery records and
        notification recipient rows with it. A notice that was sent to other
        people stays without your copy. Server logs are kept for as long as they
        are useful for troubleshooting and security, then discarded. Copies in
        backups may take a little longer to disappear.
      </p>
    </section>

    <section>
      <h2 class="text-lg font-semibold">Your choices</h2>
      <p class="mt-3">
        You can change your name and address from
        <NuxtLink to="/account/profile" class="link">your profile</NuxtLink>,
        change the password from
        <NuxtLink to="/account/password" class="link">the password page</NuxtLink>,
        pause or remove alerts from
        <NuxtLink to="/alerts" class="link">the alerts page</NuxtLink>, and stop
        any alert from the link in one of its messages. You can delete your
        account for good from your profile page. Services you added stay in the
        catalogue without your name attached. To ask for a copy, correction or
        other help with your account data, write through the
        <NuxtLink to="/contact" class="link">contact form</NuxtLink>.
      </p>
    </section>

    <section>
      <h2 class="text-lg font-semibold">Security</h2>
      <p class="mt-3">
        Passwords are stored as hashes, session tokens are stored hashed, and
        channel secrets are shown masked and sealed at rest with the app key. No
        internet service can promise perfect security, but the site is built to
        keep these values out of pages and logs.
      </p>
    </section>

    <section>
      <h2 class="text-lg font-semibold">Children</h2>
      <p class="mt-3">
        The site is not directed at children under 13. Please do not create an
        account if you are under 13.
      </p>
    </section>

    <section>
      <h2 class="text-lg font-semibold">Changes</h2>
      <p class="mt-3">
        This policy may change as the site does. The date at the bottom of the
        page says when it last changed.
      </p>
    </section>
  </LegalPage>
</template>
