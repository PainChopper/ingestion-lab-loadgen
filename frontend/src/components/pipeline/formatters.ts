const integerFormatter = new Intl.NumberFormat('en-US', {
  maximumFractionDigits: 0,
})

export function formatInteger(value: number | null): string {
  return value === null ? '—' : integerFormatter.format(value)
}

export function formatRate(value: number | null): string {
  return value === null ? '—' : `${integerFormatter.format(value)} tx/s`
}

export function formatMilliseconds(value: number | null): string {
  return value === null ? '—' : `${integerFormatter.format(value)} ms`
}
