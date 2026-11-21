import type { ReaderWorkerState, SenderWorkerState } from '../model/loadgen'

// Activity includes draining workers; draining totals are intersections, not extra workers.
export function workerStateCounts(states: readonly (ReaderWorkerState | SenderWorkerState)[]) {
  const counts = {
    liveWorkers: states.length,
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
  }
  for (const { activity, draining } of states) {
    switch (activity) {
      case 'idle':
        counts.idleWorkers += 1
        if (draining) counts.drainingIdleWorkers += 1
        break
      case 'reading':
        counts.readingWorkers += 1
        if (draining) counts.drainingReadingWorkers += 1
        break
      case 'blocked':
        counts.blockedWorkers += 1
        if (draining) counts.drainingBlockedWorkers += 1
        break
      case 'inFlight':
        counts.inFlightWorkers += 1
        if (draining) counts.drainingInFlightWorkers += 1
        break
      case 'backoff':
        counts.backoffWorkers += 1
        if (draining) counts.drainingBackoffWorkers += 1
        break
    }
    if (draining) counts.drainingWorkers += 1
  }
  return counts
}
