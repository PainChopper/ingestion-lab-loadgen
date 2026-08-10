import { describe, expect, it } from 'vitest'
import type { NumericControlSnapshot } from '../../model/loadgen'
import { ACTOR_GEOMETRY, createPipelineGeometry } from './geometry'
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

  it.each([
    { actor: 'reader', count: 1, mode: 'detailed', columns: 1, rows: 1, height: 113 },
    { actor: 'reader', count: 7, mode: 'detailed', columns: 4, rows: 2, height: 158 },
    { actor: 'sender', count: 1, mode: 'detailed', columns: 1, rows: 1, height: 113 },
    { actor: 'sender', count: 7, mode: 'detailed', columns: 4, rows: 2, height: 158 },
    { actor: 'sender', count: 8, mode: 'compact', columns: 8, rows: 1, height: 96 },
    { actor: 'sender', count: 16, mode: 'compact', columns: 8, rows: 2, height: 124 },
    { actor: 'sender', count: 32, mode: 'compact', columns: 8, rows: 4, height: 180 },
  ] as const)(
    'uses the portrait $actor $count worker grid',
    ({ actor, count, mode, columns, rows, height }) => {
      const geometry = createPipelineGeometry({
        orientation: 'portrait',
        readerWorkers: actor === 'reader' ? count : 7,
        senderWorkers: actor === 'sender' ? count : 32,
      })
      const actorGeometry = geometry.actors[actor]
      if (!('metrics' in actorGeometry)) {
        throw new Error('portrait worker metrics are missing')
      }
      const bounds = actorGeometry.bounds
      const layout = getWorkerActorLayout(
        actor,
        bounds,
        senderWorkers(count),
        'portrait',
      )

      expect(layout).toMatchObject({ mode, columns, rows, height })
      expect(layout.top + layout.height).toBe(bounds.bottom)
      expect(bounds.bottom - actorGeometry.metrics.secondary.y)
        .toBeGreaterThanOrEqual(18)
      expect(layout.chips).toHaveLength(count)
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
})
