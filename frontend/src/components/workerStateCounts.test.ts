import { describe, expect, it } from 'vitest'
import { workerStateCounts } from './workerStateCounts'

describe('displayed worker counts', () => {
  it('keeps every count zero for an empty list', () => {
    expect(workerStateCounts([])).toEqual({
      liveWorkers: 0,
      idleWorkers: 0,
      readingWorkers: 0,
      blockedWorkers: 0,
      inFlightWorkers: 0,
      backoffWorkers: 0,
      drainingWorkers: 0,
      drainingIdleWorkers: 0,
      drainingReadingWorkers: 0,
      drainingBlockedWorkers: 0,
      drainingInFlightWorkers: 0,
      drainingBackoffWorkers: 0,
    })
  })

  it('counts the owner examples with independent draining intersections', () => {
    expect(
      workerStateCounts([
        { activity: 'reading', draining: false },
        { activity: 'blocked', draining: true },
      ]),
    ).toMatchObject({
      liveWorkers: 2,
      readingWorkers: 1,
      blockedWorkers: 1,
      idleWorkers: 0,
      drainingWorkers: 1,
      drainingReadingWorkers: 0,
      drainingBlockedWorkers: 1,
    })
    expect(
      workerStateCounts([
        { activity: 'inFlight', draining: false },
        { activity: 'backoff', draining: true },
      ]),
    ).toMatchObject({
      liveWorkers: 2,
      inFlightWorkers: 1,
      backoffWorkers: 1,
      idleWorkers: 0,
      drainingWorkers: 1,
      drainingInFlightWorkers: 0,
      drainingBackoffWorkers: 1,
    })
  })

  it('counts all activity/draining pairs and preserves repeated workers', () => {
    const states = Object.freeze([
      Object.freeze({ activity: 'idle' as const, draining: false }),
      Object.freeze({ activity: 'idle' as const, draining: true }),
      Object.freeze({ activity: 'reading' as const, draining: false }),
      Object.freeze({ activity: 'reading' as const, draining: true }),
      Object.freeze({ activity: 'blocked' as const, draining: false }),
      Object.freeze({ activity: 'blocked' as const, draining: true }),
      Object.freeze({ activity: 'inFlight' as const, draining: false }),
      Object.freeze({ activity: 'inFlight' as const, draining: true }),
      Object.freeze({ activity: 'backoff' as const, draining: false }),
      Object.freeze({ activity: 'backoff' as const, draining: true }),
      Object.freeze({ activity: 'backoff' as const, draining: true }),
    ])
    const previous = JSON.stringify(states)
    expect(workerStateCounts(states)).toEqual({
      liveWorkers: 11,
      idleWorkers: 2,
      readingWorkers: 2,
      blockedWorkers: 2,
      inFlightWorkers: 2,
      backoffWorkers: 3,
      drainingWorkers: 6,
      drainingIdleWorkers: 1,
      drainingReadingWorkers: 1,
      drainingBlockedWorkers: 1,
      drainingInFlightWorkers: 1,
      drainingBackoffWorkers: 2,
    })
    expect(JSON.stringify(states)).toBe(previous)
  })
})
