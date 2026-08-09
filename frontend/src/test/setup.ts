import { cleanup } from '@testing-library/react'
import { afterEach, vi } from 'vitest'

afterEach(() => {
  cleanup()
})

Object.defineProperty(window, 'matchMedia', {
  configurable: true,
  value: vi.fn().mockImplementation((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    addListener: vi.fn(),
    removeListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
})

Object.defineProperty(window, 'requestAnimationFrame', {
  configurable: true,
  writable: true,
  value: vi.fn(() => 1),
})

Object.defineProperty(window, 'cancelAnimationFrame', {
  configurable: true,
  writable: true,
  value: vi.fn(),
})

Object.defineProperties(SVGSVGElement.prototype, {
  getScreenCTM: {
    configurable: true,
    value: () => ({ inverse: () => ({}) }),
  },
  createSVGPoint: {
    configurable: true,
    value: () => {
      const point = {
        x: 0,
        y: 0,
        matrixTransform: () => ({ x: point.x, y: point.y }),
      }
      return point
    },
  },
})

const capturedPointers = new WeakMap<Element, Set<number>>()

Object.defineProperties(SVGElement.prototype, {
  setPointerCapture: {
    configurable: true,
    value(pointerId: number) {
      const pointers = capturedPointers.get(this) ?? new Set<number>()
      pointers.add(pointerId)
      capturedPointers.set(this, pointers)
    },
  },
  hasPointerCapture: {
    configurable: true,
    value(pointerId: number) {
      return capturedPointers.get(this)?.has(pointerId) ?? false
    },
  },
  releasePointerCapture: {
    configurable: true,
    value(pointerId: number) {
      capturedPointers.get(this)?.delete(pointerId)
    },
  },
})
