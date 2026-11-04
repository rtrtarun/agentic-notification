import { useEffect, useMemo, useState } from 'react'

type FeedbackPayload = {
  event_id: string
  action: 'opened' | 'dismissed' | 'replied' | 'ignored'
  note?: string
}

const App = () => {
  const [status, setStatus] = useState<'idle' | 'loading' | 'ok' | 'error'>('idle')
  const [message, setMessage] = useState('')
  const [feedback, setFeedback] = useState<FeedbackPayload>({
    event_id: 'demo-event-1',
    action: 'opened',
    note: 'The dashboard is working.',
  })

  const backendUrl = useMemo(() => import.meta.env.VITE_BACKEND_URL || 'http://localhost:8080', [])
  // Local development only: a key shipped in a browser bundle is public. Use a real login before deploying.
  const apiKey = import.meta.env.VITE_API_KEY

  useEffect(() => {
    const checkHealth = async () => {
      setStatus('loading')
      try {
        const res = await fetch(`${backendUrl}/health`)
        if (!res.ok) throw new Error(`health check failed: ${res.status}`)
        setStatus('ok')
        setMessage('Backend is reachable.')
      } catch (error) {
        setStatus('error')
        setMessage(error instanceof Error ? error.message : 'Unable to reach backend.')
      }
    }

    checkHealth()
  }, [backendUrl])

  const submitFeedback = async () => {
    try {
      const res = await fetch(`${backendUrl}/feedback`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...(apiKey ? { 'X-API-Key': apiKey } : {}) },
        body: JSON.stringify(feedback),
      })

      if (!res.ok) throw new Error(`feedback failed: ${res.status}`)
      setMessage('Feedback submitted successfully.')
    } catch (error) {
      setMessage(error instanceof Error ? error.message : 'Unable to submit feedback.')
    }
  }

  return (
    <main style={{ fontFamily: 'Inter, sans-serif', padding: '2rem', maxWidth: '720px', margin: '0 auto', lineHeight: 1.5 }}>
      <h1>Attention Orchestration Platform</h1>
      <p>Local operator view for service health and feedback capture.</p>

      <section style={{ border: '1px solid #d0d7de', borderRadius: '12px', padding: '1rem', marginBottom: '1rem' }}>
        <h2>Backend status</h2>
        <p><strong>Status:</strong> {status}</p>
        <p>{message}</p>
      </section>

      <section style={{ border: '1px solid #d0d7de', borderRadius: '12px', padding: '1rem' }}>
        <h2>Submit feedback</h2>
        <label style={{ display: 'block', marginBottom: '0.5rem' }}>
          Event ID
          <input
            value={feedback.event_id}
            onChange={(event) => setFeedback({ ...feedback, event_id: event.target.value })}
            style={{ display: 'block', width: '100%', marginTop: '0.25rem', padding: '0.5rem' }}
          />
        </label>

        <label style={{ display: 'block', marginBottom: '0.5rem' }}>
          Action
          <select
            value={feedback.action}
            onChange={(event) => setFeedback({ ...feedback, action: event.target.value as FeedbackPayload['action'] })}
            style={{ display: 'block', width: '100%', marginTop: '0.25rem', padding: '0.5rem' }}
          >
            <option value="opened">opened</option>
            <option value="dismissed">dismissed</option>
            <option value="replied">replied</option>
            <option value="ignored">ignored</option>
          </select>
        </label>

        <label style={{ display: 'block', marginBottom: '0.75rem' }}>
          Note
          <textarea
            value={feedback.note || ''}
            onChange={(event) => setFeedback({ ...feedback, note: event.target.value })}
            style={{ display: 'block', width: '100%', marginTop: '0.25rem', padding: '0.5rem', minHeight: '90px' }}
          />
        </label>

        <button onClick={submitFeedback} style={{ padding: '0.6rem 1rem', cursor: 'pointer' }}>
          Submit feedback
        </button>
      </section>
    </main>
  )
}

export default App
