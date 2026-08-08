import type { NumericControlSnapshot } from '../../model/loadgen'
import type { Point, WorkerActorBounds } from './geometry'

const DETAILED_WORKER_LIMIT = 7
const COMPACT_COLUMNS = 4
const COMPACT_CHIP_SCALE = 0.43
const COMPACT_COLUMN_PITCH = 29
const COMPACT_ROW_PITCH = 22
const WORKER_CHIP_WIDTH = 66.5
const WORKER_CHIP_HEIGHT = 39

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

function normalizedWorkerCount(workers: NumericControlSnapshot): number {
  const value = workers.applied ?? workers.min
  return Math.min(workers.max, Math.max(workers.min, Math.round(value)))
}

export function getWorkerActorLayout(
  actor: 'reader' | 'sender',
  bounds: WorkerActorBounds,
  workers: NumericControlSnapshot,
): WorkerActorLayout {
  const workerCount = normalizedWorkerCount(workers)
  const compact = actor === 'sender' && workerCount > DETAILED_WORKER_LIMIT
  const height = compact
    ? DETAILED_WORKER_LIMIT * bounds.rowHeight + bounds.padding * 2
    : workerCount * bounds.rowHeight + bounds.padding * 2
  const top = bounds.bottom - height

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
        y: top + bounds.padding - 4 + index * bounds.rowHeight,
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
