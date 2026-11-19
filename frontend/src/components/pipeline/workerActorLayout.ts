import type { NumericControlSnapshot } from '../../model/loadgen'
import type { Point, WorkerActorBounds } from './geometry'
import type { PipelineOrientation } from './pipelineLayout'

const DETAILED_WORKER_LIMIT = 7
const COMPACT_CHIP_SCALE = 0.36
const COMPACT_COLUMN_PITCH = 26
const COMPACT_ROW_PITCH = 20
const PORT_CLEARANCE = 9
const OVERFLOW_FOOTER_HEIGHT = 24
const PORTRAIT_COMPACT_MIN_COLUMNS = 5
const PORTRAIT_COMPACT_MAX_ROWS = 7
export const PORTRAIT_WORKER_BOUNDS = { width: 360, padding: 18 } as const
const PORTRAIT_COMPACT_CHIP_SCALE = 0.34
const PORTRAIT_COMPACT_COLUMN_PITCH = 26.5
const PORTRAIT_COMPACT_ROW_PITCH = 15.2
const WORKER_CHIP_WIDTH = 66.5
const WORKER_CHIP_HEIGHT = 39

export interface PortraitWorkerGridMetrics {
  readonly workerCount: number
  readonly mode: 'detailed' | 'compact'
  readonly columns: number
  readonly rows: number
  readonly scale: number
  readonly chipWidth: number
  readonly chipHeight: number
  readonly columnPitch: number
  readonly rowPitch: number
  readonly gridBottom: number
  readonly height: number
}

export interface WorkerChipLayout extends Point {
  readonly width: number
  readonly height: number
  readonly scale: number
}

export interface WorkerActorLayout {
  readonly workerCount: number
  readonly mode: 'detailed' | 'compact'
  readonly top: number
  readonly height: number
  readonly columns: number
  readonly rows: number
  readonly chips: readonly WorkerChipLayout[]
}

export function normalizedWorkerCount(workers: NumericControlSnapshot): number {
  const value = workers.applied ?? workers.min
  return Math.min(workers.max, Math.max(workers.min, Math.round(value)))
}

function fittingSlots(space: number, chipSize: number, pitch: number): number {
  return Math.max(0, 1 + Math.floor((space - chipSize) / pitch))
}

export function getPortraitWorkerGridMetrics(
  workerCount: number,
  bounds: Pick<WorkerActorBounds, 'width' | 'padding'> = PORTRAIT_WORKER_BOUNDS,
): PortraitWorkerGridMetrics {
  const normalizedCount = Math.max(1, Math.round(workerCount))
  const compact = normalizedCount > DETAILED_WORKER_LIMIT
  const scale = compact ? PORTRAIT_COMPACT_CHIP_SCALE : 0.86
  const chipWidth = WORKER_CHIP_WIDTH * scale
  const chipHeight = WORKER_CHIP_HEIGHT * scale
  const columnPitch = compact ? PORTRAIT_COMPACT_COLUMN_PITCH : 82
  const rowPitch = compact ? PORTRAIT_COMPACT_ROW_PITCH : 45
  const columns = compact
    ? Math.min(
        fittingSlots(bounds.width - 2 * bounds.padding, chipWidth, columnPitch),
        Math.max(
          PORTRAIT_COMPACT_MIN_COLUMNS,
          Math.ceil(normalizedCount / PORTRAIT_COMPACT_MAX_ROWS),
        ),
      )
    : Math.min(4, normalizedCount)
  const slots = compact
    ? Math.min(normalizedCount, columns * PORTRAIT_COMPACT_MAX_ROWS)
    : normalizedCount
  const rows = columns > 0 ? Math.ceil(slots / columns) : 0
  // Keep the portrait card at its full height as additional columns open.
  const reservedRows = compact
    ? Math.min(PORTRAIT_COMPACT_MAX_ROWS, Math.ceil(normalizedCount / PORTRAIT_COMPACT_MIN_COLUMNS))
    : rows
  const gridBottom = bounds.padding + (reservedRows - 1) * rowPitch + chipHeight

  return {
    workerCount: slots,
    mode: compact ? 'compact' : 'detailed',
    columns,
    rows,
    scale,
    chipWidth,
    chipHeight,
    columnPitch,
    rowPitch,
    gridBottom,
    height: Math.ceil(gridBottom + 90),
  }
}

