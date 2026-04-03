import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ChangeEvent, FormEvent } from 'react'

import {
  batchLifecycleConfigNodes,
  batchDeleteConfigNodes,
  cancelQualityBatchJob,
  cancelProbeBatchJob,
  checkNodeQuality,
  createConfigNode,
  deleteConfigNode,
  exportProxies,
  exportSelectedProxies,
  fetchManageNodes,
  fetchQualityBatchJobStatus,
  fetchProbeBatchJobStatus,
  getNodeQuality,
  importNodes,
  probeNode,
  releaseNode,
  startQualityBatchJob,
  streamQualityBatchJob,
  startProbeBatchJob,
  toggleConfigNode,
  triggerReload,
  updateConfigNode,
} from '../api/client'
import type {
  ActivationReadyFilter,
  BatchLifecycleAction,
  BatchProbeJob,
  ConfigNodePayload,
  ManageListResponse,
  ManageNodeRow,
  ManageQuery,
  ManualProbeStatus,
  NodeLifecycleState,
  ImportNodesResponse,
  ManageSortDir,
  ManageSortKey,
  ManageStatus,
  NodeQualityCheckResult,
  SelectionState,
} from '../types'
import {
  applyQualityResultToManageList,
  buildQualityResultFromBatchProgress,
  buildQualityResultFromJobResult,
  buildQualityCacheEntry,
  mergeQualityJobSnapshot,
  reduceBatchQualityEvent,
} from './managePanelQuality.ts'
import type { BatchQualityState } from './managePanelQuality.ts'
import {
  buildManageFilterSnapshot,
  buildManageQueryKey,
  normalizeManageQuery,
  resolveManageResponsePage,
} from './managePanelQuery.ts'
import {
  buildSelectionRequest,
  getPageSelectionState,
  getSelectionCount,
  isNodeSelected,
  toggleNodeSelection,
  togglePageSelection,
} from './managePanelSelection.ts'

const emptyPayload: ConfigNodePayload = {
  name: '',
  uri: '',
  port: 0,
  username: '',
  password: '',
}

const emptySummary = {
  normal: 0,
  pending: 0,
  unavailable: 0,
  blacklisted: 0,
  disabled: 0,
}

const maxManagePageCacheEntries = 8

function createEmptySelection(): SelectionState {
  return { mode: 'names', names: new Set() }
}

function trimManagePageCache(cache: Map<string, ManageListResponse>) {
  while (cache.size > maxManagePageCacheEntries) {
    const oldestKey = cache.keys().next().value
    if (!oldestKey) return
    cache.delete(oldestKey)
  }
}

function latencyColor(ms: number): string {
  if (ms < 0) return 'text-base-content/50'
  if (ms <= 100) return 'text-success'
  if (ms <= 300) return 'text-warning'
  return 'text-error'
}

function regionFlag(region?: string): string {
  const flags: Record<string, string> = {
    hk: '🇭🇰', jp: '🇯🇵', kr: '🇰🇷', us: '🇺🇸', tw: '🇹🇼',
    sg: '🇸🇬', de: '🇩🇪', gb: '🇬🇧', fr: '🇫🇷', ca: '🇨🇦',
    au: '🇦🇺', in: '🇮🇳', br: '🇧🇷', ru: '🇷🇺', nl: '🇳🇱',
  }
  return flags[region?.toLowerCase() || ''] || '🌐'
}

function sourceLabel(source?: string): string {
  switch (source) {
    case 'inline':
      return '配置文件'
    case 'nodes_file':
      return '节点文件'
    case 'subscription':
      return '订阅'
    case 'manual':
      return '手动添加'
    default:
      return source || '-'
  }
}

function qualityStatusTone(status?: string): string {
  switch (status) {
    case 'dual_available':
    case 'pass':
      return 'badge-success border-none bg-success/15 text-success'
    case 'openai_only':
      return 'badge-info border-none bg-info/15 text-info'
    case 'anthropic_only':
      return 'badge-warning border-none bg-warning/15 text-warning-content'
    case 'fail':
    case 'failed':
    case 'unavailable':
      return 'badge-error border-none bg-error/15 text-error'
    default:
      return 'badge-ghost border-none bg-base-300/40 text-base-content/50'
  }
}

function qualityStatusLabel(status?: string): string {
  if (status === 'dual_available') return '双通'
  if (status === 'openai_only') return '仅 OpenAI'
  if (status === 'anthropic_only') return '仅 Anthropic'
  if (status === 'unavailable') return '不可用'
  if (status === 'unchecked') return '未检测'
  if (status === 'pass') return '通过'
  if (status === 'fail' || status === 'failed') return '失败'
  if (status === 'queued') return '排队中'
  if (status === 'running') return '检测中'
  if (status === 'completed') return '已完成'
  if (status === 'cancelled') return '已取消'

  switch (status) {
    case 'dual_available':
      return '双通'
    case 'openai_only':
      return '仅 OpenAI'
    case 'anthropic_only':
      return '仅 Anthropic'
    case 'unavailable':
      return '不可用'
    case 'unchecked':
      return '未检测'
    case 'pass':
      return '良好'
    case 'warn':
    case 'degraded':
      return '警告'
    case 'fail':
    case 'failed':
    case 'unhealthy':
      return '失败'
    default:
      return status || '未检测'
  }
}

function isBatchQualityActive(status?: BatchQualityState['status'] | null): boolean {
  return status === 'queued' || status === 'running'
}

function isBatchQualityTerminal(status?: BatchQualityState['status'] | null): boolean {
  return status === 'completed' || status === 'failed' || status === 'cancelled'
}

function formatQualityChecked(epochSeconds?: number): string {
  if (!epochSeconds) return '未检测'
  return new Date(epochSeconds * 1000).toLocaleString()
}

function formatQualityCheckedAt(iso?: string): string {
  if (!iso) return '未检测'
  return new Date(iso).toLocaleString()
}

function manualProbeTone(status: ManageNodeRow['manual_probe_status']): string {
  switch (status) {
    case 'pass':
      return 'badge-success border-none bg-success/15 text-success'
    case 'timeout':
      return 'badge-warning border-none bg-warning/20 text-warning-content'
    case 'fail':
      return 'badge-error border-none bg-error/15 text-error'
    default:
      return 'badge-ghost border-none bg-base-300/40 text-base-content/50'
  }
}

function manualProbeLabel(status: ManageNodeRow['manual_probe_status']): string {
  switch (status) {
    case 'pass':
      return '入池预检通过'
    case 'timeout':
      return '入池预检超时'
    case 'fail':
      return '入池预检失败'
    default:
      return '未做入池预检'
  }
}

function lifecycleLabel(state?: NodeLifecycleState | ''): string {
  switch (state) {
    case 'staged':
      return '待激活'
    case 'disabled':
      return '已禁用'
    default:
      return '代理池'
  }
}

function lifecycleTone(state?: NodeLifecycleState | ''): string {
  switch (state) {
    case 'staged':
      return 'badge-warning border-none bg-warning/20 text-warning-content'
    case 'disabled':
      return 'badge-ghost border-none bg-base-300/40 text-base-content/50'
    default:
      return 'badge-info border-none bg-info/15 text-info'
  }
}

function activationReadyLabel(ready: boolean): string {
  return ready ? '可激活' : '未就绪'
}

function activationReadyTone(ready: boolean): string {
  return ready
    ? 'badge-success border-none bg-success/15 text-success'
    : 'badge-warning border-none bg-warning/20 text-warning-content'
}

function activationReadyFilterLabel(value: ActivationReadyFilter): string {
  switch (value) {
    case 'ready':
      return '可激活'
    case 'blocked':
      return '未就绪'
    default:
      return '全部激活状态'
  }
}

function manualProbeFilterLabel(status: ManualProbeStatus | ''): string {
  switch (status) {
    case 'pass':
      return '入池预检通过'
    case 'fail':
      return '入池预检失败'
    case 'timeout':
      return '入池预检超时'
    case 'untested':
      return '未做入池预检'
    default:
      return '全部入池预检'
  }
}

function lifecycleFilterLabel(state: NodeLifecycleState | ''): string {
  switch (state) {
    case 'active':
      return '代理池'
    case 'staged':
      return '待激活'
    case 'disabled':
      return '已禁用'
    default:
      return '全部生命周期'
  }
}

function formatManualProbeChecked(epochSeconds?: number): string {
  if (!epochSeconds) return '未进行入池预检'
  return new Date(epochSeconds * 1000).toLocaleString()
}

function downloadTextFile(filename: string, text: string): void {
  const blob = new Blob([text], { type: 'text/plain' })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  anchor.click()
  URL.revokeObjectURL(url)
}

function SortIcon({ active, dir }: { active: boolean; dir: ManageSortDir }) {
  if (!active) {
    return (
      <svg xmlns="http://www.w3.org/2000/svg" className="ml-0.5 inline h-3 w-3 opacity-30" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M7 16V4m0 0L3 8m4-4l4 4m6 0v12m0 0l4-4m-4 4l-4-4" />
      </svg>
    )
  }
  return dir === 'asc' ? (
    <svg xmlns="http://www.w3.org/2000/svg" className="ml-0.5 inline h-3 w-3 opacity-70" fill="none" viewBox="0 0 24 24" stroke="currentColor">
      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M5 15l7-7 7 7" />
    </svg>
  ) : (
    <svg xmlns="http://www.w3.org/2000/svg" className="ml-0.5 inline h-3 w-3 opacity-70" fill="none" viewBox="0 0 24 24" stroke="currentColor">
      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M19 9l-7 7-7-7" />
    </svg>
  )
}

