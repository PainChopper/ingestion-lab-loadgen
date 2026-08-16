import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { HttpAdapter } from './adapters/HttpAdapter.ts'
import { SimulationAdapter } from './adapters/SimulationAdapter.ts'

const adapter = import.meta.env.MODE === 'backend'
  ? new HttpAdapter()
  : new SimulationAdapter()

window.addEventListener('beforeunload', adapter.dispose, { once: true })

if (import.meta.hot) {
  import.meta.hot.dispose(adapter.dispose)
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App adapter={adapter} />
  </StrictMode>,
)
