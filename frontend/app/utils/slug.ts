// The same rule the API applies when it turns a name into an address, so the
// field can show what will be saved. Letters and digits are kept, everything
// else becomes a dash, and the result is capped at the length a slug may have.
export function slugify(value: string): string {
  return value
    .toLowerCase()
    .trim()
    .replace(/[^\p{L}\p{N}]+/gu, '-')
    .slice(0, 60)
    .replace(/^-+|-+$/g, '')
}
