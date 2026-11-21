import type { WireSnapshot } from './wireSnapshot'

interface ChannelRates {
  readonly inputTransactionsPerSecond: number | null
  readonly outputTransactionsPerSecond: number | null
}

export interface CounterRates {
  readonly readTps: number | null
  readonly admittedTps: number | null
  readonly readerChannel: ChannelRates
  readonly senderChannel: ChannelRates
}

function counters(snapshot: WireSnapshot): readonly number[] {
  return [
    snapshot.reader.rowsRead,
    snapshot.run.totalTransactions,
    snapshot.readerChannel.inputTransactionsTotal,
    snapshot.readerChannel.outputTransactionsTotal,
    snapshot.readerChannel.blockedMs,
    snapshot.senderChannel.inputTransactionsTotal,
    snapshot.senderChannel.outputTransactionsTotal,
    snapshot.senderChannel.blockedMs,
  ]
}

// Each adapter owns its baseline. Only newly received wire observations advance it.
export class CounterRateCalculator {
  private previous: WireSnapshot | null = null
  private rates: CounterRates | null = null

  observe(snapshot: WireSnapshot): CounterRates {
    if (snapshot === this.previous) return this.rates!

    const previous = this.previous
    this.previous = snapshot
    const elapsedMs = previous === null ? 0 : snapshot.capturedAtMs - previous.capturedAtMs
    const currentCounters = counters(snapshot)
    const previousCounters = previous === null ? [] : counters(previous)
    const valid =
      previous !== null &&
      elapsedMs > 0 &&
      currentCounters.every((value, index) => value >= previousCounters[index])
    const rate = (value: number, before: number): number | null =>
      valid ? ((value - before) * 1_000) / elapsedMs : null
    const channelRates = (side: 'readerChannel' | 'senderChannel'): ChannelRates => ({
      inputTransactionsPerSecond: rate(
        snapshot[side].inputTransactionsTotal,
        previous?.[side].inputTransactionsTotal ?? 0,
      ),
      outputTransactionsPerSecond: rate(
        snapshot[side].outputTransactionsTotal,
        previous?.[side].outputTransactionsTotal ?? 0,
      ),
    })
    const readerChannel = channelRates('readerChannel')
    const senderChannel = channelRates('senderChannel')
    this.rates = {
      readTps: rate(snapshot.reader.rowsRead, previous?.reader.rowsRead ?? 0),
      admittedTps: senderChannel.inputTransactionsPerSecond,
      readerChannel,
      senderChannel,
    }
    return this.rates
  }
}
