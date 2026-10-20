// The console harness: renders one console view on made-up data, in a page
// that stands in for the portal shell (dark canvas, the fonts the shell loads).
// Not part of the console's build; `npm run harness` serves it.
//   /?scenario=backlog|empty|unknown|error  &admin=0            the missing tab
//   /?view=subtitles&scenario=library|calm|off|unset|error  &admin=0  the subtitles tab
//   /?view=subtitles&tab=notfound&language=de&q=harbour       a tab (attention|notfound|waiting|way|has), a language, a search
//   /?view=subtitles&picker=1                                  the picker, over the tab
//   /?view=settings&scenario=library|off                       the settings view, its subtitles section on the same data
//   &zh=hans                                                   with the languages en, de, zh-Hans (Chinese, Simplified)
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { Toaster } from '@nalet/design-system'
import '@nalet/design-system/styles.css'
import '../src/app.css'
import { Missing } from '../src/views/Missing'
import { Settings } from '../src/views/Settings'
import { Subtitles, type SubtitlesStart } from '../src/views/Subtitles'
import { mockApi, mockSubtitlesApi, type Scenario, type SubtitleChinese, type SubtitleScenario } from './mock'

const params = new URLSearchParams(window.location.search)
const view = params.get('view') ?? 'missing'
const admin = params.get('admin') !== '0'
const chinese: SubtitleChinese = params.get('zh') === 'hans' ? 'zh-Hans' : 'zh'

function App() {
  if (view === 'settings') {
    return <Settings api={mockSubtitlesApi((params.get('scenario') ?? 'library') as SubtitleScenario, chinese)} admin={admin} />
  }
  if (view === 'subtitles') {
    const api = mockSubtitlesApi((params.get('scenario') ?? 'library') as SubtitleScenario, chinese)
    const start: SubtitlesStart = {
      tab: (params.get('tab') as SubtitlesStart['tab']) ?? undefined,
      language: params.get('language') ?? undefined,
      q: params.get('q') ?? undefined,
      pick: params.get('picker') === '1',
    }
    return <Subtitles api={api} admin={admin} tick={0} onNavigate={() => undefined} start={start} />
  }
  return <Missing api={mockApi((params.get('scenario') ?? 'backlog') as Scenario)} admin={admin} tick={0} />
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <div className="acq">
      <main className="acq__main">
        <App />
      </main>
      <Toaster />
    </div>
  </StrictMode>,
)
