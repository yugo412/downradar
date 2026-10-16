// One clock for the whole app. The index renders over a hundred "N ago" values,
// so they share a single interval rather than starting a timer each.
//
// VueUse's useNow looks like the tool for this, but it is not shared and it
// ticks on requestAnimationFrame, which for a hundred values is a lot of work
// to move a number once a second.
const now = ref(Date.now())
let timer: ReturnType<typeof setInterval> | undefined

export function useNow() {
  if (import.meta.client && !timer) {
    timer = setInterval(() => {
      now.value = Date.now()
    }, 1000)
  }
  return now
}
