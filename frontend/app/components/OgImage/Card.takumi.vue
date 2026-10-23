<script setup lang="ts">
const props = defineProps<{
  eyebrow: string
  name: string
  description: string
  address: string
}>()

// The renderer has no reliable text ellipsis, so every line that can arrive
// from a customer is cut here before it reaches the layout.
function shorten(value: string, limit: number) {
  const text = String(value ?? '').replace(/\s+/g, ' ').trim()
  if (text.length <= limit) return text
  return `${text.slice(0, limit - 1).trimEnd()}…`
}
</script>

<template>
  <div
    class="flex h-full w-full"
    style="background:#f2f2f2;padding:52px;font-family:Inter,sans-serif;"
  >
    <div
      class="flex h-full w-full flex-col overflow-hidden"
      style="background:#fff;border:1px solid #d1d5db;border-radius:8px;box-shadow:0 18px 40px rgba(15,23,42,.10);"
    >
      <div style="height:10px;background:#38bdf8;" />
      <div class="flex flex-1 flex-col justify-between" style="padding:56px 64px;">
        <div class="flex items-center gap-3">
          <span
            style="display:inline-block;width:22px;height:22px;border-radius:9999px;background:#ef4444;"
          />
          <span style="font-size:28px;font-weight:700;letter-spacing:-.02em;color:#1f2937;">
            DownRadar
          </span>
        </div>

        <div class="flex flex-col">
          <p
            style="display:block;margin:0;font-size:20px;font-weight:700;letter-spacing:.16em;text-transform:uppercase;color:#38bdf8;"
          >
            {{ shorten(props.eyebrow, 48) }}
          </p>
          <h1
            style="display:block;margin:0;padding-top:20px;font-size:64px;font-weight:700;line-height:1.15;letter-spacing:-.035em;color:#1f2937;"
          >
            {{ shorten(props.name, 28) }}
          </h1>
          <p
            style="display:block;margin:0;padding-top:22px;font-size:30px;line-height:1.35;color:#6b7280;"
          >
            {{ shorten(props.description, 95) }}
          </p>
        </div>

        <div style="display:block;border-top:1px solid #e5e7eb;padding-top:24px;">
          <span style="display:block;font-size:24px;color:#6b7280;">
            {{ shorten(props.address, 64) }}
          </span>
        </div>
      </div>
    </div>
  </div>
</template>
