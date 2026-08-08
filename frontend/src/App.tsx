import type { LoadgenAdapter } from './adapters/LoadgenAdapter'
import { LabShell } from './components/LabShell'
import './App.css'

interface AppProps {
  adapter: LoadgenAdapter
}

export default function App({ adapter }: AppProps) {
  return <LabShell adapter={adapter} />
}