export function getWorkerActorLayout(
  bounds: WorkerActorBounds,
  workers: NumericControlSnapshot,
  orientation: PipelineOrientation = 'landscape',
  visibleWorkerCount = normalizedWorkerCount(workers),
  desiredWorkerCount = normalizedWorkerCount(workers),
): WorkerActorLayout {
  const workerCount = Math.max(desiredWorkerCount, visibleWorkerCount)
  const compact = workerCount > DETAILED_WORKER_LIMIT
  if (orientation === 'portrait') {
    const metrics = getPortraitWorkerGridMetrics(workerCount, bounds)
    const gridWidth = (metrics.columns - 1) * metrics.columnPitch + metrics.chipWidth
    const top = bounds.bottom - metrics.height
    const gridX = bounds.x + (bounds.width - gridWidth) / 2
    const gridY = top + bounds.padding

    return {
      workerCount: metrics.workerCount,
      mode: metrics.mode,
      top,
      height: metrics.height,
      columns: metrics.columns,
      rows: metrics.rows,
      chips: Array.from(
        { length: Math.min(visibleWorkerCount, metrics.workerCount) },
        (_, index) => ({
          x: gridX + (index % metrics.columns) * metrics.columnPitch,
          y: gridY + Math.floor(index / metrics.columns) * metrics.rowPitch,
          width: metrics.chipWidth,
          height: metrics.chipHeight,
          scale: metrics.scale,
        }),
      ),
    }
  }

  const contentHeight = compact
    ? DETAILED_WORKER_LIMIT * bounds.rowHeight + bounds.padding * 2
    : workerCount * bounds.rowHeight + bounds.padding * 2
  const height = Math.max(contentHeight, bounds.minHeight ?? 0)
  const top = bounds.bottom - height
  const contentTop = bounds.bottom - contentHeight

  if (!compact) {
    return {
      workerCount,
      mode: 'detailed',
      top,
      height,
      columns: 1,
      rows: workerCount,
      chips: Array.from({ length: visibleWorkerCount }, (_, index) => ({
        x: bounds.x + 20,
        y: contentTop + bounds.padding - 4 + index * bounds.rowHeight,
        width: WORKER_CHIP_WIDTH,
        height: WORKER_CHIP_HEIGHT,
        scale: 1,
      })),
    }
  }

  const chipWidth = WORKER_CHIP_WIDTH * COMPACT_CHIP_SCALE
  const chipHeight = WORKER_CHIP_HEIGHT * COMPACT_CHIP_SCALE
  const columns = fittingSlots(bounds.width - 2 * PORT_CLEARANCE, chipWidth, COMPACT_COLUMN_PITCH)
  const maxRows = fittingSlots(
    height - bounds.padding - OVERFLOW_FOOTER_HEIGHT,
    chipHeight,
    COMPACT_ROW_PITCH,
  )
  const slots = Math.min(workerCount, columns * maxRows)
  const rows = columns > 0 ? Math.ceil(slots / columns) : 0
  const gridWidth = (columns - 1) * COMPACT_COLUMN_PITCH + chipWidth
  const gridX = bounds.x + (bounds.width - gridWidth) / 2
  const gridY = top + bounds.padding

  return {
    workerCount: slots,
    mode: 'compact',
    top,
    height,
    columns,
    rows,
    chips: Array.from({ length: Math.min(visibleWorkerCount, slots) }, (_, index) => ({
      x: gridX + (index % columns) * COMPACT_COLUMN_PITCH,
      y: gridY + Math.floor(index / columns) * COMPACT_ROW_PITCH,
      width: chipWidth,
      height: chipHeight,
      scale: COMPACT_CHIP_SCALE,
    })),
  }
}
