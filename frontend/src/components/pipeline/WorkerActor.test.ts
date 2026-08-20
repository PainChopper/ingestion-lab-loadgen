import { describe, expect, it } from 'vitest'
import type { NumericControlSnapshot } from '../../model/loadgen'
import {
  ACTOR_BOTTOM,
  ACTOR_GEOMETRY,
  createPipelineGeometry,
  FLOW_BASELINE,
  LANDSCAPE_WORKER_ACTOR_MIN_HEIGHT,
} from './geometry'
import { getWorkerActorLayout } from './workerActorLayout'

function workers(applied: number): NumericControlSnapshot {
  return {
    applied,
    preview: null,
    pending: null,
    min: 0,
    max: 32,
    step: 1,
    unit: 'workers',
    applyMode: 'immediate',
  }
}

const LANDSCAPE_CASES = [
  { count: 0, reader: { top: 355, height: 120 }, sender: { top: 355, height: 120 } },
  { count: 1, reader: { top: 355, height: 120 }, sender: { top: 355, height: 120 } },
  { count: 2, reader: { top: 355, height: 120 }, sender: { top: 355, height: 120 } },
  { count: 3, reader: { top: 326, height: 149 }, sender: { top: 326, height: 149 } },
  { count: 7, reader: { top: 170, height: 305 }, sender: { top: 170, height: 305 } },
  { count: 8, reader: { top: 131, height: 344 }, sender: { top: 170, height: 305 } },
  { count: 16, reader: { top: -181, height: 656 }, sender: { top: 170, height: 305 } },
  { count: 32, reader: { top: -805, height: 1280 }, sender: { top: 170, height: 305 } },
] as const

