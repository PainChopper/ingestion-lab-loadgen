import { describe, expect, it } from 'vitest'
import { createPipelineGeometry } from './geometry'
import { getCapacityLegCount, getChannelCapacityGeometryPresentation } from './channelCableGeometry'

describe('channel cable capacity geometry', () => {
  it('adds two legs for each absolute batch-capacity threshold', () => {
    const capacities = [null, 0, 1, 3, 4, 9, 10, 31, 32, 99, 100, 319, 320, 999, 1_000, 8_192]
    expect(capacities.map(getCapacityLegCount)).toEqual([
      0, 0, 2, 2, 4, 4, 6, 6, 8, 8, 10, 10, 12, 12, 14, 14,
    ])
  })

  it.each(['landscape', 'portrait'] as const)(
    'distributes max-capacity %s turns evenly across the free span',
    (orientation) => {
      const start = orientation === 'landscape' ? { x: 150, y: 415 } : { x: 240, y: 260 }
      const end = orientation === 'landscape' ? { x: 355, y: 415 } : { x: 240, y: 520 }
      const geometry = getChannelCapacityGeometryPresentation(8_192, start, end, orientation)

      expect(geometry.legCount).toBe(14)
      expect(geometry.cablePath.match(orientation === 'landscape' ? /V/g : /H/g)).toHaveLength(14)
      expect(geometry.cablePath.startsWith(`M${start.x} ${start.y}`)).toBe(true)
      expect(
        geometry.cablePath.endsWith(orientation === 'landscape' ? `H${end.x}` : `V${end.y}`),
      ).toBe(true)
      const landscape = orientation === 'landscape'
      const entryCorners = geometry.cablePath.matchAll(/Q([\d.]+) ([\d.]+) [\d.]+ [\d.]+ ([VH])/g)
      const turns = Array.from(entryCorners)
        .filter((corner) => corner[3] === (landscape ? 'V' : 'H'))
        .map((corner) => Number(corner[landscape ? 1 : 2]))
      const first = landscape ? start.x + 24 : start.y + 28
      const last = landscape ? end.x - 24 : end.y - 28
      expect(turns).toHaveLength(geometry.legCount)
      turns.forEach((turn, index) => {
        expect(turn).toBeCloseTo(first + ((last - first) * index) / (turns.length - 1), 1)
      })
    },
  )

  it('fits dense landscape geometry down only by even leg counts', () => {
    const legCount = (width: number) =>
      getChannelCapacityGeometryPresentation(
        8_192,
        { x: 0, y: 415 },
        { x: width, y: 415 },
        'landscape',
      ).legCount

    expect([
      legCount(100),
      legCount(92),
      legCount(84),
      legCount(76),
      legCount(68),
      legCount(60),
      legCount(52),
      legCount(48),
    ]).toEqual([14, 12, 10, 8, 6, 4, 2, 0])
  })

  it('uses a direct path for zero capacity in both orientations', () => {
    const landscape = getChannelCapacityGeometryPresentation(
      0,
      { x: 150, y: 415 },
      { x: 355, y: 415 },
    )
    const portrait = getChannelCapacityGeometryPresentation(
      0,
      { x: 240, y: 260 },
      { x: 240, y: 520 },
      'portrait',
    )

    expect(landscape).toMatchObject({ legCount: 0, cablePath: 'M150 415 L355 415' })
    expect(portrait).toMatchObject({ legCount: 0, cablePath: 'M240 260 L240 520' })
  })

  it('builds portrait channels from exact vertical endpoints without overflow', () => {
    const start = { x: 240, y: 268 }
    const end = { x: 240, y: 515 }
    const zero = getChannelCapacityGeometryPresentation(0, start, end, 'portrait')
    const full = getChannelCapacityGeometryPresentation(1_000, start, end, 'portrait')

    expect(zero.cablePath).toBe('M240 268 L240 515')
    expect(full.cablePath.startsWith('M240 268')).toBe(true)
    expect(full.cablePath.endsWith('V515')).toBe(true)
  })

  it.each([0, 6, 12])('routes fixed portrait capacity %s outside the Batch guard', (applied) => {
    const geometry = createPipelineGeometry({
      orientation: 'portrait',
      readerWorkers: 7,
      senderWorkers: 32,
    })
    const channel = geometry.channels['reader-to-throttler']
    const presentation = getChannelCapacityGeometryPresentation(
      applied,
      channel.start,
      channel.end,
      'portrait',
      [geometry.batchLabel.guard],
    )
    const bypassX = geometry.batchLabel.guard.x + geometry.batchLabel.guard.width + 8

    expect(presentation.cablePath).toContain(String(bypassX))
    expect(channel.metrics.waitingY + 12).toBeLessThan(geometry.batchLabel.guard.y)
    expect(channel.end.y - channel.start.y).toBeGreaterThan(300)
  })

  it.each([0, 8, 64, 8_192])(
    'fits portrait channel branches and leaves the label rail clear at capacity %s',
    (applied) => {
      const geometry = createPipelineGeometry({
        orientation: 'portrait',
        readerWorkers: 7,
        senderWorkers: 32,
      })
      for (const channel of Object.values(geometry.channels)) {
        const guard = { x: 70, y: channel.end.y - 196, width: 340, height: 188 }
        const presentation = getChannelCapacityGeometryPresentation(
          applied,
          channel.start,
          channel.end,
          'portrait',
          [guard],
        )
        expect(presentation.legCount).toBe(
          applied === 0 ? 0 : applied === 8 ? 4 : applied === 64 ? 8 : 14,
        )
        const tokens = presentation.cablePath.match(/[MLHVQ]|-?\d+(?:\.\d+)?/g)!
        let point = channel.start
        const assertPoint = (x: number, y: number) => {
          expect(x).toBeGreaterThanOrEqual(24)
          expect(x).toBeLessThanOrEqual(geometry.viewBox.width - 24)
          expect(y).toBeGreaterThanOrEqual(channel.start.y)
          expect(y).toBeLessThanOrEqual(channel.end.y)
          expect(
            x > guard.x && x < guard.x + guard.width && y > guard.y && y < guard.y + guard.height,
          ).toBe(false)
        }
        while (tokens.length > 0) {
          const command = tokens.shift()
          if (command === 'M') {
            point = { x: Number(tokens.shift()), y: Number(tokens.shift()) }
            continue
          }
          const control =
            command === 'Q' ? { x: Number(tokens.shift()), y: Number(tokens.shift()) } : null
          const end =
            command === 'H'
              ? { x: Number(tokens.shift()), y: point.y }
              : command === 'V'
                ? { x: point.x, y: Number(tokens.shift()) }
                : { x: Number(tokens.shift()), y: Number(tokens.shift()) }
          for (let sample = 0; sample <= 20; sample += 1) {
            const t = sample / 20
            const x =
              control === null
                ? point.x + (end.x - point.x) * t
                : (1 - t) ** 2 * point.x + 2 * (1 - t) * t * control.x + t ** 2 * end.x
            const y =
              control === null
                ? point.y + (end.y - point.y) * t
                : (1 - t) ** 2 * point.y + 2 * (1 - t) * t * control.y + t ** 2 * end.y
            assertPoint(x, y)
          }
          point = end
        }
        expect(point).toEqual(channel.end)
      }
    },
  )
})
