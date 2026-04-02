import assert from 'node:assert/strict'
import test from 'node:test'

type FetchCall = {
  input: RequestInfo | URL
  init?: RequestInit
}

function createLocalStorageMock() {
  const store = new Map<string, string>()
  return {
    getItem(key: string) {
      return store.has(key) ? store.get(key)! : null
    },
    setItem(key: string, value: string) {
      store.set(key, value)
    },
    removeItem(key: string) {
      store.delete(key)
    },
    clear() {
      store.clear()
    },
  }
}

function createSSEBody(lines: string[]): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder()
  return new ReadableStream<Uint8Array>({
    start(controller) {
      for (const line of lines) {
        controller.enqueue(encoder.encode(line))
      }
      controller.close()
    },
  })
}

async function importClientModule() {
  const localStorage = createLocalStorageMock()
  const dispatched: Event[] = []

  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: localStorage,
  })
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: {
      dispatchEvent(event: Event) {
        dispatched.push(event)
        return true
      },
    },
  })

  const moduleUrl = new URL(`../src/api/client.ts?test=${Date.now()}-${Math.random()}`, import.meta.url)
  const client = await import(moduleUrl.href)
  return { client, dispatched }
}

async function waitFor(assertion: () => void, timeoutMs = 1000): Promise<void> {
  const startedAt = Date.now()
  while (true) {
    try {
      assertion()
      return
    } catch (error) {
      if (Date.now() - startedAt > timeoutMs) {
        throw error
      }
      await new Promise(resolve => setTimeout(resolve, 10))
    }
  }
}

test('probeBatchNodes posts to probe-batch and emits SSE events', async () => {
  const fetchCalls: FetchCall[] = []

  Object.defineProperty(globalThis, 'fetch', {
    configurable: true,
    value: async (input: RequestInfo | URL, init?: RequestInit) => {
      fetchCalls.push({ input, init })
      return new Response(
        createSSEBody([
          'data: {"type":"progress","tag":"tag-a","name":"Node A","latency":120,"status":"success","error":"","current":1,"total":1,"progress":100}\n',
          '\n',
        ]),
        { status: 200, headers: { 'Content-Type': 'text/event-stream' } },
      )
    },
  })

  const { client } = await importClientModule()
  const events: unknown[] = []
  const errors: Error[] = []

  client.probeBatchNodes(['tag-a'], (event: unknown) => {
    events.push(event)
  }, (error: Error) => {
    errors.push(error)
  })

  await waitFor(() => {
    assert.equal(fetchCalls.length, 1)
    assert.equal(fetchCalls[0].input, '/api/nodes/probe-batch')
    assert.equal(fetchCalls[0].init?.method, 'POST')
    assert.equal(fetchCalls[0].init?.body, JSON.stringify({ tags: ['tag-a'] }))
    assert.equal(events.length, 1)
  })

  assert.deepEqual(errors, [])
})

test('checkNodeQualityBatch starts, streams, and cancels by job id', async () => {
  const fetchCalls: FetchCall[] = []

  Object.defineProperty(globalThis, 'fetch', {
    configurable: true,
    value: async (input: RequestInfo | URL, init?: RequestInit) => {
      fetchCalls.push({ input, init })

      if (fetchCalls.length === 1) {
        return new Response(
          JSON.stringify({
            job: {
              id: 'job-1',
              status: 'queued',
              started_at: '2026-04-02T00:00:00Z',
              updated_at: '2026-04-02T00:00:00Z',
              total: 2,
              completed: 0,
              success: 0,
              failed: 0,
              active_workers: 0,
            },
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        )
      }

      if (typeof input === 'string' && input.startsWith('/api/nodes/quality-check-batch/stream')) {
        return new Response(
          createSSEBody([
            'data: {"type":"start","job_id":"job-1","status":"running","total":2,"completed":0,"success":0,"failed":0}\n',
            '\n',
          ]),
          { status: 200, headers: { 'Content-Type': 'text/event-stream' } },
        )
      }

      return new Response(
        JSON.stringify({ message: 'cancelled', job_id: 'job-1' }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      )
    },
  })

  const { client } = await importClientModule()
  const events: unknown[] = []
  const errors: Error[] = []

  const controller = client.checkNodeQualityBatch({
    selection: {
      mode: 'names',
      names: ['node-a', 'node-b'],
    },
  }, (event: unknown) => {
    events.push(event)
  }, (error: Error) => {
    errors.push(error)
  })

  await waitFor(() => {
    assert.equal(fetchCalls.length >= 2, true)
    assert.equal(fetchCalls[0].input, '/api/nodes/quality-check-batch')
    assert.equal(fetchCalls[0].init?.body, JSON.stringify({
      selection: {
        mode: 'names',
        names: ['node-a', 'node-b'],
      },
    }))
    assert.equal(fetchCalls[1].input, '/api/nodes/quality-check-batch/stream?job_id=job-1')
    assert.equal(events.length, 1)
  })

  controller.abort()

  await waitFor(() => {
    assert.equal(fetchCalls.length, 3)
    assert.equal(fetchCalls[2].input, '/api/nodes/quality-check-batch/cancel')
    assert.equal(fetchCalls[2].init?.body, JSON.stringify({ job_id: 'job-1' }))
  })

  assert.deepEqual(errors, [])
})
