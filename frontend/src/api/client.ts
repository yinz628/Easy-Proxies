import type {
  AuthResponse,
  NodesResponse,
  DebugResponse,
  SettingsData,
  SettingsUpdateResponse,
  ConfigNodesResponse,
  ConfigNodePayload,
  ConfigNodeMutationResponse,
  ImportNodesResponse,
  BatchLifecycleAction,
  BatchLifecycleResponse,
  ManageListResponse,
  ManageQuery,
  ManageSelectionRequest,
  SubscriptionStatus,
  SubscriptionImportResponse,
  BatchProbeJob,
  BatchQualityJob,
  ProbeSSEEvent,
  NodeQualityCheckResult,
  QualityCheckBatchEvent,
  TrafficStreamEvent,
} from '../types'

// ---- Token management ----

let authToken: string | null = localStorage.getItem('auth_token')

export function getToken(): string | null {
  return authToken
}

export function setToken(token: string | null) {
  authToken = token
  if (token) {
    localStorage.setItem('auth_token', token)
  } else {
    localStorage.removeItem('auth_token')
  }
}

export function clearToken() {
  setToken(null)
}

// ---- Base request helper ----

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = {
    ...(options.headers as Record<string, string> || {}),
  }

  // Add auth header if we have a token
  if (authToken) {
    headers['Authorization'] = `Bearer ${authToken}`
  }

  // Set JSON content type for non-GET requests with body
  if (options.body && typeof options.body === 'string') {
    headers['Content-Type'] = 'application/json'
  }

  const res = await fetch(path, {
    ...options,
    headers,
    credentials: 'include', // send cookies
  })

  if (res.status === 401) {
    clearToken()
    // Dispatch a custom event so App can react
    window.dispatchEvent(new CustomEvent('auth:unauthorized'))
    throw new ApiError('未授权，请重新登录', 401)
  }

  if (!res.ok) {
    let msg = `HTTP ${res.status}`
    try {
      const body = await res.json()
      if (body.error) msg = body.error
    } catch { /* ignore parse errors */ }
    throw new ApiError(msg, res.status)
  }

  // Handle empty responses
  const text = await res.text()
  if (!text) return {} as T
  return JSON.parse(text) as T
}

export class ApiError extends Error {
  status: number
  constructor(message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

function normalizeSelectionBody(selectionOrNames: ManageSelectionRequest | string[], key: 'names' | 'tags'): ManageSelectionRequest | { tags: string[] } {
  if (Array.isArray(selectionOrNames)) {
    if (key === 'tags') {
      return { tags: selectionOrNames }
    }
    return {
      selection: {
        mode: 'names',
        names: selectionOrNames,
      },
    }
  }
  return selectionOrNames
}

// ---- Auth API ----

/** Check if password is required & login */
export async function checkAuth(): Promise<AuthResponse> {
  // Use GET-like behavior: /api/auth without POST returns password status
  const res = await fetch('/api/auth', { credentials: 'include' })
  return res.json()
}

export async function login(password: string): Promise<AuthResponse> {
  const res = await fetch('/api/auth', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ password }),
    credentials: 'include',
  })

  if (!res.ok) {
    const body = await res.json()
    throw new ApiError(body.error || '登录失败', res.status)
  }

  const data: AuthResponse = await res.json()
  if (data.token) {
    setToken(data.token)
  }
  return data
}

export function logout() {
  clearToken()
}

// ---- Nodes API ----

export async function fetchNodes(): Promise<NodesResponse> {
  return request<NodesResponse>('/api/nodes')
}

