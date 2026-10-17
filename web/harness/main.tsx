// The console harness: renders one console view on made-up data, in a page
// that stands in for the portal shell (dark canvas, the fonts the shell loads).
// Not part of the console's build; `npm run harness` serves it.
//   /?scenario=backlog|empty|unknown|error  &admin=0
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { Toaster } from '@nalet/design-system'
import '@nalet/design-system/styles.css'
import '../src/app.css'
import { Missing } from '../src/views/Missing'
import { mockApi, type Scenario } from './mock'

const params = new URLSearchParams(window.location.search)
const scenario = (params.get('scenario') ?? 'backlog') as Scenario
const admin = params.get('admin') !== '0'
const api = mockApi(scenario)

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <div className="acq">
      <main className="acq__main">
        <Missing api={api} admin={admin} tick={0} />
      </main>
      <Toaster />
    </div>
  </StrictMode>,
)
