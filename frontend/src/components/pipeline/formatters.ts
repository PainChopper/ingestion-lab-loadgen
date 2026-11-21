import type { ReaderSnapshot } from '../../model/loadgen'

const integerFormatter = new Intl.NumberFormat('en-US', {
  maximumFractionDigits: 0,
})

export function formatInteger(value: number | null): string {
  return value === null ? '—' : integerFormatter.format(value)
}

export function formatRate(value: number | null): string {
  return value === null ? 'No data' : `${integerFormatter.format(value)} tx/s`
}

export function formatMilliseconds(value: number | null): string {
  return value === null ? '—' : `${integerFormatter.format(value)} ms`
}

export function formatReaderErrorCount({ rowsRead, sourceError }: ReaderSnapshot): string {
  if (sourceError !== null && sourceError !== undefined) return '1 error'
  // A received snapshot always has a row count, even when retained after a connection error.
  return rowsRead === null ? '— errors' : '0 errors'
}
