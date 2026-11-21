import { z } from 'zod/mini'

const wireInteger = z.int().check(z.nonnegative())

const workersConfigSchema = z
  .strictObject({
    initial: wireInteger.check(z.positive()),
    max: wireInteger.check(z.positive()),
  })
  .check(
    z.refine(({ initial, max }) => initial <= max, {
      path: ['initial'],
      error: 'Expected initial within 1..max',
    }),
  )

const tpsConfigSchema = z
  .strictObject({
    initial: wireInteger,
    max: wireInteger.check(z.positive()),
    step: wireInteger.check(z.positive()),
  })
  .check(
    z.refine(({ initial, max, step }) => initial <= max && initial % step === 0, {
      path: ['initial'],
      error: 'Expected initial within 0..max and on the step grid',
    }),
  )

const configSchema = z.strictObject({
  readerWorkers: workersConfigSchema,
  senderWorkers: workersConfigSchema,
  throttlerRequestedTps: tpsConfigSchema,
})

const sourceErrorSchema = z.object({
  category: z.string(),
  operation: z.string(),
  relativePath: z.string(),
  message: z.string(),
})

const channelSchema = z.strictObject({
  capacity: wireInteger,
  depthBatches: wireInteger,
  bufferedTransactions: wireInteger,
  blockedSenders: wireInteger,
  oldestBlockedSenderMs: wireInteger,
  blockedMs: wireInteger,
  inputTransactionsTotal: wireInteger,
  outputTransactionsTotal: wireInteger,
})

export const wireSnapshotSchema = z.object({
  capturedAtMs: wireInteger,
  run: z.object({
    state: z.enum(['starting', 'running', 'stopping', 'faulted']),
    elapsedMs: wireInteger,
    totalTransactions: wireInteger,
  }),
  reader: z.strictObject({
    workers: wireInteger,
    workerStates: z.array(
      z.strictObject({
        activity: z.enum(['idle', 'reading', 'blocked']),
        draining: z.boolean(),
      }),
    ),
    readBatchSize: wireInteger.check(z.positive()),
    rowsRead: wireInteger,
    sourceDirectory: z.string(),
    sourceError: z.nullable(sourceErrorSchema),
  }),
  throttler: z.strictObject({
    requestedTps: wireInteger,
    installed: z.boolean(),
  }),
  sender: z.object({
    workers: wireInteger,
    workerStates: z.array(
      z.strictObject({
        activity: z.enum(['idle', 'inFlight', 'backoff']),
        draining: z.boolean(),
      }),
    ),
  }),
  readerChannel: channelSchema,
  senderChannel: channelSchema,
  config: configSchema,
})

export type WireSnapshot = z.infer<typeof wireSnapshotSchema>
