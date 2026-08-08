import type {
  AdapterKind,
  CommandReceipt,
  LoadgenCommand,
  LoadgenSnapshot,
} from '../model/loadgen'

export type LoadgenSnapshotListener = (snapshot: LoadgenSnapshot) => void

export interface LoadgenAdapter {
  readonly kind: AdapterKind
  getSnapshot(): LoadgenSnapshot
  subscribe(listener: LoadgenSnapshotListener): () => void
  dispatch(command: LoadgenCommand): Promise<CommandReceipt>
  dispose(): void
}
