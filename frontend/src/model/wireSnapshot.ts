import { z } from 'zod/mini'

const wireInteger = z.int().check(z.nonnegative())

const configSchema = z.strictObject({
  readerWorkersMax: wireInteger.check(z.positive()),
  senderWorkersMax: wireInteger.check(z.positive()),
  requestedTpsMax: wireInteger.check(z.positive()),
  readBatchSize: wireInteger.check(z.positive()),
  readerChannelCapacity: wireInteger,
  senderChannelCapacity: wireInteger,
  sourcePath: z.string(),
})

const sourceErrorSchema = z.strictObject({
  operation: z.string(),
  path: z.string(),
  message: z.string(),
})

const channelSchema = z.strictObject({
  depthBatches: wireInteger,
  blockedSenders: wireInteger,
  oldestBlockedSenderMs: wireInteger,
  blockedMs: wireInteger,
  inputTransactionsTotal: wireInteger,
  outputTransactionsTotal: wireInteger,
})

export const wireSnapshotSchema = z.strictObject({
  capturedAtMs: wireInteger,
  run: z.strictObject({
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
    rowsRead: wireInteger,
    sourceError: z.nullable(sourceErrorSchema),
  }),
  throttler: z.strictObject({
    requestedTps: wireInteger,
    installed: z.boolean(),
  }),
  sender: z.strictObject({
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
