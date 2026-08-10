import { describe, expect, it } from 'vitest'
import type { NumericControlSnapshot, QueueId } from '../../model/loadgen'
import { createPipelineGeometry, QUEUE_CABLE_ENDPOINTS } from './geometry'
import { buildQueueCablePath, capacityToCableY } from './queueCableGeometry'
import {
  getMarkerStagePathGeometry,
  getQueueMarkerPathGeometry,
  getValveMarkerPathGeometry,
  pointAtValvePathPhase,
  VALVE_WAITING_STOP_X,
} from './markerPaths'
import type { MarkerStage } from './markerLifecycle'
import {
  VALVE_PISTON,
  valvePistonCenterY,
} from './throttlerValve'

function capacityControl(
  applied: number,
  max: number,
  step: number,
  candidate: number | null = null,
): NumericControlSnapshot {
  return {
    applied,
    preview: candidate,
    pending: candidate,
    min: 0,
    max,
    step,
    unit: 'batches',
    applyMode: 'immediate',
  }
}

describe('marker paths', () => {
  it('joins every lifecycle stage at the exact next endpoint', () => {
    const queue1 = capacityControl(10, 12, 1)
    const queue2 = capacityControl(160, 160, 10)
    const stages: readonly MarkerStage[] = [
      'reader',
      'queue1',
      'throttler',
      'queue2',
      'sender',
      'http',
      'target',
    ]
    const paths = stages.map((stage) =>
      getMarkerStagePathGeometry(stage, queue1, queue2)
    )

    for (let index = 0; index < paths.length - 1; index += 1) {
      expect(paths[index].end).toEqual(paths[index + 1].start)
    }
    expect(paths.every((path) => path.length > 0)).toBe(true)
  })

  it('uses canonical endpoints and follows the committed candidate capacity', () => {
    const cases: ReadonlyArray<{
      queueId: QueueId
      applied: number
      candidate: number
      max: number
      step: number
    }> = [
      {
        queueId: 'reader-to-throttler',
        applied: 10,
        candidate: 0,
        max: 12,
        step: 1,
      },
      {
        queueId: 'throttler-to-sender',
        applied: 160,
        candidate: 50,
        max: 160,
        step: 10,
      },
    ]

    for (const testCase of cases) {
      const endpoints = QUEUE_CABLE_ENDPOINTS[testCase.queueId]
      const canonical = getQueueMarkerPathGeometry(
        testCase.queueId,
        capacityControl(testCase.applied, testCase.max, testCase.step),
      )
      const pending = getQueueMarkerPathGeometry(
        testCase.queueId,
        capacityControl(
          testCase.applied,
          testCase.max,
          testCase.step,
          testCase.candidate,
        ),
      )
      const afterApply = getQueueMarkerPathGeometry(
        testCase.queueId,
        capacityControl(testCase.candidate, testCase.max, testCase.step),
      )
      const appliedTopY = capacityToCableY(
        testCase.applied,
        capacityControl(testCase.applied, testCase.max, testCase.step),
        endpoints.start.y,
        240,
      )
      const candidateTopY = capacityToCableY(
        testCase.candidate,
        capacityControl(testCase.applied, testCase.max, testCase.step),
        endpoints.start.y,
        240,
      )

      expect(canonical.cablePath).toBe(
        buildQueueCablePath(endpoints.start, endpoints.end, appliedTopY),
      )
      expect(pending.cablePath).toBe(
        buildQueueCablePath(endpoints.start, endpoints.end, candidateTopY),
      )
      expect(pending).toEqual(afterApply)
      expect(afterApply).not.toEqual(canonical)
    }
  })

  it('keeps waiting before the flange and routes applied openings around piston', () => {
    const closed = getValveMarkerPathGeometry(0)
    const partial = getValveMarkerPathGeometry(5)
    const open = getValveMarkerPathGeometry(11)
    expect(pointAtValvePathPhase(closed, closed.preAdmissionStopPhase))
      .toEqual({ x: VALVE_WAITING_STOP_X, y: 415 })
    expect(VALVE_WAITING_STOP_X).toBe(397)
    expect(partial.points.some((point) => point.y > 415)).toBe(true)
    expect(open.points.every((point) => point.y === 415)).toBe(true)

    for (const openingIndex of [1, 5, 11]) {
      const geometry = getValveMarkerPathGeometry(openingIndex)
      const pistonY = valvePistonCenterY(openingIndex)
      for (let sample = 0; sample <= 500; sample += 1) {
        const point = pointAtValvePathPhase(geometry, sample / 500)
        if (point.x < 414 || point.x > 446) continue
        const normalized =
          ((point.x - 430) / (VALVE_PISTON.radiusX + 4)) ** 2 +
          ((point.y - pistonY) / (VALVE_PISTON.radiusY + 4)) ** 2
        expect(normalized).toBeGreaterThanOrEqual(1)
      }
    }
  })

  it('joins portrait stages at top and bottom ports through upright valve elbows', () => {
    const queue1 = capacityControl(10, 12, 1)
    const queue2 = capacityControl(160, 160, 10)
    const stages: readonly MarkerStage[] = [
      'reader',
      'queue1',
      'throttler',
      'queue2',
      'sender',
      'http',
      'target',
    ]
    for (const readerWorkers of [1, 7]) {
      for (const senderWorkers of [1, 7, 8, 16, 32]) {
        const geometry = createPipelineGeometry({
          orientation: 'portrait',
          readerWorkers,
          senderWorkers,
        })
        const paths = stages.map((stage) =>
          getMarkerStagePathGeometry(stage, queue1, queue2, 11, geometry)
        )
        for (let index = 0; index < paths.length - 1; index += 1) {
          expect(paths[index].end).toEqual(paths[index + 1].start)
        }
      }
    }

    const geometry = createPipelineGeometry({
      orientation: 'portrait',
      readerWorkers: 7,
      senderWorkers: 32,
    })
    const paths = stages.map((stage) =>
      getMarkerStagePathGeometry(stage, queue1, queue2, 11, geometry)
    )
    expect(paths[1].start).toEqual({ x: 240, y: 246 })
    expect(paths[1].end).toEqual({ x: 240, y: 493 })
    expect(paths[2].start).toEqual({ x: 240, y: 493 })
    expect(paths[2].end).toEqual({ x: 240, y: 686 })
    expect(paths[5].end).toEqual({ x: 240, y: 1258 })

    const valve = getValveMarkerPathGeometry(5, geometry)
    expect(valve.points).toContainEqual({ x: 192, y: 626 })
    expect(valve.points).toContainEqual({ x: 288, y: 626 })
    expect(valve.points.some((point) => point.y > 626)).toBe(true)
  })

  it.each([1120, 1440, 1920])(
    'joins landscape stages at content width %i',
    (landscapeContentWidth) => {
      const queue1 = capacityControl(10, 12, 1)
      const queue2 = capacityControl(160, 160, 10)
      const geometry = createPipelineGeometry({
        orientation: 'landscape',
        landscapeContentWidth,
        readerWorkers: 7,
        senderWorkers: 32,
      })
      const stages: readonly MarkerStage[] = [
        'reader',
        'queue1',
        'throttler',
        'queue2',
        'sender',
        'http',
        'target',
      ]
      const paths = stages.map((stage) =>
        getMarkerStagePathGeometry(stage, queue1, queue2, 11, geometry)
      )

      for (let index = 0; index < paths.length - 1; index += 1) {
        expect(paths[index].end).toEqual(paths[index + 1].start)
      }
    },
  )
})
