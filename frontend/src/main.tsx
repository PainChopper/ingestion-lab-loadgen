import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { HttpAdapter } from './adapters/HttpAdapter.ts'
import { SimulationAdapter } from './adapters/SimulationAdapter.ts'

const adapter = import.meta.env.MODE === 'backend' ? new HttpAdapter() : new SimulationAdapter()

window.addEventListener('beforeunload', adapter.dispose, { once: true })

const root = createRoot(document.getElementById('root')!)

if (import.meta.hot) {
  import.meta.hot.dispose(() => {
    root.unmount()
    window.removeEventListener('beforeunload', adapter.dispose)
    adapter.dispose()
  })
}

root.render(
  <StrictMode>
    <App adapter={adapter} />
  </StrictMode>,
)