describe('WorkerActor layout', () => {
  it('derives the landscape minimum height from the fixed anchors', () => {
    expect(LANDSCAPE_WORKER_ACTOR_MIN_HEIGHT).toBe(120)
    expect(LANDSCAPE_WORKER_ACTOR_MIN_HEIGHT)
      .toBe(2 * (ACTOR_BOTTOM - FLOW_BASELINE))
    expect(ACTOR_GEOMETRY.reader.bounds.minHeight).toBe(120)
    expect(ACTOR_GEOMETRY.sender.bounds.minHeight).toBe(120)
  })

  it.each(['reader', 'sender'] as const)(
    'uses exact bottom-aligned landscape bounds for %s counts',
    (actor) => {
      const bounds = ACTOR_GEOMETRY[actor].bounds
      const layouts = LANDSCAPE_CASES.map((testCase) => ({
        count: testCase.count,
        expected: testCase[actor],
        layout: getWorkerActorLayout(actor, bounds, workers(testCase.count)),
      }))

      expect(layouts.map(({ count, layout }) => ({
        count,
        top: layout.top,
        height: layout.height,
      }))).toEqual(LANDSCAPE_CASES.map((testCase) => ({
        count: testCase.count,
        ...testCase[actor],
      })))

      for (const { count, expected, layout } of layouts) {
        expect(layout.workerCount).toBe(count)
        expect(layout.chips).toHaveLength(count)
        expect(layout.top).toBe(expected.top)
        expect(layout.height).toBe(expected.height)
        expect(layout.top + layout.height).toBe(bounds.bottom)

        const midpoint = layout.top + layout.height / 2
        if (layout.height === bounds.minHeight) {
          expect(midpoint).toBe(FLOW_BASELINE)
        } else {
          expect(midpoint).not.toBe(FLOW_BASELINE)
        }

        for (const chip of layout.chips) {
          expect(chip.x).toBeGreaterThanOrEqual(bounds.x)
          expect(chip.x + chip.width).toBeLessThanOrEqual(
            bounds.x + bounds.width,
          )
          expect(chip.y).toBeGreaterThanOrEqual(layout.top)
          expect(chip.y + chip.height).toBeLessThanOrEqual(bounds.bottom)
        }

        if (layout.mode === 'detailed' && layout.chips.length > 0) {
          expect(layout.chips.at(-1)?.y).toBe(416)
        }
      }

      for (let index = 1; index < layouts.length; index += 1) {
        expect(layouts[index].layout.height)
          .toBeGreaterThanOrEqual(layouts[index - 1].layout.height)
        expect(layouts[index].layout.top)
          .toBeLessThanOrEqual(layouts[index - 1].layout.top)
      }
    },
  )

  it('keeps detailed content bottom-aligned across the minimum boundary', () => {
    for (const actor of ['reader', 'sender'] as const) {
      const bounds = ACTOR_GEOMETRY[actor].bounds
      const lastChipY = [1, 2, 3, 7].map((count) =>
        getWorkerActorLayout(actor, bounds, workers(count)).chips.at(-1)?.y
      )

      expect(lastChipY).toEqual([416, 416, 416, 416])
    }
  })

  it('preserves the approved seven-worker footprint in compact mode', () => {
    const bounds = ACTOR_GEOMETRY.sender.bounds
    const seven = getWorkerActorLayout('sender', bounds, workers(7))

    for (const count of [8, 16, 32]) {
      const compact = getWorkerActorLayout(
        'sender',
        bounds,
        workers(count),
      )

      expect(compact.top).toBe(seven.top)
      expect(compact.height).toBe(seven.height)
    }
  })

  it('keeps the reader on the detailed one-to-seven layout', () => {
    const readerWorkers = { ...workers(7), max: 7 }
    const layout = getWorkerActorLayout(
      'reader',
      ACTOR_GEOMETRY.reader.bounds,
      readerWorkers,
    )

    expect(layout.mode).toBe('detailed')
    expect(layout.rows).toBe(7)
    expect(layout.chips.every((chip) => chip.scale === 1)).toBe(true)
  })

  it.each([
    { actor: 'reader', count: 0, displayed: 1, mode: 'detailed', columns: 1, rows: 1, height: 113 },
    { actor: 'reader', count: 1, displayed: 1, mode: 'detailed', columns: 1, rows: 1, height: 113 },
    { actor: 'reader', count: 2, displayed: 2, mode: 'detailed', columns: 2, rows: 1, height: 113 },
    { actor: 'reader', count: 3, displayed: 3, mode: 'detailed', columns: 3, rows: 1, height: 113 },
    { actor: 'reader', count: 7, displayed: 7, mode: 'detailed', columns: 4, rows: 2, height: 158 },
    { actor: 'reader', count: 8, displayed: 8, mode: 'detailed', columns: 4, rows: 2, height: 158 },
    { actor: 'reader', count: 16, displayed: 16, mode: 'detailed', columns: 4, rows: 4, height: 248 },
    { actor: 'reader', count: 32, displayed: 32, mode: 'detailed', columns: 4, rows: 8, height: 428 },
    { actor: 'sender', count: 0, displayed: 1, mode: 'detailed', columns: 1, rows: 1, height: 113 },
    { actor: 'sender', count: 1, displayed: 1, mode: 'detailed', columns: 1, rows: 1, height: 113 },
    { actor: 'sender', count: 2, displayed: 2, mode: 'detailed', columns: 2, rows: 1, height: 113 },
    { actor: 'sender', count: 3, displayed: 3, mode: 'detailed', columns: 3, rows: 1, height: 113 },
    { actor: 'sender', count: 7, displayed: 7, mode: 'detailed', columns: 4, rows: 2, height: 158 },
    { actor: 'sender', count: 8, displayed: 8, mode: 'compact', columns: 8, rows: 1, height: 96 },
    { actor: 'sender', count: 16, displayed: 16, mode: 'compact', columns: 8, rows: 2, height: 124 },
    { actor: 'sender', count: 32, displayed: 32, mode: 'compact', columns: 8, rows: 4, height: 180 },
  ] as const)(
    'preserves portrait $actor output at $count workers',
    ({ actor, count, displayed, mode, columns, rows, height }) => {
      const geometry = createPipelineGeometry({
        orientation: 'portrait',
        readerWorkers: actor === 'reader' ? count : 7,
        senderWorkers: actor === 'sender' ? count : 32,
      })
      const actorGeometry = geometry.actors[actor]
      const bounds = actorGeometry.bounds
      const layout = getWorkerActorLayout(
        actor,
        bounds,
        workers(count),
        'portrait',
      )

      expect(bounds).not.toHaveProperty('minHeight')
      expect(layout).toMatchObject({
        workerCount: displayed,
        mode,
        columns,
        rows,
        height,
      })
      expect(layout.top + layout.height).toBe(bounds.bottom)
      expect(layout.chips).toHaveLength(count)
      expect(bounds.bottom - actorGeometry.metrics.secondary.y)
        .toBeGreaterThanOrEqual(18)
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
