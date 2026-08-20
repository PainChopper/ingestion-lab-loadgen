import type { NumericControlSnapshot } from '../../model/loadgen'
import type { Point, WorkerActorBounds } from './geometry'
import type { PipelineOrientation } from './pipelineLayout'

const DETAILED_WORKER_LIMIT = 7
const COMPACT_COLUMNS = 4
const COMPACT_CHIP_SCALE = 0.43
const COMPACT_COLUMN_PITCH = 29
const COMPACT_ROW_PITCH = 22
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

export function getPortraitWorkerGridMetrics(
  actor: 'reader' | 'sender',
  workerCount: number,
): PortraitWorkerGridMetrics {
  const normalizedCount = Math.max(1, Math.round(workerCount))
  const compact = actor === 'sender' && normalizedCount > DETAILED_WORKER_LIMIT
  const columns = compact
    ? Math.min(8, normalizedCount)
    : Math.min(4, normalizedCount)
  const rows = Math.ceil(normalizedCount / columns)
  const scale = compact ? COMPACT_CHIP_SCALE : 0.86
  const chipWidth = WORKER_CHIP_WIDTH * scale
  const chipHeight = WORKER_CHIP_HEIGHT * scale
  const columnPitch = compact ? 43 : 82
  const rowPitch = compact ? 28 : 45
  const gridBottom = 18 + (rows - 1) * rowPitch + chipHeight

  return {
    workerCount: normalizedCount,
    mode: compact ? 'compact' : 'detailed',
    columns,
    rows,
    scale,
    chipWidth,
    chipHeight,
    columnPitch,
    rowPitch,
    gridBottom,
    height: Math.ceil(gridBottom + 61),
  }
}

export function getWorkerActorLayout(
  actor: 'reader' | 'sender',
  bounds: WorkerActorBounds,
  workers: NumericControlSnapshot,
  orientation: PipelineOrientation = 'landscape',
): WorkerActorLayout {
  const workerCount = normalizedWorkerCount(workers)
  const compact = actor === 'sender' && workerCount > DETAILED_WORKER_LIMIT
  if (orientation === 'portrait') {
    const metrics = getPortraitWorkerGridMetrics(actor, workerCount)
    const gridWidth =
      (metrics.columns - 1) * metrics.columnPitch + metrics.chipWidth
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
      chips: Array.from({ length: workerCount }, (_, index) => ({
        x: gridX + (index % metrics.columns) * metrics.columnPitch,
        y: gridY + Math.floor(index / metrics.columns) * metrics.rowPitch,
        width: metrics.chipWidth,
        height: metrics.chipHeight,
        scale: metrics.scale,
      })),
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
      chips: Array.from({ length: workerCount }, (_, index) => ({
        x: bounds.x + 20,
        y: contentTop + bounds.padding - 4 + index * bounds.rowHeight,
        width: WORKER_CHIP_WIDTH,
        height: WORKER_CHIP_HEIGHT,
        scale: 1,
      })),
    }
  }

  const columns = COMPACT_COLUMNS
  const rows = Math.ceil(workerCount / columns)
  const chipWidth = WORKER_CHIP_WIDTH * COMPACT_CHIP_SCALE
  const chipHeight = WORKER_CHIP_HEIGHT * COMPACT_CHIP_SCALE
  const gridWidth = (columns - 1) * COMPACT_COLUMN_PITCH + chipWidth
  const gridX = bounds.x + (bounds.width - gridWidth) / 2
  const gridY = top + bounds.padding

  return {
    workerCount,
    mode: 'compact',
    top,
    height,
    columns,
    rows,
    chips: Array.from({ length: workerCount }, (_, index) => ({
      x: gridX + (index % columns) * COMPACT_COLUMN_PITCH,
      y: gridY + Math.floor(index / columns) * COMPACT_ROW_PITCH,
      width: chipWidth,
      height: chipHeight,
      scale: COMPACT_CHIP_SCALE,
    })),
  }
}