function StatusBadge({ status }: { status: ManageNodeRow['runtime_status'] }) {
  switch (status) {
    case 'normal':
      return <span className="badge badge-success badge-sm flex items-center gap-1 border-none bg-success/15 px-2 py-3.5 font-medium text-success"><div className="h-1.5 w-1.5 rounded-full bg-success"></div>正常</span>
    case 'unavailable':
      return <span className="badge badge-error badge-sm flex items-center gap-1 border-none bg-error/15 px-2 py-3.5 font-medium text-error"><div className="h-1.5 w-1.5 rounded-full bg-error"></div>不可用</span>
    case 'blacklisted':
      return <span className="badge badge-error badge-sm flex items-center gap-1 border-none bg-error/30 px-2 py-3.5 font-bold text-error"><svg xmlns="http://www.w3.org/2000/svg" className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2.5" d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728A9 9 0 015.636 5.636m12.728 12.728L5.636 5.636" /></svg>黑名单</span>
    case 'pending':
      return <span className="badge badge-warning badge-sm flex items-center gap-1 border-none bg-warning/15 px-2 py-3.5 font-medium text-warning-content"><div className="h-1.5 w-1.5 rounded-full bg-warning animate-pulse"></div>待检查</span>
    case 'disabled':
      return <span className="badge badge-ghost badge-sm border-none bg-base-300/50 px-2 py-3.5 font-medium text-base-content/50">已禁用</span>
    default:
      return <span className="badge badge-ghost badge-sm border-none px-2 py-3.5">未知</span>
  }
}

function ProviderQualityBadge({ provider, status }: { provider: string; status?: string }) {
  if (!status) return null
  return <span className={`badge badge-sm ${qualityStatusTone(status)}`}>{provider} {qualityStatusLabel(status)}</span>
}

function ManualProbeBadge({ node }: { node: ManageNodeRow }) {
  return (
    <span className={`badge badge-sm ${manualProbeTone(node.manual_probe_status)}`} title={node.manual_probe_message || manualProbeLabel(node.manual_probe_status)}>
      {manualProbeLabel(node.manual_probe_status)}
    </span>
  )
}

function ActivationBadge({ ready, reason }: { ready: boolean; reason?: string }) {
  return (
    <span className={`badge badge-sm ${activationReadyTone(ready)}`} title={reason || activationReadyLabel(ready)}>
      {activationReadyLabel(ready)}
    </span>
  )
}