export async function fetchManageNodes(query: ManageQuery, signal?: AbortSignal): Promise<ManageListResponse> {
  const params = new URLSearchParams()
  params.set('page', String(query.page))
  params.set('page_size', String(query.page_size))
  if (query.keyword) params.set('keyword', query.keyword)
  if (query.status) params.set('status', query.status)
  if (query.region) params.set('region', query.region)
  if (query.source) params.set('source', query.source)
  if (query.lifecycle_state) params.set('lifecycle_state', query.lifecycle_state)
  if (query.manual_probe_status) params.set('manual_probe_status', query.manual_probe_status)
  if (query.activation_ready) params.set('activation_ready', query.activation_ready)
  if (query.quality_status) params.set('quality_status', query.quality_status)
  params.set('sort_key', query.sort_key)
  params.set('sort_dir', query.sort_dir)

  return request<ManageListResponse>(`/api/nodes/manage?${params.toString()}`, { signal })
}

export async function probeNode(tag: string): Promise<{ message: string; latency_ms: number }> {
  const response = await request<{ message?: string; latency_ms?: number; error?: string }>(`/api/nodes/${encodeURIComponent(tag)}/probe`, { method: 'POST' })
  if (response.error) {
    throw new Error(response.error)
  }
  return {
    message: response.message || '探测成功',
    latency_ms: response.latency_ms ?? -1,
  }
}

export async function releaseNode(tag: string): Promise<{ message: string }> {
  return request(`/api/nodes/${encodeURIComponent(tag)}/release`, { method: 'POST' })
}

export async function checkNodeQuality(tag: string): Promise<{ message: string; result: NodeQualityCheckResult }> {
  return request(`/api/nodes/${encodeURIComponent(tag)}/quality-check`, { method: 'POST' })
}

export async function getNodeQuality(tag: string): Promise<{ result: NodeQualityCheckResult }> {
  return request(`/api/nodes/${encodeURIComponent(tag)}/quality-check`)
}

/** Probe all nodes with SSE progress updates */
export function probeAllNodes(
  onEvent: (event: ProbeSSEEvent) => void,
  onError?: (error: Error) => void
): AbortController {
  const controller = new AbortController()

  const doFetch = async () => {
    try {
      const headers: Record<string, string> = {}
      if (authToken) {
        headers['Authorization'] = `Bearer ${authToken}`
      }

      const res = await fetch('/api/nodes/probe-all', {
        method: 'POST',
        headers,
        credentials: 'include',
        signal: controller.signal,
      })

      if (!res.ok) {
        throw new ApiError(`探测失败: HTTP ${res.status}`, res.status)
      }

      const reader = res.body?.getReader()
      if (!reader) throw new Error('No response body')

      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        for (const line of lines) {
          const trimmed = line.trim()
          if (trimmed.startsWith('data: ')) {
            try {
              const data = JSON.parse(trimmed.slice(6)) as ProbeSSEEvent
              onEvent(data)
            } catch { /* skip malformed events */ }
          }
        }
      }
    } catch (err) {
      if ((err as Error).name !== 'AbortError') {
        onError?.(err as Error)
      }
    }
  }

  doFetch()
  return controller
}

// ---- Traffic Stream API ----

/** Subscribe real-time traffic speeds via SSE */
export function streamTraffic(
  onEvent: (event: TrafficStreamEvent) => void,
  onError?: (error: Error) => void
): AbortController {
  const controller = new AbortController()

  const doFetch = async () => {
    try {
      const headers: Record<string, string> = {}
      if (authToken) {
        headers['Authorization'] = `Bearer ${authToken}`
      }

      const res = await fetch('/api/nodes/traffic/stream', {
        method: 'GET',
        headers,
        credentials: 'include',
        signal: controller.signal,
      })

      if (!res.ok) {
        throw new ApiError(`流量流订阅失败: HTTP ${res.status}`, res.status)
      }

      const reader = res.body?.getReader()
      if (!reader) throw new Error('No response body')

      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        for (const line of lines) {
          const trimmed = line.trim()
          if (trimmed.startsWith('data: ')) {
            try {
              const data = JSON.parse(trimmed.slice(6)) as TrafficStreamEvent
              if (data.type === 'traffic') {
                onEvent(data)
              }
            } catch { /* skip malformed events */ }
          }
        }
      }
    } catch (err) {
      if ((err as Error).name !== 'AbortError') {
        onError?.(err as Error)
      }
    }
  }

  doFetch()
  return controller
}

