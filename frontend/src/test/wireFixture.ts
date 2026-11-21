import { readFileSync } from 'node:fs'
import { URL as NodeURL } from 'node:url'

export function readWireFixture() {
  return JSON.parse(
    readFileSync(new NodeURL('../../../loadgen/testdata/snapshot.json', import.meta.url), 'utf8'),
  )
}

export function readConfigFixture() {
  return JSON.parse(
    readFileSync(new NodeURL('../../../loadgen/testdata/config.json', import.meta.url), 'utf8'),
  )
}
