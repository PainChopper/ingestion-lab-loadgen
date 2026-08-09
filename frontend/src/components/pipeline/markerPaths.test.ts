import { describe, expect, it } from 'vitest'
import type { NumericControlSnapshot, QueueId } from '../../model/loadgen'
import { QUEUE_CABLE_ENDPOINTS } from './geometry'
import { buildQueueCablePath, capacityToCableY } from './queueCableGeometry'
import { getQueueMarkerPathGeometry } from './markerPaths'

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
  it('uses canonical endpoints and changes only with applied capacity', () => {
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
      const topY = capacityToCableY(
        testCase.applied,
        capacityControl(testCase.applied, testCase.max, testCase.step),
        endpoints.start.y,
        240,
      )

      expect(canonical.cablePath).toBe(
        buildQueueCablePath(endpoints.start, endpoints.end, topY),
      )
      expect(pending).toEqual(canonical)
      expect(afterApply).not.toEqual(canonical)
    }
  })
})
