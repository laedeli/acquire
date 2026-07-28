// Live updates from acquire's SSE endpoint.
//
// The stream is authenticated, so it is read with fetch-streaming rather than
// EventSource (which cannot send an Authorization header). It carries two kinds
// of message: a bare `changed` ping meaning "refetch the lists", and `download`
// events carrying one telemetry row to apply in place — so a progress bar moves
// smoothly without refetching anything.
import { useEffect, useRef } from 'react'
import { type Download } from './api'

type Handlers = {
  onDownload: (d: Download) => void
  onChanged: () => void
  onReconnect: () => void
}

export function useEventStream(apiBase: string, token: string | undefined, handlers: Handlers) {
  // Keep the newest callbacks without restarting the stream on every render.
  const ref = useRef(handlers)
  ref.current = handlers

  useEffect(() => {
    if (!token) return
    const ctrl = new AbortController()
    let stopped = false

    async function run() {
      let attempt = 0
      while (!stopped) {
        try {
          const res = await fetch(apiBase + 'api/events', {
            headers: { Authorization: 'Bearer ' + token, Accept: 'text/event-stream' },
            signal: ctrl.signal,
          })
          if (!res.ok || !res.body) throw new Error('stream ' + res.status)
          attempt = 0
          const reader = res.body.getReader()
          const decoder = new TextDecoder()
          let buf = ''
          for (;;) {
            const { value, done } = await reader.read()
            if (done) break
            buf += decoder.decode(value, { stream: true })
            let i: number
            // SSE frames are separated by a blank line; a chunk can split one.
            while ((i = buf.indexOf('\n\n')) >= 0) {
              const frame = buf.slice(0, i)
              buf = buf.slice(i + 2)
              let name = 'message'
              let data = ''
              for (const line of frame.split('\n')) {
                if (line.startsWith('event: ')) name = line.slice(7).trim()
                else if (line.startsWith('data: ')) data += line.slice(6)
                // ': comment' frames (connected / heartbeat) are ignored.
              }
              if (name === 'download' && data) {
                try {
                  ref.current.onDownload(JSON.parse(data) as Download)
                } catch {
                  /* a malformed frame must not kill the stream */
                }
              } else if (name === 'changed') {
                ref.current.onChanged()
              }
            }
          }
        } catch {
          if (stopped) return
        }
        if (stopped) return
        // Reconnect with backoff, then resync whatever we missed while away.
        const wait = Math.min(30_000, 1000 * 2 ** Math.min(attempt++, 5))
        await new Promise((r) => setTimeout(r, wait))
        if (!stopped) ref.current.onReconnect()
      }
    }
    void run()
    return () => {
      stopped = true
      ctrl.abort()
    }
  }, [apiBase, token])
}

/** debounce collapses a burst of stream pings into one refetch. */
export function debounce<T extends (...args: never[]) => void>(fn: T, ms: number) {
  let t: ReturnType<typeof setTimeout> | undefined
  return (...args: Parameters<T>) => {
    if (t) clearTimeout(t)
    t = setTimeout(() => fn(...args), ms)
  }
}