// ---- Debug API ----

export async function fetchDebug(): Promise<DebugResponse> {
  return request<DebugResponse>('/api/debug')
}

// ---- Settings API ----

export async function fetchSettings(): Promise<SettingsData> {
  return request<SettingsData>('/api/settings')
}

export async function updateSettings(settings: SettingsData): Promise<SettingsUpdateResponse> {
  return request<SettingsUpdateResponse>('/api/settings', {
    method: 'PUT',
    body: JSON.stringify(settings),
  })
}

// ---- Config Nodes CRUD API ----

export async function fetchConfigNodes(): Promise<ConfigNodesResponse> {
  return request<ConfigNodesResponse>('/api/nodes/config')
}

export async function createConfigNode(payload: ConfigNodePayload): Promise<ConfigNodeMutationResponse> {
  return request<ConfigNodeMutationResponse>('/api/nodes/config', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export async function updateConfigNode(name: string, payload: ConfigNodePayload): Promise<ConfigNodeMutationResponse> {
  return request<ConfigNodeMutationResponse>(`/api/nodes/config/${encodeURIComponent(name)}`, {
    method: 'PUT',
    body: JSON.stringify(payload),
  })
}

export async function deleteConfigNode(name: string): Promise<ConfigNodeMutationResponse> {
  return request<ConfigNodeMutationResponse>(`/api/nodes/config/${encodeURIComponent(name)}`, {
    method: 'DELETE',
  })
}

export async function toggleConfigNode(name: string, enabled: boolean): Promise<ConfigNodeMutationResponse> {
  return request<ConfigNodeMutationResponse>(`/api/nodes/config/${encodeURIComponent(name)}`, {
    method: 'PATCH',
    body: JSON.stringify({ enabled }),
  })
}

export async function batchToggleConfigNodes(selectionOrNames: ManageSelectionRequest | string[], enabled: boolean): Promise<{ message: string; success: number; total: number; errors?: string[] }> {
  return request('/api/nodes/config/batch-toggle', {
    method: 'POST',
    body: JSON.stringify({ ...normalizeSelectionBody(selectionOrNames, 'names'), enabled }),
  })
}

export async function batchLifecycleConfigNodes(selectionOrNames: ManageSelectionRequest | string[], action: BatchLifecycleAction): Promise<BatchLifecycleResponse> {
  return request('/api/nodes/config/batch-lifecycle', {
    method: 'POST',
    body: JSON.stringify({ ...normalizeSelectionBody(selectionOrNames, 'names'), action }),
  })
}

export async function batchDeleteConfigNodes(selectionOrNames: ManageSelectionRequest | string[]): Promise<{ message: string; success: number; total: number; errors?: string[] }> {
  return request('/api/nodes/config/batch-delete', {
    method: 'POST',
    body: JSON.stringify(normalizeSelectionBody(selectionOrNames, 'names')),
  })
}

// ---- Reload API ----

export async function triggerReload(): Promise<{ message: string }> {
  return request('/api/reload', { method: 'POST' })
}

// ---- Subscription API ----

export async function fetchSubscriptionStatus(): Promise<SubscriptionStatus> {
  const response = await request<SubscriptionStatus>('/api/subscription/status')
  return {
    ...response,
    node_count: response.staged_count ?? response.node_count,
  }
}

export async function refreshLegacySubscriptions(): Promise<SubscriptionImportResponse> {
  const response = await request<SubscriptionImportResponse>('/api/subscription/refresh-legacy', { method: 'POST' })
  return { ...response, node_count: response.staged_count }
}

export async function refreshTXTSubscriptions(): Promise<SubscriptionImportResponse> {
  const response = await request<SubscriptionImportResponse>('/api/subscription/refresh-txt', { method: 'POST' })
  return { ...response, node_count: response.staged_count }
}

export function probeBatchNodes(
  tags: string[],
  onEvent: (event: ProbeSSEEvent) => void,
  onError?: (error: Error) => void
): AbortController {
  const controller = new AbortController()

  const doFetch = async () => {
    try {
      const headers: Record<string, string> = {
        'Content-Type': 'application/json',
      }
      if (authToken) {
        headers['Authorization'] = `Bearer ${authToken}`
      }

      const res = await fetch('/api/nodes/probe-batch', {
        method: 'POST',
        headers,
        body: JSON.stringify({ tags }),
        credentials: 'include',
        signal: controller.signal,
      })
      if (!res.ok) {

        let message = `批量探测失败: HTTP ${res.status}`
        try {
          const body = await res.json()
          if (body.error) message = body.error
        } catch { /* ignore parse errors */ }
        throw new ApiError(message, res.status)
      }

      const reader = res.body?.getReader()
      if (!reader) throw new Error('No response body')

      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        for (const line of lines) {
          const trimmed = line.trim()
          if (trimmed.startsWith('data: ')) {
            try {
              const data = JSON.parse(trimmed.slice(6)) as ProbeSSEEvent
              onEvent(data)
            } catch { /* skip malformed events */ }
          }
        }
      }
    } catch (err) {
      if ((err as Error).name !== 'AbortError') {
        onError?.(err as Error)
      }
    }
  }

  doFetch()
  return controller
}

export async function startProbeBatchJob(selectionOrTags: ManageSelectionRequest | string[]): Promise<{ job: BatchProbeJob }> {
  return request('/api/nodes/probe-batch/start', {
    method: 'POST',
    body: JSON.stringify(normalizeSelectionBody(selectionOrTags, 'tags')),
  })
}

export async function fetchProbeBatchJobStatus(): Promise<{ job: BatchProbeJob | null }> {
  return request('/api/nodes/probe-batch/status')
}

export async function cancelProbeBatchJob(jobId: string): Promise<{ message: string; job_id: string }> {
  return request('/api/nodes/probe-batch/cancel', {
    method: 'POST',
    body: JSON.stringify({ job_id: jobId }),
  })
}

export async function startQualityBatchJob(selectionOrTags: ManageSelectionRequest | string[]): Promise<{ job: BatchQualityJob }> {
  return request('/api/nodes/quality-check-batch', {
    method: 'POST',
    body: JSON.stringify(normalizeSelectionBody(selectionOrTags, 'tags')),
  })
}

async function readQualityBatchStream(
  jobId: string,
  controller: AbortController,
  onEvent: (event: QualityCheckBatchEvent) => void,
  onError?: (error: Error) => void,
): Promise<void> {
  try {
    const headers: Record<string, string> = {}
    if (authToken) {
      headers['Authorization'] = `Bearer ${authToken}`
    }

    const res = await fetch(`/api/nodes/quality-check-batch/stream?job_id=${encodeURIComponent(jobId)}`, {
      method: 'GET',
      headers,
      credentials: 'include',
      signal: controller.signal,
    })

    if (!res.ok) {
      let message = `批量质量检测任务流订阅失败: HTTP ${res.status}`
      try {
        const text = await res.text()
        if (text) message = text
      } catch { /* ignore parse errors */ }
      throw new ApiError(message, res.status)
    }

    const reader = res.body?.getReader()
    if (!reader) throw new Error('No response body')

    const decoder = new TextDecoder()
    let buffer = ''

    while (true) {
      const { done, value } = await reader.read()
      if (done) break

      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''

      for (const line of lines) {
        const trimmed = line.trim()
        if (trimmed.startsWith('data: ')) {
          try {
            const data = JSON.parse(trimmed.slice(6)) as QualityCheckBatchEvent
            onEvent(data)
          } catch { /* skip malformed events */ }
        }
      }
    }
  } catch (err) {
    if ((err as Error).name !== 'AbortError') {
      onError?.(err as Error)
    }
  }
}

export async function fetchQualityBatchJobStatus(): Promise<{ job: BatchQualityJob | null }> {
  return request('/api/nodes/quality-check-batch/status')
}

export async function cancelQualityBatchJob(jobId: string): Promise<{ message: string; job_id: string }> {
  return request('/api/nodes/quality-check-batch/cancel', {
    method: 'POST',
    body: JSON.stringify({ job_id: jobId }),
  })
}

export function streamQualityBatchJob(
  jobId: string,
  onEvent: (event: QualityCheckBatchEvent) => void,
  onError?: (error: Error) => void
): AbortController {
  const controller = new AbortController()
  void readQualityBatchStream(jobId, controller, onEvent, onError)
  return controller
}

export function checkNodeQualityBatch(
  selectionOrTags: ManageSelectionRequest | string[],
  onEvent: (event: QualityCheckBatchEvent) => void,
  onError?: (error: Error) => void
): AbortController {
  const controller = new AbortController()
  let jobId: string | null = null
  let cancelRequested = false

  controller.signal.addEventListener('abort', () => {
    if (cancelRequested || !jobId) return
    cancelRequested = true
    void cancelQualityBatchJob(jobId).catch(() => {})
  })

  const doFetch = async () => {
    try {
      const start = await startQualityBatchJob(selectionOrTags)
      jobId = start.job.id
      void readQualityBatchStream(jobId, controller, onEvent, onError)
      return
      /*


      if (!res.ok) {
        let message = `鎵归噺璐ㄩ噺妫€鏌ュけ璐? HTTP ${res.status}`
        try {
          const body = await res.json()
          if (body.error) message = body.error
        } catch {}
        throw new ApiError(message, res.status)
      }

      const reader = res.body?.getReader()
      if (!reader) throw new Error('No response body')

      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        for (const line of lines) {
          const trimmed = line.trim()
          if (trimmed.startsWith('data: ')) {
            try {
              const data = JSON.parse(trimmed.slice(6)) as QualityCheckBatchEvent
              onEvent(data)
            } catch {}
          }
        }
      */
    } catch (err) {
      if ((err as Error).name !== 'AbortError') {
        onError?.(err as Error)
      }
    }
  }

  doFetch()
  return controller
}

export async function refreshSubscriptionFeed(feedKey: string): Promise<SubscriptionImportResponse> {
  const response = await request<SubscriptionImportResponse>('/api/subscription/refresh-feed', {
    method: 'POST',
    body: JSON.stringify({ feed_key: feedKey }),
  })
  return { ...response, node_count: response.staged_count }
}

// ---- Export API ----

export async function exportProxies(): Promise<string> {
  const headers: Record<string, string> = {}
  if (authToken) {
    headers['Authorization'] = `Bearer ${authToken}`
  }
  const res = await fetch('/api/export', {
    headers,
    credentials: 'include',
  })
  if (!res.ok) throw new ApiError('导出失败', res.status)
  return res.text()
}

export async function exportSelectedProxies(selection: ManageSelectionRequest): Promise<string> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  }
  if (authToken) {
    headers['Authorization'] = `Bearer ${authToken}`
  }
  const res = await fetch('/api/export', {
    method: 'POST',
    headers,
    body: JSON.stringify(selection),
    credentials: 'include',
  })
  if (!res.ok) throw new ApiError('导出选中节点失败', res.status)
  return res.text()
}

// ---- Import API ----

export async function importNodes(content: string, namePrefix: string): Promise<ImportNodesResponse> {
  return request('/api/import', {
    method: 'POST',
    body: JSON.stringify({
      content,
      name_prefix: namePrefix,
    }),
  })
}