export default function ManagePanelPage() {
  const [pageData, setPageData] = useState<ManageListResponse | null>(null)
  const [query, setQuery] = useState<ManageQuery>(() => normalizeManageQuery())
  const [keywordInput, setKeywordInput] = useState('')
  const [listLoading, setListLoading] = useState(true)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [needReload, setNeedReload] = useState(false)
  const pageCacheRef = useRef(new Map<string, ManageListResponse>())
  const listAbortRef = useRef<AbortController | null>(null)
  const queryRef = useRef(query)

  const [modalOpen, setModalOpen] = useState(false)
  const [editingNode, setEditingNode] = useState<string | null>(null)
  const [form, setForm] = useState<ConfigNodePayload>(emptyPayload)
  const [formError, setFormError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [toggling, setToggling] = useState<string | null>(null)
  const [probingTag, setProbingTag] = useState<string | null>(null)
  const [qualityLoadingKey, setQualityLoadingKey] = useState<string | null>(null)
  const [expandedQualityNode, setExpandedQualityNode] = useState<string | null>(null)
  const [qualityDetails, setQualityDetails] = useState<Record<string, NodeQualityCheckResult>>({})
  const [batchQualityState, setBatchQualityState] = useState<BatchQualityState | null>(null)
  const batchQualityControllerRef = useRef<AbortController | null>(null)
  const batchQualityStreamJobIdRef = useRef<string | null>(null)
  const batchQualityTouchedNamesRef = useRef(new Set<string>())
  const [lastQualityJobStatus, setLastQualityJobStatus] = useState<BatchQualityState['status'] | null>(null)

  const [selection, setSelection] = useState<SelectionState>(createEmptySelection)
  const [batchProcessing, setBatchProcessing] = useState(false)
  const [batchDeleteConfirm, setBatchDeleteConfirm] = useState(false)
  const [batchProbeProgress, setBatchProbeProgress] = useState<{ current: number; total: number } | null>(null)
  const [activeProbeJob, setActiveProbeJob] = useState<BatchProbeJob | null>(null)
  const [lastProbeJobStatus, setLastProbeJobStatus] = useState<string | null>(null)

  const [importModalOpen, setImportModalOpen] = useState(false)
  const [importContent, setImportContent] = useState('')
  const [importNamePrefix, setImportNamePrefix] = useState('imported')
  const [importing, setImporting] = useState(false)
  const [importError, setImportError] = useState('')
  const [importResult, setImportResult] = useState<ImportNodesResponse | null>(null)

  useEffect(() => {
    queryRef.current = query
  }, [query])

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setQuery(prev => normalizeManageQuery({ ...prev, page: 1, keyword: keywordInput }))
    }, 250)
    return () => window.clearTimeout(timer)
  }, [keywordInput])

  useEffect(() => {
    if (!success) return
    const timer = window.setTimeout(() => setSuccess(''), 5000)
    return () => window.clearTimeout(timer)
  }, [success])

  useEffect(() => {
    return () => {
      listAbortRef.current?.abort()
      batchQualityControllerRef.current?.abort()
    }
  }, [])

  useEffect(() => {
    if (selection.mode !== 'filter') return
    const current = buildManageFilterSnapshot(query)
    if (
      selection.filter.keyword !== current.keyword
      || selection.filter.status !== current.status
      || selection.filter.region !== current.region
      || selection.filter.source !== current.source
      || selection.filter.lifecycle_state !== current.lifecycle_state
      || selection.filter.manual_probe_status !== current.manual_probe_status
      || selection.filter.activation_ready !== current.activation_ready
      || selection.filter.quality_status !== current.quality_status
    ) {
      setSelection(createEmptySelection())
    }
  }, [query, selection])

  const loadManagePage = useCallback(async (nextQuery: ManageQuery, force = false) => {
    const normalized = normalizeManageQuery(nextQuery)
    const key = buildManageQueryKey(normalized)
    if (!force) {
      const cached = pageCacheRef.current.get(key)
      if (cached) {
        setPageData(cached)
        setListLoading(false)
        return
      }
    }

    listAbortRef.current?.abort()
    const controller = new AbortController()
    listAbortRef.current = controller
    setListLoading(true)

    try {
      const res = await fetchManageNodes(normalized, controller.signal)
      if (controller.signal.aborted) return

      const resolvedPage = resolveManageResponsePage(normalized, res)
      if (resolvedPage !== normalized.page) {
        setQuery(prev => prev.page === resolvedPage ? prev : normalizeManageQuery({ ...prev, page: resolvedPage }))
        return
      }

      pageCacheRef.current.set(key, res)
      trimManagePageCache(pageCacheRef.current)
      setPageData(res)
      setError('')
    } catch (err) {
      if ((err as Error).name !== 'AbortError') {
        setError(err instanceof Error ? err.message : '加载节点失败')
      }
    } finally {
      if (listAbortRef.current === controller) {
        listAbortRef.current = null
        setListLoading(false)
      }
    }
  }, [])

  useEffect(() => {
    void loadManagePage(query)
  }, [loadManagePage, query])

  useEffect(() => {
    let disposed = false

    const syncBatchProbeJob = async () => {
      try {
        const res = await fetchProbeBatchJobStatus()
        if (disposed) return
        setActiveProbeJob(res.job)
        if (res.job && (res.job.status === 'queued' || res.job.status === 'running')) {
          setBatchProbeProgress({ current: res.job.completed, total: res.job.total })
          return
        }
        setBatchProbeProgress(null)
      } catch {
        if (!disposed) {
          setActiveProbeJob(null)
          setBatchProbeProgress(null)
        }
      }
    }

    void syncBatchProbeJob()
    const timer = window.setInterval(() => void syncBatchProbeJob(), 1000)
    return () => {
      disposed = true
      window.clearInterval(timer)
    }
  }, [])

  const invalidateCachedPages = useCallback((names?: string[], clearAll = false) => {
    if (clearAll) {
      pageCacheRef.current.clear()
      return
    }

    const currentKey = buildManageQueryKey(queryRef.current)
    if (!names || names.length === 0) {
      pageCacheRef.current.delete(currentKey)
      return
    }

    const touched = new Set(names)
    for (const [key, value] of Array.from(pageCacheRef.current.entries())) {
      if (key === currentKey || value.items.some(item => touched.has(item.name))) {
        pageCacheRef.current.delete(key)
      }
    }
  }, [])

  const refreshCurrentPage = useCallback(async (names?: string[], clearAll = false) => {
    invalidateCachedPages(names, clearAll)
    await loadManagePage(queryRef.current, true)
  }, [invalidateCachedPages, loadManagePage])

  const applyQualityResult = useCallback((nodeName: string, result: NodeQualityCheckResult) => {
    setQualityDetails(prev => ({ ...prev, [nodeName]: result }))
    setPageData(prev => applyQualityResultToManageList(prev, nodeName, result))
    for (const [key, value] of Array.from(pageCacheRef.current.entries())) {
      const next = applyQualityResultToManageList(value, nodeName, result)
      if (next) {
        pageCacheRef.current.set(key, next)
      }
    }
  }, [])

  const handleBatchQualityEvent = useCallback((event: Parameters<typeof reduceBatchQualityEvent>[1]) => {
    setBatchQualityState(prev => reduceBatchQualityEvent(prev, event))

    if (event.type === 'start') {
      return
    }

    if (event.type === 'progress') {
      if (event.status === 'success') {
        const result = buildQualityResultFromBatchProgress(event)
        if (result) {
          batchQualityTouchedNamesRef.current.add(event.name)
          applyQualityResult(event.name, result)
        }
      }
      return
    }

    batchQualityControllerRef.current?.abort()
    batchQualityControllerRef.current = null
    batchQualityStreamJobIdRef.current = null

    if (event.status === 'completed') {
      setSuccess(`批量质量检测完成：成功 ${event.success}，失败 ${event.failed}`)
    } else if (event.status === 'cancelled') {
      setSuccess('批量质量检测已取消')
    } else if (event.status === 'failed') {
      setError('批量质量检测任务失败')
    }
  }, [applyQualityResult])

  const handleBatchQualityStreamError = useCallback((err: Error) => {
    batchQualityControllerRef.current = null
    batchQualityStreamJobIdRef.current = null
    setError(err.message || '批量质量检测流中断')
  }, [])

  useEffect(() => {
    let disposed = false

    const syncBatchQualityJob = async () => {
      try {
        const res = await fetchQualityBatchJobStatus()
        if (disposed) return

        setBatchQualityState(prev => mergeQualityJobSnapshot(prev, res.job))

        if (res.job?.last_result) {
          const restored = buildQualityResultFromJobResult(res.job.last_result)
          if (restored) {
            applyQualityResult(res.job.last_result.name, restored)
          }
        }

        if (res.job && isBatchQualityActive(res.job.status)) {
          if (batchQualityStreamJobIdRef.current !== res.job.id) {
            batchQualityControllerRef.current?.abort()
            batchQualityControllerRef.current = streamQualityBatchJob(
              res.job.id,
              handleBatchQualityEvent,
              handleBatchQualityStreamError,
            )
            batchQualityStreamJobIdRef.current = res.job.id
          }
          return
        }

        batchQualityControllerRef.current?.abort()
        batchQualityControllerRef.current = null
        batchQualityStreamJobIdRef.current = null
      } catch {
        if (disposed) return
      }
    }

    void syncBatchQualityJob()
    const timer = window.setInterval(() => void syncBatchQualityJob(), 1000)
    return () => {
      disposed = true
      window.clearInterval(timer)
    }
  }, [applyQualityResult, handleBatchQualityEvent, handleBatchQualityStreamError])

  const summary = pageData?.summary ?? emptySummary
  const nodes = useMemo(() => pageData?.items ?? [], [pageData?.items])
  const lifecycleStateOptions = pageData?.facets.lifecycle_states ?? []
  const manualProbeStatusOptions = pageData?.facets.manual_probe_statuses ?? []
  const activationReadyOptions = pageData?.facets.activation_readiness ?? []
  const qualityStatusOptions = pageData?.facets.quality_statuses ?? []
  const currentPageNames = useMemo(() => nodes.map(node => node.name), [nodes])
  const currentFilter = useMemo(() => buildManageFilterSnapshot(query), [query])
  const selectionCount = useMemo(
    () => getSelectionCount(selection, pageData?.filtered_total ?? 0, query),
    [pageData?.filtered_total, query, selection],
  )
  const pageSelection = useMemo(
    () => getPageSelectionState(selection, currentPageNames, query),
    [currentPageNames, query, selection],
  )
  const selectionMatchesCurrentFilter = selection.mode === 'filter'
    && selection.filter.keyword === currentFilter.keyword
    && selection.filter.status === currentFilter.status
    && selection.filter.region === currentFilter.region
    && selection.filter.source === currentFilter.source
    && selection.filter.lifecycle_state === currentFilter.lifecycle_state
    && selection.filter.manual_probe_status === currentFilter.manual_probe_status
    && selection.filter.activation_ready === currentFilter.activation_ready
    && selection.filter.quality_status === currentFilter.quality_status
  const totalPages = Math.max(1, Math.ceil((pageData?.filtered_total ?? 0) / (query.page_size || 100)))
  const filtersActive = Boolean(
    query.keyword
    || query.status
    || query.region
    || query.source
    || query.lifecycle_state
    || query.manual_probe_status
    || query.activation_ready
    || query.quality_status
  )
  const probeJobRunning = activeProbeJob?.status === 'queued' || activeProbeJob?.status === 'running'
  const qualityBatchRunning = isBatchQualityActive(batchQualityState?.status)
  const initialLoading = listLoading && !pageData
  const thClass = 'cursor-pointer select-none font-semibold transition-colors hover:text-primary'

  useEffect(() => {
    const current = activeProbeJob?.status ?? null
    if (lastProbeJobStatus && current && current !== lastProbeJobStatus && current !== 'queued' && current !== 'running') {
      pageCacheRef.current.clear()
      void loadManagePage(queryRef.current, true)
    }
    if (current !== lastProbeJobStatus) {
      setLastProbeJobStatus(current)
    }
  }, [activeProbeJob?.status, lastProbeJobStatus, loadManagePage])

  useEffect(() => {
    const current = batchQualityState?.status ?? null
    if (lastQualityJobStatus && current && current !== lastQualityJobStatus && isBatchQualityTerminal(current)) {
      const touchedNames = Array.from(batchQualityTouchedNamesRef.current)
      batchQualityTouchedNamesRef.current = new Set()
      void refreshCurrentPage(touchedNames, touchedNames.length === 0)
    }
    if (current !== lastQualityJobStatus) {
      setLastQualityJobStatus(current)
    }
  }, [batchQualityState?.status, lastQualityJobStatus, refreshCurrentPage])

  const updateQuery = useCallback((updater: (prev: ManageQuery) => ManageQuery) => {
    setQuery(prev => normalizeManageQuery(updater(prev)))
  }, [])

  const handleSort = useCallback((key: ManageSortKey) => {
    updateQuery(prev => ({
      ...prev,
      page: 1,
      sort_key: key,
      sort_dir: prev.sort_key === key && prev.sort_dir === 'asc' ? 'desc' : 'asc',
    }))
  }, [updateQuery])

  const openCreateModal = useCallback(() => {
    setEditingNode(null)
    setForm(emptyPayload)
    setFormError('')
    setModalOpen(true)
  }, [])

  const openEditModal = useCallback((node: ManageNodeRow) => {
    setEditingNode(node.name)
    setForm({
      name: node.name,
      uri: node.uri,
      port: node.port,
      username: node.username || '',
      password: node.password || '',
    })
    setFormError('')
    setModalOpen(true)
  }, [])

  const handleSubmit = useCallback(async (event: FormEvent) => {
    event.preventDefault()
    if (!form.name.trim()) {
      setFormError('节点名称不能为空')
      return
    }
    if (!form.uri.trim()) {
      setFormError('URI 不能为空')
      return
    }

    setSubmitting(true)
    setFormError('')
    try {
      const res = editingNode
        ? await updateConfigNode(editingNode, form)
        : await createConfigNode(form)
      setSuccess(res.message || (editingNode ? '节点已更新' : '节点已添加'))
      setNeedReload(true)
      setModalOpen(false)
      pageCacheRef.current.clear()
      await loadManagePage(queryRef.current, true)
    } catch (err) {
      setFormError(err instanceof Error ? err.message : '操作失败')
    } finally {
      setSubmitting(false)
    }
  }, [editingNode, form, loadManagePage])

  const handleDelete = useCallback(async () => {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      const res = await deleteConfigNode(deleteTarget)
      setSuccess(res.message || '节点已删除')
      setNeedReload(true)
      setDeleteTarget(null)
      pageCacheRef.current.clear()
      await loadManagePage(queryRef.current, true)
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除失败')
    } finally {
      setDeleting(false)
    }
  }, [deleteTarget, loadManagePage])

  const handleToggle = useCallback(async (node: ManageNodeRow) => {
    setToggling(node.name)
    try {
      const res = await toggleConfigNode(node.name, !!node.disabled)
      setSuccess(res.message || (node.disabled ? '节点已启用' : '节点已禁用'))
      await refreshCurrentPage([node.name])
    } catch (err) {
      setError(err instanceof Error ? err.message : '操作失败')
    } finally {
      setToggling(null)
    }
  }, [refreshCurrentPage])

  const handleProbe = useCallback(async (node: ManageNodeRow) => {
    if (!node.tag) return
    setProbingTag(node.tag)
    try {
      const res = await probeNode(node.tag)
      setSuccess(res.message || '入池预检通过')
      await refreshCurrentPage([node.name])
    } catch (err) {
      setError(err instanceof Error ? err.message : '入池预检失败')
    } finally {
      setProbingTag(null)
    }
  }, [refreshCurrentPage])

  const handleRelease = useCallback(async (node: ManageNodeRow) => {
    if (!node.tag) return
    try {
      await releaseNode(node.tag)
      setSuccess('已解除黑名单')
      await refreshCurrentPage([node.name])
    } catch (err) {
      setError(err instanceof Error ? err.message : '解除失败')
    }
  }, [refreshCurrentPage])

  const handleQualityCheck = useCallback(async (node: ManageNodeRow) => {
    if (!node.tag) {
      setError('当前节点还没有运行时标识，暂时无法执行质量检测')
      return
    }

    const loadingKey = `check:${node.name}`
    setExpandedQualityNode(node.name)
    setQualityLoadingKey(loadingKey)
    try {
      const res = await checkNodeQuality(node.tag)
      applyQualityResult(node.name, res.result)
      setSuccess(res.message || '质量检测完成')
      await refreshCurrentPage([node.name])
    } catch (err) {
      setError(err instanceof Error ? err.message : '质量检测失败')
    } finally {
      setQualityLoadingKey(current => current === loadingKey ? null : current)
    }
  }, [applyQualityResult, refreshCurrentPage])

  const handleToggleQualityDetails = useCallback(async (node: ManageNodeRow) => {
    if (expandedQualityNode === node.name) {
      setExpandedQualityNode(null)
      return
    }

    setExpandedQualityNode(node.name)
    const cached = qualityDetails[node.name]
    if (cached && cached.items.length > 0) return
    if (!node.tag) return

    const loadingKey = `detail:${node.name}`
    setQualityLoadingKey(loadingKey)
    try {
      const res = await getNodeQuality(node.tag)
      applyQualityResult(node.name, res.result)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载质量检测详情失败')
    } finally {
      setQualityLoadingKey(current => current === loadingKey ? null : current)
    }
  }, [applyQualityResult, expandedQualityNode, qualityDetails])

  const handleBatchLifecycle = useCallback(async (action: BatchLifecycleAction) => {
    if (selectionCount === 0) return
    setBatchProcessing(true)
    try {
      const res = await batchLifecycleConfigNodes(buildSelectionRequest(selection), action)
      const combinedErrors = [res.reload_error, ...(res.errors ?? [])].filter(Boolean).join('; ')
      setError(combinedErrors)
      setNeedReload(Boolean(res.reload_error))
      setSuccess(res.message || '批量生命周期操作已完成')
      setSelection(createEmptySelection())
      await refreshCurrentPage(undefined, true)
    } catch (err) {
      setError(err instanceof Error ? err.message : '批量生命周期操作失败')
    } finally {
      setBatchProcessing(false)
    }
  }, [refreshCurrentPage, selection, selectionCount])

  const handleBatchProbe = useCallback(async () => {
    if (selectionCount === 0) return
    try {
      const res = await startProbeBatchJob(buildSelectionRequest(selection))
      setActiveProbeJob(res.job)
      setBatchProbeProgress({ current: res.job.completed, total: res.job.total })
      setSuccess('批量入池预检任务已启动')
    } catch (err) {
      setError(err instanceof Error ? err.message : '批量入池预检启动失败')
    }
  }, [selection, selectionCount])

  const handleBatchProbeCancel = useCallback(async () => {
    if (!activeProbeJob) return
    try {
      await cancelProbeBatchJob(activeProbeJob.id)
      setSuccess('批量入池预检任务已取消')
      const res = await fetchProbeBatchJobStatus()
      setActiveProbeJob(res.job)
      if (!res.job || (res.job.status !== 'queued' && res.job.status !== 'running')) {
        setBatchProbeProgress(null)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : '取消批量入池预检失败')
    }
  }, [activeProbeJob])

  const handleBatchQualityCheck = useCallback(async () => {
    if (selectionCount === 0) return

    batchQualityTouchedNamesRef.current = new Set()
    batchQualityControllerRef.current?.abort()
    batchQualityControllerRef.current = null
    batchQualityStreamJobIdRef.current = null

    try {
      const res = await startQualityBatchJob(buildSelectionRequest(selection))
      setBatchQualityState(prev => mergeQualityJobSnapshot(prev, res.job))
      batchQualityControllerRef.current = streamQualityBatchJob(
        res.job.id,
        handleBatchQualityEvent,
        handleBatchQualityStreamError,
      )
      batchQualityStreamJobIdRef.current = res.job.id
      setSuccess('批量质量检测任务已启动')
    } catch (err) {
      setError(err instanceof Error ? err.message : '批量质量检测启动失败')
    }
    return
    /*

    batchQualityControllerRef.current?.abort()
    const touchedNames = new Set<string>()
    setBatchQualityState({
      status: 'running',
      total: selectionCount,
      current: 0,
      success: 0,
      failed: 0,
      lastResult: null,
    })

    const controller = checkNodeQualityBatch(
      buildSelectionRequest(selection, query),
      (event) => {
        setBatchQualityState(prev => reduceBatchQualityEvent(prev, event))
        if (event.type === 'progress' && event.status === 'success') {
          const result = buildQualityResultFromBatchProgress(event)
          if (result) {
            touchedNames.add(event.name)
            applyQualityResult(event.name, result)
          }
        }
        if (event.type === 'complete') {
          batchQualityControllerRef.current = null
          setSuccess(`批量质量检测完成：成功 ${event.success}，失败 ${event.failed}`)
          void refreshCurrentPage(Array.from(touchedNames), touchedNames.size === 0)
        }
      },
      (err) => {
        batchQualityControllerRef.current = null
        setError(err instanceof Error ? err.message : '批量质量检测失败')
        setBatchQualityState(prev => prev ? { ...prev, status: 'completed' } : prev)
      },
    )

    batchQualityControllerRef.current = controller
    */
  }, [handleBatchQualityEvent, handleBatchQualityStreamError, selection, selectionCount])

  const handleBatchQualityCancel = useCallback(async () => {
    const jobId = batchQualityState?.jobId ?? batchQualityStreamJobIdRef.current
    if (!jobId) return

    try {
      await cancelQualityBatchJob(jobId)
      setSuccess('已提交批量质量检测取消请求')
    } catch (err) {
      setError(err instanceof Error ? err.message : '取消批量质量检测失败')
    }
  }, [batchQualityState?.jobId])

  const handleBatchDelete = useCallback(async () => {
    if (selectionCount === 0) return
    setBatchProcessing(true)
    setBatchDeleteConfirm(false)
    try {
      const res = await batchDeleteConfigNodes(buildSelectionRequest(selection))
      setSuccess(res.message || '批量删除完成')
      setNeedReload(true)
      setSelection(createEmptySelection())
      pageCacheRef.current.clear()
      await loadManagePage(queryRef.current, true)
    } catch (err) {
      setError(err instanceof Error ? err.message : '批量删除失败')
    } finally {
      setBatchProcessing(false)
    }
  }, [loadManagePage, selection, selectionCount])

  const openImportModal = useCallback(() => {
    setImportContent('')
    setImportNamePrefix('imported')
    setImportError('')
    setImportResult(null)
    setImportModalOpen(true)
  }, [])

  const handleFileImport = useCallback((event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = (loadEvent) => {
      const text = loadEvent.target?.result
      if (typeof text === 'string') setImportContent(text)
    }
    reader.readAsText(file)
    event.target.value = ''
  }, [])

  const handleImport = useCallback(async () => {
    if (!importContent.trim()) {
      setImportError('请输入节点 URI')
      return
    }

    setImporting(true)
    setImportError('')
    setImportResult(null)
    try {
      const res = await importNodes(importContent, importNamePrefix)
      setImportResult(res)
      if (res.imported > 0) {
        setNeedReload(true)
        setSuccess(res.message)
        pageCacheRef.current.clear()
        await loadManagePage(queryRef.current, true)
      }
    } catch (err) {
      setImportError(err instanceof Error ? err.message : '导入失败')
    } finally {
      setImporting(false)
    }
  }, [importContent, importNamePrefix, loadManagePage])

  const handleExport = useCallback(async () => {
    try {
      const text = await exportProxies()
      if (!text.trim()) {
        setError('没有可导出的节点')
        return
      }
      downloadTextFile('nodes_export.txt', text)
      setSuccess('节点已导出')
    } catch (err) {
      setError(err instanceof Error ? err.message : '导出失败')
    }
  }, [])

  const handleExportSelected = useCallback(async () => {
    if (selectionCount === 0) return
    try {
      const text = await exportSelectedProxies(buildSelectionRequest(selection))
      if (!text.trim()) {
        setError('没有可导出的选中节点')
        return
      }
      downloadTextFile('selected_nodes_export.txt', text)
      setSuccess(selection.mode === 'filter' ? `已导出 ${selectionCount} 个筛选结果节点` : `已导出 ${selectionCount} 个选中节点`)
    } catch (err) {
      setError(err instanceof Error ? err.message : '导出选中节点失败')
    }
  }, [selection, selectionCount])

  const handleReload = useCallback(async () => {
    try {
      const res = await triggerReload()
      setSuccess(res.message || '重载成功')
      setNeedReload(false)
      pageCacheRef.current.clear()
      await loadManagePage(queryRef.current, true)
    } catch (err) {
      setError(err instanceof Error ? err.message : '重载失败')
    }
  }, [loadManagePage])

  if (initialLoading) {
    return (
      <div className="flex h-64 items-center justify-center">
        <span className="loading loading-spinner loading-lg text-primary"></span>
      </div>
    )
  }

  return (
    <div className="flex min-h-full flex-col animate-in fade-in duration-500">
      <div className="sticky top-0 z-30 border-b border-base-300/60 bg-base-100/80 px-4 py-4 shadow-sm backdrop-blur-xl lg:px-8">
        <div className="mx-auto flex w-full max-w-[1600px] flex-col items-start justify-between gap-4 md:flex-row md:items-center">
          <div>
            <h2 className="flex items-center gap-3 text-2xl font-bold">
              <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-primary/20 bg-primary/10 text-primary">
                <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2.5" d="M4 6h16M4 10h16M4 14h16M4 18h16" />
                </svg>
              </div>
              节点管理
            </h2>
            <div className="mt-1.5 ml-[3.25rem] flex items-center gap-2 text-sm font-medium text-base-content/50">
              <span>共 <strong className="text-base-content/80">{pageData?.total ?? 0}</strong> 个节点</span>
              {summary.normal > 0 && <span className="badge badge-success badge-xs border-none bg-success/15 text-success">正常 {summary.normal}</span>}
              {summary.blacklisted > 0 && <span className="badge badge-error badge-xs border-none bg-error/15 text-error">黑名单 {summary.blacklisted}</span>}
              {summary.disabled > 0 && <span className="badge badge-ghost badge-xs bg-base-200 text-base-content/50">禁用 {summary.disabled}</span>}
              {listLoading && pageData && <span className="badge badge-ghost badge-xs">刷新中</span>}
            </div>
          </div>
          <div className="flex items-center gap-2">
            <button className="btn btn-sm btn-primary gap-2 shadow-sm lg:btn-md" onClick={openCreateModal}>
              <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2.5" d="M12 4v16m8-8H4" /></svg>
              添加节点
            </button>
            <div className="dropdown dropdown-end">
              <div tabIndex={0} role="button" className="btn btn-ghost btn-sm gap-2 border border-base-300 shadow-sm lg:btn-md">
                管理操作
                <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4 opacity-50" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M19 9l-7 7-7-7" /></svg>
              </div>
              <ul tabIndex={0} className="dropdown-content menu z-20 mt-2 w-56 rounded-xl border border-base-200 bg-base-100 p-2 shadow-xl">
                <li><button type="button" onClick={openImportModal} className="gap-3 hover:bg-primary/10 hover:text-primary"><svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-8l-4-4m0 0L8 8m4-4v12" /></svg>导入节点配置</button></li>
                <li><button type="button" onClick={() => void handleExportSelected()} disabled={selectionCount === 0} className="gap-3 hover:bg-primary/10 hover:text-primary disabled:bg-transparent disabled:text-base-content/30"><svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" /></svg>导出选中节点</button></li>
                <li><button type="button" onClick={() => void handleExport()} className="gap-3 hover:bg-primary/10 hover:text-primary"><svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" /></svg>导出所有节点</button></li>
              </ul>
            </div>
            {needReload && (
              <button className="btn btn-warning btn-sm animate-pulse gap-2 shadow-sm lg:btn-md" onClick={() => void handleReload()}>
                <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" /></svg>
                重载生效
              </button>
            )}
          </div>
        </div>
      </div>

      <div className="mx-auto flex w-full max-w-[1600px] flex-1 flex-col gap-6 p-4 lg:p-8">
        {error && (
          <div role="alert" className="alert alert-error alert-soft text-sm">
            <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z" /></svg>
            <span>{error}</span>
            <button className="btn btn-ghost btn-xs" onClick={() => setError('')}>✕</button>
          </div>
        )}
        {success && (
          <div role="alert" className="alert alert-success alert-soft text-sm">
            <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" /></svg>
            <span>{success}</span>
          </div>
        )}
        {needReload && (
          <div role="alert" className="alert alert-warning alert-soft text-sm">
            <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L4.082 16.5c-.77.833.192 2.5 1.732 2.5z" /></svg>
            <span>配置已变更，请点击「重载生效」使其生效</span>
          </div>
        )}
        {activeProbeJob && (
          <div className={`rounded-2xl border px-5 py-4 shadow-sm ${probeJobRunning ? 'border-primary/30 bg-primary/5' : 'border-base-300/50 bg-base-100'}`}>
            <div className="flex flex-col gap-3 lg:flex-row lg:items-center">
              <div className="flex-1">
                <div className="text-sm font-semibold text-base-content">批量入池预检任务<span className="ml-2 badge badge-sm">{activeProbeJob.status}</span></div>
                <div className="mt-1 text-xs text-base-content/60">进度 {activeProbeJob.completed}/{activeProbeJob.total} · 成功 {activeProbeJob.success} · 失败 {activeProbeJob.failed} · 活跃工作线程 {activeProbeJob.active_workers}</div>
                {activeProbeJob.last_result && <div className="mt-1 text-xs text-base-content/50">最近完成: {activeProbeJob.last_result.name}{activeProbeJob.last_result.error ? ` · ${activeProbeJob.last_result.error}` : ` · ${activeProbeJob.last_result.latency_ms} ms`}</div>}
              </div>
              {probeJobRunning && <button className="btn btn-sm btn-warning" onClick={() => void handleBatchProbeCancel()}>取消批量入池预检</button>}
            </div>
            <progress className="progress progress-primary mt-3 h-2 w-full" value={activeProbeJob.completed} max={activeProbeJob.total || 1}></progress>
          </div>
        )}
        {batchQualityState && (
          <div className={`rounded-2xl border px-5 py-4 shadow-sm ${qualityBatchRunning ? 'border-secondary/30 bg-secondary/5' : 'border-base-300/50 bg-base-100'}`}>
            <div className="flex flex-col gap-3 lg:flex-row lg:items-center">
              <div className="flex-1">
                <div className="text-sm font-semibold text-base-content">批量质量检测<span className="ml-2 badge badge-sm">{batchQualityState.status}</span></div>
                <div className="mt-1 text-xs text-base-content/60">进度 {batchQualityState.current}/{batchQualityState.total} · 成功 {batchQualityState.success} · 失败 {batchQualityState.failed}</div>
                {batchQualityState.lastResult && <div className="mt-1 text-xs text-base-content/50">最近完成: {batchQualityState.lastResult.name}{batchQualityState.lastResult.status === 'error' ? ` · ${batchQualityState.lastResult.error || '检测失败'}` : ` · ${batchQualityState.lastResult.quality_grade || '-'} / ${batchQualityState.lastResult.quality_status || 'unknown'}`}</div>}
              </div>
              {qualityBatchRunning && <button className="btn btn-sm btn-warning" onClick={() => void handleBatchQualityCancel()}>取消批量质检</button>}
            </div>
            <progress className="progress progress-secondary mt-3 h-2 w-full" value={batchQualityState.current} max={batchQualityState.total || 1}></progress>
          </div>
        )}

        <div className="rounded-2xl border border-base-300/50 bg-base-100 p-4 shadow-sm">
          <div className="flex flex-col items-center gap-4 lg:flex-row">
            <div className="relative w-full flex-1">
              <div className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3.5 text-base-content/40">
                <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" /></svg>
              </div>
              <input type="text" className="input input-md w-full bg-base-200/50 pl-11 transition-colors focus:border-primary/50 focus:bg-base-100" placeholder="搜索节点名称、URI 或 地区..." value={keywordInput} onChange={(event) => setKeywordInput(event.target.value)} />
            </div>

            <div className="flex w-full flex-wrap gap-3 sm:flex-nowrap lg:w-auto">
              <select className="select select-md flex-1 bg-base-200/50 focus:bg-base-100 sm:w-36" value={query.status} onChange={(event) => updateQuery(prev => ({ ...prev, page: 1, status: event.target.value as ManageStatus }))}>
                <option value="">全部状态</option>
                <option value="normal">✅ 正常运行</option>
                <option value="unavailable">❌ 不可用</option>
                <option value="blacklisted">🔴 黑名单</option>
                <option value="pending">⚠️ 待检查</option>
                <option value="disabled">🚫 已禁用</option>
              </select>
              {(lifecycleStateOptions.length ?? 0) > 0 && (
                <select className="select select-md flex-1 bg-base-200/50 focus:bg-base-100 sm:w-32" value={query.lifecycle_state} onChange={(event) => updateQuery(prev => ({ ...prev, page: 1, lifecycle_state: event.target.value as NodeLifecycleState | '' }))}>
                  <option value="">{lifecycleFilterLabel('')}</option>
                  {lifecycleStateOptions.map(state => <option key={state} value={state}>{lifecycleFilterLabel(state)}</option>)}
                </select>
              )}
              {(manualProbeStatusOptions.length ?? 0) > 0 && (
                <select className="select select-md flex-1 bg-base-200/50 focus:bg-base-100 sm:w-32" value={query.manual_probe_status} onChange={(event) => updateQuery(prev => ({ ...prev, page: 1, manual_probe_status: event.target.value as ManualProbeStatus | '' }))}>
                  <option value="">{manualProbeFilterLabel('')}</option>
                  {manualProbeStatusOptions.map(status => <option key={status} value={status}>{manualProbeFilterLabel(status)}</option>)}
                </select>
              )}
              {(activationReadyOptions.length ?? 0) > 0 && (
                <select className="select select-md flex-1 bg-base-200/50 focus:bg-base-100 sm:w-32" value={query.activation_ready} onChange={(event) => updateQuery(prev => ({ ...prev, page: 1, activation_ready: event.target.value as ActivationReadyFilter }))}>
                  <option value="">{activationReadyFilterLabel('')}</option>
                  {activationReadyOptions.map(status => <option key={status} value={status}>{activationReadyFilterLabel(status)}</option>)}
                </select>
              )}
              {(pageData?.facets.regions.length ?? 0) > 0 && (
                <select className="select select-md flex-1 bg-base-200/50 focus:bg-base-100 sm:w-32" value={query.region} onChange={(event) => updateQuery(prev => ({ ...prev, page: 1, region: event.target.value }))}>
                  <option value="">全部地区</option>
                  {pageData?.facets.regions.map(region => <option key={region} value={region}>{regionFlag(region)} {region.toUpperCase()}</option>)}
                </select>
              )}
              {(pageData?.facets.sources.length ?? 0) > 1 && (
                <select className="select select-md flex-1 bg-base-200/50 focus:bg-base-100 sm:w-32" value={query.source} onChange={(event) => updateQuery(prev => ({ ...prev, page: 1, source: event.target.value }))}>
                  <option value="">全部来源</option>
                  {pageData?.facets.sources.map(source => <option key={source} value={source}>{sourceLabel(source)}</option>)}
                </select>
              )}
              <select className="select select-md flex-1 bg-base-200/50 focus:bg-base-100 sm:w-32" value={query.quality_status} onChange={(event) => updateQuery(prev => ({ ...prev, page: 1, quality_status: event.target.value }))}>
                <option value="">全部质检</option>
                {qualityStatusOptions.map(status => <option key={status} value={status}>{qualityStatusLabel(status)}</option>)}
              </select>
              <button type="button" className={`btn btn-md ${selectionMatchesCurrentFilter ? 'btn-primary' : 'btn-ghost'}`} onClick={() => setSelection({ mode: 'filter', filter: buildManageFilterSnapshot(query), excludeNames: new Set() })} disabled={(pageData?.filtered_total ?? 0) === 0 || batchProcessing || probeJobRunning || qualityBatchRunning}>
                {selectionMatchesCurrentFilter ? `已选筛选结果 ${selectionCount}` : `全选筛选结果${(pageData?.filtered_total ?? 0) > 0 ? ` (${pageData?.filtered_total ?? 0})` : ''}`}
              </button>
            </div>
          </div>
        </div>

        <div className={`overflow-hidden transition-all duration-300 ${selectionCount > 0 ? 'max-h-32 opacity-100' : 'max-h-0 opacity-0'}`}>
          <div className="relative flex flex-col gap-3 rounded-2xl border border-primary/20 bg-primary/5 px-5 py-4 shadow-inner">
            <div className="absolute top-0 bottom-0 left-0 w-1.5 rounded-l-2xl bg-primary"></div>
            <div className="flex flex-wrap items-center gap-4">
              <span className="flex items-center gap-2 text-base font-medium text-base-content/80">
                <span className="badge badge-primary badge-md font-bold">{selectionCount}</span>
                {selection.mode === 'filter' && selectionMatchesCurrentFilter ? `项已按筛选选中（排除 ${selection.excludeNames.size} 项）` : '项已选择'}
              </span>
              <div className="ml-auto flex flex-wrap gap-2">
                <button className="btn btn-sm btn-primary gap-1.5 shadow-sm" onClick={() => void handleBatchProbe()} disabled={batchProcessing || probeJobRunning || qualityBatchRunning}>
                  {batchProbeProgress ? <><span className="loading loading-spinner loading-xs"></span> {batchProbeProgress.current}/{batchProbeProgress.total}</> : <><svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M13 10V3L4 14h7v7l9-11h-7z" /></svg>批量入池预检</>}
                </button>
                <button className="btn btn-sm btn-secondary gap-1.5 shadow-sm" onClick={handleBatchQualityCheck} disabled={batchProcessing || probeJobRunning || qualityBatchRunning}>
                  {qualityBatchRunning ? <><span className="loading loading-spinner loading-xs"></span> {batchQualityState?.current || 0}/{batchQualityState?.total || 0}</> : <><svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M9 12l2 2 4-4m5-2a9 9 0 11-18 0 9 9 0 0118 0z" /></svg>批量质检</>}
                </button>
                <div className="mx-1 h-6 w-px self-center bg-base-300"></div>
                <button className="btn btn-sm border-none bg-success/15 text-success hover:bg-success hover:text-success-content" onClick={() => void handleBatchLifecycle('activate')} disabled={batchProcessing || probeJobRunning || qualityBatchRunning}>激活</button>
                <button className="btn btn-sm border-none bg-info/15 text-info hover:bg-info hover:text-info-content" onClick={() => void handleBatchLifecycle('deactivate')} disabled={batchProcessing || probeJobRunning || qualityBatchRunning}>移回待激活</button>
                <button className="btn btn-sm border-none bg-warning/15 text-warning-content hover:bg-warning hover:text-warning-content" onClick={() => void handleBatchLifecycle('disable')} disabled={batchProcessing || probeJobRunning || qualityBatchRunning}>禁用</button>
                <button className="btn btn-sm border-none bg-error/15 text-error hover:bg-error hover:text-error-content" onClick={() => setBatchDeleteConfirm(true)} disabled={batchProcessing || probeJobRunning || qualityBatchRunning}>删除</button>
                <div className="mx-1 h-6 w-px self-center bg-base-300"></div>
                <button className="btn btn-sm btn-ghost hover:bg-base-300" onClick={() => setSelection(createEmptySelection())} disabled={batchProcessing || probeJobRunning || qualityBatchRunning}>取消选择</button>
              </div>
            </div>
            {batchProbeProgress && <progress className="progress progress-primary h-1.5 w-full bg-primary/20" value={batchProbeProgress.current} max={batchProbeProgress.total}></progress>}
          </div>
        </div>

        <div className="overflow-hidden rounded-2xl border border-base-300/50 bg-base-100 shadow-sm">
          <div className="max-h-[calc(100vh-280px)] min-h-[400px] overflow-x-auto overflow-y-auto">
            <table className="table table-md table-pin-rows">
              <thead>
                <tr className="border-b border-base-300/50 bg-base-200/50 text-base-content/70 shadow-sm">
                  <th className="w-8">
                    <input type="checkbox" className="checkbox checkbox-xs" checked={pageSelection.allSelected} onChange={() => setSelection(prev => togglePageSelection(prev, currentPageNames))} ref={(element) => { if (element) { element.indeterminate = pageSelection.partiallySelected } }} />
                  </th>
                  <th className={thClass} onClick={() => handleSort('name')}>名称 <SortIcon active={query.sort_key === 'name'} dir={query.sort_dir} /></th>
                  <th className={thClass} onClick={() => handleSort('status')}>状态 <SortIcon active={query.sort_key === 'status'} dir={query.sort_dir} /></th>
                  <th className={thClass} onClick={() => handleSort('latency')}>延迟 <SortIcon active={query.sort_key === 'latency'} dir={query.sort_dir} /></th>
                  <th className={`hidden md:table-cell ${thClass}`} onClick={() => handleSort('region')}>区域 <SortIcon active={query.sort_key === 'region'} dir={query.sort_dir} /></th>
                  <th className={`hidden md:table-cell ${thClass}`} onClick={() => handleSort('port')}>端口 <SortIcon active={query.sort_key === 'port'} dir={query.sort_dir} /></th>
                  <th className={`hidden lg:table-cell ${thClass}`} onClick={() => handleSort('source')}>来源 <SortIcon active={query.sort_key === 'source'} dir={query.sort_dir} /></th>
                  <th className="font-semibold">操作</th>
                </tr>
              </thead>
              <tbody>
                {nodes.length === 0 ? (
                  <tr>
                    <td colSpan={8} className="h-[300px] p-0">
                      <div className="flex h-full w-full flex-col items-center justify-center opacity-60">
                        <div className="mb-4 flex h-16 w-16 items-center justify-center rounded-full bg-base-200">
                          <svg xmlns="http://www.w3.org/2000/svg" className="h-8 w-8 text-base-content/40" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.5" d="M20 13V6a2 2 0 00-2-2H6a2 2 0 00-2 2v7m16 0v5a2 2 0 01-2 2H6a2 2 0 01-2-2v-5m16 0h-2.586a1 1 0 00-.707.293l-2.414 2.414a1 1 0 01-.707.293h-3.172a1 1 0 01-.707-.293l-2.414-2.414A1 1 0 006.586 13H4" />
                          </svg>
                        </div>
                        <p className="text-base font-medium text-base-content">{filtersActive ? '未找到匹配的节点数据' : '暂无配置节点'}</p>
                        {!filtersActive && <p className="mt-1 text-sm text-base-content/50">请点击右上角「添加节点」或导入配置以开始</p>}
                      </div>
                    </td>
                  </tr>
                ) : (
                  nodes.map((node) => {
                    const detail = qualityDetails[node.name] ?? buildQualityCacheEntry(node)
                    const isExpanded = expandedQualityNode === node.name
                    const isCheckingQuality = qualityLoadingKey === `check:${node.name}`
                    const isLoadingDetail = qualityLoadingKey === `detail:${node.name}`
                    const checked = isNodeSelected(selection, node.name, query)
                    const activationReady = detail?.activation_ready ?? node.activation_ready ?? false
                    const activationBlockReason = detail?.activation_block_reason || node.activation_block_reason || ''
                    const canShowQuality = Boolean(node.tag || detail)

                    return (
                      <Fragment key={node.name}>
                        <tr className={`group border-b border-base-200/50 transition-colors ${node.runtime_status === 'disabled' ? 'opacity-50 grayscale-[0.5]' : ''} ${node.runtime_status === 'blacklisted' ? 'opacity-80' : ''} ${checked ? 'bg-primary/5' : 'hover:bg-base-200/40'}`}>
                          <td className="w-8">
                            <input type="checkbox" className="checkbox checkbox-sm" checked={checked} onChange={() => setSelection(prev => toggleNodeSelection(prev, node.name))} />
                          </td>
                          <td>
                            <div className="flex items-center gap-2 text-sm font-semibold">
                              {node.region && <span className="text-lg leading-none drop-shadow-sm">{regionFlag(node.region)}</span>}
                              <span className="max-w-[200px] truncate" title={node.name}>{node.name}</span>
                            </div>
                            <div className="mt-1 flex flex-wrap items-center gap-2 text-xs">
                              <span className={`badge badge-sm ${lifecycleTone(node.lifecycle_state)}`}>{lifecycleLabel(node.lifecycle_state)}</span>
                              <ManualProbeBadge node={node} />
                              <ActivationBadge ready={activationReady} reason={activationBlockReason} />
                              {node.manual_probe_status === 'pass' && node.manual_probe_latency_ms >= 0 && <span className="font-mono text-base-content/55">预检 {node.manual_probe_latency_ms} ms</span>}
                              {node.manual_probe_checked && <span className="text-base-content/45">{formatManualProbeChecked(node.manual_probe_checked)}</span>}
                              {node.manual_probe_message && node.manual_probe_status !== 'pass' && <span className="max-w-[320px] truncate text-base-content/50" title={node.manual_probe_message}>{node.manual_probe_message}</span>}
                              {!activationReady && activationBlockReason && <span className="max-w-[320px] truncate text-base-content/50" title={activationBlockReason}>{activationBlockReason}</span>}
                              {detail && (
                                <>
                                  <ProviderQualityBadge provider="OpenAI" status={detail.quality_openai_status} />
                                  <ProviderQualityBadge provider="Anthropic" status={detail.quality_anthropic_status} />
                                </>
                              )}
                              {node.quality_grade ? <span className={`badge badge-sm ${qualityStatusTone(node.quality_status)}`}>{node.quality_grade} · {node.quality_status || 'unknown'}</span> : <span className="text-base-content/35">未做质量检测</span>}
                              {typeof node.quality_score === 'number' && <span className="font-mono text-base-content/55">分数 {node.quality_score}</span>}
                              {(node.quality_checked || detail?.quality_checked_at) && <span className="text-base-content/45">{node.quality_checked ? formatQualityChecked(node.quality_checked) : formatQualityCheckedAt(detail?.quality_checked_at)}</span>}
                              {canShowQuality && <button type="button" className="link link-hover text-info" onClick={() => void handleToggleQualityDetails(node)}>{isExpanded ? '收起详情' : '查看详情'}</button>}
                            </div>
                          </td>
                          <td><StatusBadge status={node.runtime_status} /></td>
                          <td className={`font-mono text-sm font-medium ${latencyColor(node.latency_ms)}`}>{node.latency_ms < 0 ? <span className="text-base-content/30">-</span> : `${node.latency_ms} ms`}</td>
                          <td className="hidden text-sm text-base-content/70 md:table-cell">{node.country || node.region ? <div className="badge badge-ghost badge-sm">{node.country || node.region}</div> : '-'}</td>
                          <td className="hidden font-mono text-sm text-base-content/70 md:table-cell">{node.port || '-'}</td>
                          <td className="hidden lg:table-cell"><div className="badge badge-ghost badge-sm border-base-300 bg-transparent opacity-70">{sourceLabel(node.source)}</div></td>
                          <td>
                            <div className="flex gap-1.5 opacity-60 transition-opacity group-hover:opacity-100">
                              {!node.disabled && node.tag && <button className="btn btn-sm btn-square btn-ghost text-primary hover:bg-primary/10" onClick={() => void handleProbe(node)} disabled={probingTag === node.tag || qualityBatchRunning} title="执行入池预检">{probingTag === node.tag ? <span className="loading loading-spinner loading-xs"></span> : <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M13 10V3L4 14h7v7l9-11h-7z" /></svg>}</button>}
                              {!node.disabled && node.tag && <button className="btn btn-sm btn-square btn-ghost text-secondary hover:bg-secondary/10" onClick={() => void handleQualityCheck(node)} disabled={isCheckingQuality || qualityBatchRunning} title="执行质量检测">{isCheckingQuality ? <span className="loading loading-spinner loading-xs"></span> : <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M9 12l2 2 4-4m5-2a9 9 0 11-18 0 9 9 0 0118 0z" /></svg>}</button>}
                              {node.runtime_status === 'blacklisted' && node.tag && <button className="btn btn-sm btn-square btn-ghost text-warning hover:bg-warning/10" onClick={() => void handleRelease(node)} title="解除黑名单"><svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M8 11V7a4 4 0 118 0m-4 8v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2z" /></svg></button>}
                              <button className={`btn btn-sm btn-square btn-ghost ${node.disabled ? 'text-success hover:bg-success/10' : 'text-warning hover:bg-warning/10'}`} onClick={() => void handleToggle(node)} disabled={toggling === node.name || qualityBatchRunning} title={node.disabled ? '启用该节点' : '禁用该节点'}>{toggling === node.name ? <span className="loading loading-spinner loading-xs"></span> : node.disabled ? <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M5 13l4 4L19 7" /></svg> : <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728A9 9 0 015.636 5.636m12.728 12.728L5.636 5.636" /></svg>}</button>
                              <button className="btn btn-sm btn-square btn-ghost text-info hover:bg-info/10" onClick={() => openEditModal(node)} title="编辑节点配置"><svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" /></svg></button>
                              <button className="btn btn-sm btn-square btn-ghost text-error hover:bg-error/10" onClick={() => setDeleteTarget(node.name)} title="删除节点"><svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" /></svg></button>
                            </div>
                          </td>
                        </tr>
                        {isExpanded && (
                          <tr className="border-b border-base-200/50 bg-base-200/20">
                            <td colSpan={8} className="px-4 py-4">
                              <div className="rounded-2xl border border-base-300/50 bg-base-100 px-4 py-4 shadow-sm">
                                <div className="flex flex-col gap-4 lg:flex-row lg:items-start">
                                  <div className="flex-1 space-y-3">
                                    <div className="flex flex-wrap items-center gap-2">
                                      <span className={`badge badge-sm ${lifecycleTone(node.lifecycle_state)}`}>{lifecycleLabel(node.lifecycle_state)}</span>
                                      <ActivationBadge ready={activationReady} reason={activationBlockReason} />
                                      <ProviderQualityBadge provider="OpenAI" status={detail?.quality_openai_status || node.quality_openai_status} />
                                      <ProviderQualityBadge provider="Anthropic" status={detail?.quality_anthropic_status || node.quality_anthropic_status} />
                                      <span className={`badge badge-sm ${qualityStatusTone(detail?.quality_status)}`}>{detail?.quality_grade || node.quality_grade || '-'} · {detail?.quality_status || node.quality_status || 'unknown'}</span>
                                      {typeof (detail?.quality_score ?? node.quality_score) === 'number' && <span className="badge badge-ghost badge-sm">分数 {detail?.quality_score ?? node.quality_score}</span>}
                                      <span className="text-xs text-base-content/50">{detail?.quality_checked_at ? formatQualityCheckedAt(detail.quality_checked_at) : formatQualityChecked(node.quality_checked)}</span>
                                      {isLoadingDetail && <span className="loading loading-spinner loading-xs text-primary"></span>}
                                    </div>
                                    <div className="text-sm text-base-content/75">{detail?.quality_summary || node.quality_summary || '暂无质量检测摘要'}</div>
                                    {!activationReady && activationBlockReason && <div className="text-sm text-base-content/60">激活阻塞：{activationBlockReason}</div>}
                                    <div className="hidden flex-wrap gap-2 text-xs text-base-content/60">
                                      {(detail?.exit_ip || node.exit_ip) && <span className="badge badge-ghost badge-sm">出口 IP {detail?.exit_ip || node.exit_ip}</span>}
                                      {(detail?.exit_country || node.exit_country) && <span className="badge badge-ghost badge-sm">{(detail?.exit_country_code || node.exit_country_code || '').toUpperCase()} {detail?.exit_country || node.exit_country}</span>}
                                      {(detail?.exit_region || node.exit_region) && <span className="badge badge-ghost badge-sm">区域 {detail?.exit_region || node.exit_region}</span>}
                                    </div>
                                  </div>
                                  {node.tag && <button className="btn btn-sm btn-secondary" onClick={() => void handleQualityCheck(node)} disabled={isCheckingQuality}>{isCheckingQuality ? <span className="loading loading-spinner loading-xs"></span> : '重新检测'}</button>}
                                </div>
                                <div className="mt-4">
                                  {detail?.items && detail.items.length > 0 ? (
                                    <div className="grid gap-2">
                                      {detail.items.map((item, index) => (
                                        <div key={`${item.target}-${index}`} className="flex flex-col gap-2 rounded-xl border border-base-300/40 bg-base-200/20 px-3 py-2 text-sm lg:flex-row lg:items-center">
                                          <div className="min-w-[120px] font-medium text-base-content">{item.target}</div>
                                          <div className={`badge badge-sm ${qualityStatusTone(item.status)}`}>{item.status}</div>
                                          {typeof item.http_status === 'number' && <div className="text-base-content/55">HTTP {item.http_status}</div>}
                                          {typeof item.latency_ms === 'number' && <div className="text-base-content/55">{item.latency_ms} ms</div>}
                                          {item.message && <div className="break-all text-base-content/60">{item.message}</div>}
                                        </div>
                                      ))}
                                    </div>
                                  ) : (
                                    <div className="text-sm text-base-content/45">{node.tag ? '暂无详细检查项，展开时会按需加载。' : '当前仅能展示已持久化的质量摘要。'}</div>
                                  )}
                                </div>
                              </div>
                            </td>
                          </tr>
                        )}
                      </Fragment>
                    )
                  })
                )}
              </tbody>
            </table>
          </div>
        </div>

        <div className="flex flex-col items-center justify-between gap-3 text-xs text-base-content/40 sm:flex-row">
          <div>筛选显示 {pageData?.filtered_total ?? 0} / {pageData?.total ?? 0} 个节点 · 第 {query.page} / {totalPages} 页</div>
          <div className="join">
            <button className="join-item btn btn-sm" onClick={() => updateQuery(prev => ({ ...prev, page: Math.max(1, prev.page - 1) }))} disabled={listLoading || query.page <= 1}>上一页</button>
            <button className="join-item btn btn-sm" onClick={() => updateQuery(prev => ({ ...prev, page: Math.min(totalPages, prev.page + 1) }))} disabled={listLoading || query.page >= totalPages}>下一页</button>
          </div>
        </div>

        {modalOpen && (
          <div className="modal modal-open">
            <div className="modal-box">
              <h3 className="mb-4 text-xl font-bold">{editingNode ? `编辑节点: ${editingNode}` : '添加节点'}</h3>
              <form onSubmit={handleSubmit}>
                {formError && <div className="alert alert-error mb-3 py-2 text-sm"><span>{formError}</span></div>}
                <fieldset className="fieldset mb-3">
                  <legend className="fieldset-legend">名称 *</legend>
                  <input type="text" className="input input-sm w-full" placeholder="节点名称" value={form.name} onChange={(event) => setForm(prev => ({ ...prev, name: event.target.value }))} disabled={Boolean(editingNode)} />
                </fieldset>
                <fieldset className="fieldset mb-3">
                  <legend className="fieldset-legend">URI *</legend>
                  <input type="text" className="input input-sm w-full font-mono text-xs" placeholder="trojan://password@host:port?..." value={form.uri} onChange={(event) => setForm(prev => ({ ...prev, uri: event.target.value }))} />
                </fieldset>
                <fieldset className="fieldset mb-3">
                  <legend className="fieldset-legend">本地代理端口</legend>
                  <input type="number" className="input input-sm w-full" placeholder="0 = 自动分配" value={form.port || ''} onChange={(event) => setForm(prev => ({ ...prev, port: parseInt(event.target.value, 10) || 0 }))} min={0} max={65535} />
                </fieldset>
                <div className="mb-4 grid grid-cols-2 gap-3">
                  <fieldset className="fieldset">
                    <legend className="fieldset-legend">用户名</legend>
                    <input type="text" className="input input-sm w-full" placeholder="可选" value={form.username} onChange={(event) => setForm(prev => ({ ...prev, username: event.target.value }))} />
                  </fieldset>
                  <fieldset className="fieldset">
                    <legend className="fieldset-legend">密码</legend>
                    <input type="text" className="input input-sm w-full" placeholder="可选" value={form.password} onChange={(event) => setForm(prev => ({ ...prev, password: event.target.value }))} />
                  </fieldset>
                </div>
                <div className="modal-action">
                  <button type="button" className="btn btn-ghost" onClick={() => setModalOpen(false)}>取消</button>
                  <button type="submit" className="btn btn-primary" disabled={submitting}>{submitting ? <span className="loading loading-spinner loading-xs"></span> : (editingNode ? '更新' : '添加')}</button>
                </div>
              </form>
            </div>
            <form method="dialog" className="modal-backdrop" onClick={() => setModalOpen(false)}><button>close</button></form>
          </div>
        )}

        {importModalOpen && (
          <div className="modal modal-open">
            <div className="modal-box max-w-2xl">
              <h3 className="mb-4 text-xl font-bold">导入节点</h3>
              {importError && <div className="alert alert-error mb-3 py-2 text-sm"><span>{importError}</span></div>}
              {importResult && (
                <div className={`alert mb-3 py-2 text-sm ${importResult.imported > 0 ? 'alert-success' : 'alert-warning'}`}>
                  <div>
                    <span>{importResult.message}</span>
                    {importResult.errors && importResult.errors.length > 0 && (
                      <details className="mt-2">
                        <summary className="cursor-pointer text-xs opacity-70">{importResult.errors.length} 个错误</summary>
                        <ul className="mt-1 space-y-0.5 text-xs">
                          {importResult.errors.map((item, index) => <li key={index} className="opacity-70">• {item}</li>)}
                        </ul>
                      </details>
                    )}
                  </div>
                </div>
              )}
              <p className="mb-3 text-sm text-base-content/60">每行一个代理 URI（支持 trojan://、vless://、vmess://、ss://、hysteria2:// 等），可以直接粘贴导出文件的内容或从文件导入。</p>
              <label className="form-control mb-3">
                <div className="label pb-1">
                  <span className="label-text text-sm font-medium">命名前缀</span>
                </div>
                <input
                  type="text"
                  className="input input-bordered w-full"
                  value={importNamePrefix}
                  onChange={(event) => setImportNamePrefix(event.target.value)}
                  placeholder="imported"
                />
                <div className="label pt-1">
                  <span className="label-text-alt text-xs text-base-content/50">无名称或重名节点会自动命名为 &lt;前缀&gt;-序号</span>
                </div>
              </label>
              <div className="mb-3">
                <label className="btn btn-soft btn-sm">
                  <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M7 16a4 4 0 01-.88-7.903A5 5 0 1115.9 6L16 6a5 5 0 011 9.9M15 13l-3-3m0 0l-3 3m3-3v12" /></svg>
                  选择文件
                  <input type="file" accept=".txt,.conf,.list" className="hidden" onChange={handleFileImport} />
                </label>
              </div>
              <textarea className="textarea textarea-bordered h-48 w-full font-mono text-xs" placeholder={'trojan://password@host:port?sni=example.com#节点名称\nvless://uuid@host:port?encryption=none#另一个节点\n...'} value={importContent} onChange={(event) => setImportContent(event.target.value)} />
              <div className="mt-1 text-xs text-base-content/40">{importContent.trim() ? `${importContent.trim().split('\n').filter(line => line.trim() && !line.trim().startsWith('#')).length} 行有效内容` : '等待输入...'}</div>
              {importResult && importResult.renamed > 0 && (
                <div className="mt-2 text-xs text-base-content/70">自动改名 {importResult.renamed} 个节点</div>
              )}
              {importResult?.items && importResult.items.length > 0 && (
                <details className="mt-2 rounded-lg border border-base-300/60 bg-base-200/40 p-3">
                  <summary className="cursor-pointer text-xs font-medium text-base-content/70">查看导入命名结果</summary>
                  <ul className="mt-2 space-y-1 text-xs text-base-content/70">
                    {importResult.items.map((item) => (
                      <li key={`${item.line}-${item.final_name}`}>
                        第 {item.line} 行：{item.requested_name || '(无名称)'} -&gt; {item.final_name}
                      </li>
                    ))}
                  </ul>
                </details>
              )}
              <div className="modal-action">
                <button type="button" className="btn btn-ghost" onClick={() => setImportModalOpen(false)}>{importResult?.imported ? '完成' : '取消'}</button>
                <button type="button" className="btn btn-primary" onClick={() => void handleImport()} disabled={importing || !importContent.trim()}>{importing ? <span className="loading loading-spinner loading-xs"></span> : '导入'}</button>
              </div>
            </div>
            <form method="dialog" className="modal-backdrop" onClick={() => !importing && setImportModalOpen(false)}><button>close</button></form>
          </div>
        )}

        {deleteTarget && (
          <div className="modal modal-open">
            <div className="modal-box max-w-sm">
              <h3 className="mb-2 text-lg font-bold">确认删除</h3>
              <p className="text-base-content/70">确定要删除节点 <strong>{deleteTarget}</strong> 吗？此操作不可撤销。</p>
              <div className="modal-action">
                <button className="btn btn-ghost" onClick={() => setDeleteTarget(null)} disabled={deleting}>取消</button>
                <button className="btn btn-error" onClick={() => void handleDelete()} disabled={deleting}>{deleting ? <span className="loading loading-spinner loading-xs"></span> : '删除'}</button>
              </div>
            </div>
            <form method="dialog" className="modal-backdrop" onClick={() => !deleting && setDeleteTarget(null)}><button>close</button></form>
          </div>
        )}

        {batchDeleteConfirm && (
          <div className="modal modal-open">
            <div className="modal-box max-w-sm">
              <h3 className="mb-2 text-lg font-bold">确认批量删除</h3>
              <p className="text-base-content/70">确定要删除选中的 <strong>{selectionCount}</strong> 个节点吗？此操作不可撤销。</p>
              <div className="modal-action">
                <button className="btn btn-ghost" onClick={() => setBatchDeleteConfirm(false)} disabled={batchProcessing || probeJobRunning || qualityBatchRunning}>取消</button>
                <button className="btn btn-error" onClick={() => void handleBatchDelete()} disabled={batchProcessing || probeJobRunning || qualityBatchRunning}>{batchProcessing || probeJobRunning || qualityBatchRunning ? <span className="loading loading-spinner loading-xs"></span> : `删除 ${selectionCount} 个节点`}</button>
              </div>
            </div>
            <form method="dialog" className="modal-backdrop" onClick={() => !(batchProcessing || probeJobRunning || qualityBatchRunning) && setBatchDeleteConfirm(false)}><button>close</button></form>
          </div>
        )}
      </div>
    </div>
  )
}
