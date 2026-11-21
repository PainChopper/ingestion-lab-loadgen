import { describe, expect, it } from 'vitest'
import { CounterRateCalculator } from './counterRates'
import type { WireSnapshot } from './wireSnapshot'
import { readWireFixture } from '../test/wireFixture'

function nextObservation(): WireSnapshot {
  const wire = readWireFixture()
  return {
    ...wire,
    capturedAtMs: wire.capturedAtMs + 2_500,
    reader: { ...wire.reader, rowsRead: wire.reader.rowsRead + 25 },
    run: { ...wire.run, totalTransactions: wire.run.totalTransactions + 5 },
    readerChannel: {
      ...wire.readerChannel,
      inputTransactionsTotal: wire.readerChannel.inputTransactionsTotal + 20,
      outputTransactionsTotal: wire.readerChannel.outputTransactionsTotal + 15,
    },
    senderChannel: {
      ...wire.senderChannel,
      inputTransactionsTotal: wire.senderChannel.inputTransactionsTotal + 10,
      outputTransactionsTotal: wire.senderChannel.outputTransactionsTotal + 5,
    },
  }
}

const unavailable = {
  readTps: null,
  admittedTps: null,
  readerChannel: { inputTransactionsPerSecond: null, outputTransactionsPerSecond: null },
  senderChannel: { inputTransactionsPerSecond: null, outputTransactionsPerSecond: null },
}

describe('CounterRateCalculator', () => {
  it('uses server time and each stage counter, caching repeated reads of the same observation', () => {
    const calculator = new CounterRateCalculator()
    const first = readWireFixture()
    expect(calculator.observe(first)).toEqual(unavailable)
    const next = nextObservation()
    const rates = calculator.observe(next)
    expect(rates).toEqual({
      readTps: 10,
      admittedTps: 4,
      readerChannel: { inputTransactionsPerSecond: 8, outputTransactionsPerSecond: 6 },
      senderChannel: { inputTransactionsPerSecond: 4, outputTransactionsPerSecond: 2 },
    })
    expect(calculator.observe(next)).toBe(rates)
    expect(calculator.observe({ ...next, capturedAtMs: next.capturedAtMs + 100 })).toEqual({
      readTps: 0,
      admittedTps: 0,
      readerChannel: { inputTransactionsPerSecond: 0, outputTransactionsPerSecond: 0 },
      senderChannel: { inputTransactionsPerSecond: 0, outputTransactionsPerSecond: 0 },
    })
    expect(new CounterRateCalculator().observe(next)).toEqual(unavailable)
  })

  it.each([
    'equal-time',
    'time',
    'rowsRead',
    'totalTransactions',
    'reader-input',
    'reader-output',
    'reader-blocked',
    'sender-input',
    'sender-output',
    'sender-blocked',
  ])('invalidates the entire interval on %s and uses the new baseline', (regression) => {
    const calculator = new CounterRateCalculator()
    const first = readWireFixture()
    calculator.observe(first)
    const next = nextObservation()
    const reset = {
      ...next,
      capturedAtMs:
        regression === 'equal-time'
          ? first.capturedAtMs
          : regression === 'time'
            ? first.capturedAtMs - 1
            : next.capturedAtMs,
      reader: { ...next.reader, rowsRead: regression === 'rowsRead' ? 0 : next.reader.rowsRead },
      run: {
        ...next.run,
        totalTransactions: regression === 'totalTransactions' ? 0 : next.run.totalTransactions,
      },
      readerChannel: {
        ...next.readerChannel,
        inputTransactionsTotal:
          regression === 'reader-input' ? 0 : next.readerChannel.inputTransactionsTotal,
        outputTransactionsTotal:
          regression === 'reader-output' ? 0 : next.readerChannel.outputTransactionsTotal,
        blockedMs: regression === 'reader-blocked' ? 0 : next.readerChannel.blockedMs,
      },
      senderChannel: {
        ...next.senderChannel,
        inputTransactionsTotal:
          regression === 'sender-input' ? 0 : next.senderChannel.inputTransactionsTotal,
        outputTransactionsTotal:
          regression === 'sender-output' ? 0 : next.senderChannel.outputTransactionsTotal,
        blockedMs: regression === 'sender-blocked' ? 0 : next.senderChannel.blockedMs,
      },
    }
    expect(calculator.observe(reset)).toEqual(unavailable)
    expect(
      calculator.observe({
        ...reset,
        capturedAtMs: reset.capturedAtMs + 500,
        reader: { ...reset.reader, rowsRead: reset.reader.rowsRead + 10 },
      }).readTps,
    ).toBe(20)
  })
})
