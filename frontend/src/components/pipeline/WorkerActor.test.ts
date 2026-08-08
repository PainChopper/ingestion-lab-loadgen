import { describe, expect, it } from 'vitest'
import type { NumericControlSnapshot } from '../../model/loadgen'
import { ACTOR_GEOMETRY } from './geometry'
import { getWorkerActorLayout } from './workerActorLayout'

function senderWorkers(applied: number): NumericControlSnapshot {
  return {
    applied,
    preview: null,
    pending: null,
    min: 1,
    max: 32,
    step: 1,
    unit: 'workers',
    applyMode: 'immediate',
  }
}

describe('WorkerActor sender layout', () => {
  it.each([
    { count: 1, mode: 'detailed', rows: 1 },
    { count: 7, mode: 'detailed', rows: 7 },
    { count: 8, mode: 'compact', rows: 2 },
    { count: 16, mode: 'compact', rows: 4 },
    { count: 32, mode: 'compact', rows: 8 },
  ] as const)(
    'keeps $count workers inside the bottom-aligned actor box',
    ({ count, mode, rows }) => {
      const bounds = ACTOR_GEOMETRY.sender.bounds
      const layout = getWorkerActorLayout(
        'sender',
        bounds,
        senderWorkers(count),
      )

      expect(layout.workerCount).toBe(count)
      expect(layout.mode).toBe(mode)
      expect(layout.rows).toBe(rows)
      expect(layout.chips).toHaveLength(count)
      expect(layout.top + layout.height).toBe(bounds.bottom)

      for (const chip of layout.chips) {
        expect(chip.x).toBeGreaterThanOrEqual(bounds.x)
        expect(chip.x + chip.width).toBeLessThanOrEqual(
          bounds.x + bounds.width,
        )
        expect(chip.y).toBeGreaterThanOrEqual(layout.top)
        expect(chip.y + chip.height).toBeLessThanOrEqual(bounds.bottom)
      }
    },
  )

  it('preserves the approved seven-worker footprint in compact mode', () => {
    const bounds = ACTOR_GEOMETRY.sender.bounds
    const seven = getWorkerActorLayout('sender', bounds, senderWorkers(7))

    for (const count of [8, 16, 32]) {
      const compact = getWorkerActorLayout(
        'sender',
        bounds,
        senderWorkers(count),
      )

      expect(compact.top).toBe(seven.top)
      expect(compact.height).toBe(seven.height)
    }
  })

  it('keeps the reader on the detailed one-to-seven layout', () => {
    const workers = { ...senderWorkers(7), max: 7 }
    const layout = getWorkerActorLayout(
      'reader',
      ACTOR_GEOMETRY.reader.bounds,
      workers,
    )

    expect(layout.mode).toBe('detailed')
    expect(layout.rows).toBe(7)
    expect(layout.chips.every((chip) => chip.scale === 1)).toBe(true)
  })
})
