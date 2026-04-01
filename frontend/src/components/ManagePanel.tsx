import { useState, useEffect, useCallback, useMemo } from 'react'
import type { ConfigNodeConfig, ConfigNodePayload, NodesResponse } from '../types'
import {
  fetchConfigNodes, createConfigNode, updateConfigNode, deleteConfigNode,
  toggleConfigNode, batchToggleConfigNodes, batchDeleteConfigNodes, triggerReload,
  importNodes, exportProxies,
  fetchNodes, probeNode, releaseNode, startProbeBatchJob, fetchProbeBatchJobStatus, cancelProbeBatchJob,
} from '../api/client'
import type { BatchProbeJob } from '../types'
import { Fragment, useRef } from 'react'
import type { NodeQualityCheckResult } from '../types'
import { checkNodeQuality, getNodeQuality, checkNodeQualityBatch } from '../api/client'
import {
  applyQualityResultToConfigNode,
  buildQualityResultFromBatchProgress,
  buildQualityCacheEntry,
  reduceBatchQualityEvent,
} from './managePanelQuality.ts'
import type { BatchQualityState } from './managePanelQuality.ts'
import { getBatchProbeSelection, mergeManageNodes } from './managePanelNodes.ts'
import type { MergedManageNode as MergedNode } from './managePanelNodes.ts'

// ---- Helpers ----

type ManageSortKey = 'name' | 'status' | 'latency' | 'region' | 'port' | 'source'
type SortDir = 'asc' | 'desc'
type StatusFilter = '' | 'normal' | 'unavailable' | 'blacklisted' | 'pending' | 'disabled'

function statusOrder(s: MergedNode['runtimeStatus']): number {
  switch (s) {
    case 'normal': return 0
    case 'pending': return 1
    case 'unavailable': return 2
    case 'blacklisted': return 3
    case 'disabled': return 4
    default: return 5
  }
}

function compareManageNodes(a: MergedNode, b: MergedNode, key: ManageSortKey, dir: SortDir): number {
  let cmp = 0
  switch (key) {
    case 'name':
      cmp = a.name.localeCompare(b.name)
      break
    case 'status':
      cmp = statusOrder(a.runtimeStatus) - statusOrder(b.runtimeStatus)
      break
    case 'latency': {
      const la = a.latency_ms < 0 ? Infinity : a.latency_ms
      const lb = b.latency_ms < 0 ? Infinity : b.latency_ms
      cmp = la - lb
      break
    }
    case 'region':
      cmp = (a.region || a.country || '').localeCompare(b.region || b.country || '')
      break
    case 'port':
      cmp = (a.port || 0) - (b.port || 0)
      break
    case 'source':
      cmp = (a.source || '').localeCompare(b.source || '')
      break
  }
  return dir === 'asc' ? cmp : -cmp
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

function qualityStatusTone(status?: string): string {
  switch (status) {
    case 'healthy':
    case 'pass':
      return 'badge-success border-none bg-success/15 text-success'
    case 'warn':
    case 'degraded':
      return 'badge-warning border-none bg-warning/15 text-warning-content'
    case 'fail':
    case 'failed':
    case 'unhealthy':
      return 'badge-error border-none bg-error/15 text-error'
    default:
      return 'badge-ghost border-none bg-base-300/40 text-base-content/50'
  }
}

function formatQualityChecked(epochSeconds?: number): string {
  if (!epochSeconds) return '未检测'
  return new Date(epochSeconds * 1000).toLocaleString()
}

function formatQualityCheckedAt(iso?: string): string {
  if (!iso) return '未检测'
  return new Date(iso).toLocaleString()
}

function SortIcon({ active, dir }: { active: boolean; dir: SortDir }) {
  if (!active) {
    return (
      <svg xmlns="http://www.w3.org/2000/svg" className="h-3 w-3 opacity-30 ml-0.5 inline" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M7 16V4m0 0L3 8m4-4l4 4m6 0v12m0 0l4-4m-4 4l-4-4" />
      </svg>
    )
  }
  return dir === 'asc' ? (
    <svg xmlns="http://www.w3.org/2000/svg" className="h-3 w-3 opacity-70 ml-0.5 inline" fill="none" viewBox="0 0 24 24" stroke="currentColor">
      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M5 15l7-7 7 7" />
    </svg>
  ) : (
    <svg xmlns="http://www.w3.org/2000/svg" className="h-3 w-3 opacity-70 ml-0.5 inline" fill="none" viewBox="0 0 24 24" stroke="currentColor">
      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M19 9l-7 7-7-7" />
    </svg>
  )
}

function StatusBadge({ status }: { status: MergedNode['runtimeStatus'] }) {
  switch (status) {
    case 'normal':
      return <span className="badge badge-success badge-sm border-none bg-success/15 text-success font-medium flex gap-1 items-center px-2 py-3.5"><div className="w-1.5 h-1.5 rounded-full bg-success"></div>正常</span>
    case 'unavailable':
      return <span className="badge badge-error badge-sm border-none bg-error/15 text-error font-medium flex gap-1 items-center px-2 py-3.5"><div className="w-1.5 h-1.5 rounded-full bg-error"></div>不可用</span>
    case 'blacklisted':
      return <span className="badge badge-error badge-sm border-none bg-error/30 text-error font-bold flex gap-1 items-center px-2 py-3.5"><svg xmlns="http://www.w3.org/2000/svg" className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2.5" d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728A9 9 0 015.636 5.636m12.728 12.728L5.636 5.636" /></svg>黑名单</span>
    case 'pending':
      return <span className="badge badge-warning badge-sm border-none bg-warning/15 text-warning-content font-medium flex gap-1 items-center px-2 py-3.5"><div className="w-1.5 h-1.5 rounded-full bg-warning animate-pulse"></div>待检查</span>
    case 'disabled':
      return <span className="badge badge-ghost badge-sm border-none bg-base-300/50 text-base-content/50 font-medium px-2 py-3.5">已禁用</span>
    default:
      return <span className="badge badge-ghost badge-sm border-none px-2 py-3.5">未知</span>
  }
}

const emptyPayload: ConfigNodePayload = {
  name: '',
  uri: '',
  port: 0,
  username: '',
  password: '',
}

// ---- Component ----

export default function ManagePanel() {
  const [configNodes, setConfigNodes] = useState<ConfigNodeConfig[]>([])
  const [monitorData, setMonitorData] = useState<NodesResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [needReload, setNeedReload] = useState(false)

  // Modal state
  const [modalOpen, setModalOpen] = useState(false)
  const [editingNode, setEditingNode] = useState<string | null>(null)
  const [form, setForm] = useState<ConfigNodePayload>(emptyPayload)
  const [formError, setFormError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  // Delete confirm
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)

  // Toggle state
  const [toggling, setToggling] = useState<string | null>(null)

  // Probe state
  const [probingTag, setProbingTag] = useState<string | null>(null)
  const [qualityLoadingKey, setQualityLoadingKey] = useState<string | null>(null)
  const [expandedQualityNode, setExpandedQualityNode] = useState<string | null>(null)
  const [qualityDetails, setQualityDetails] = useState<Record<string, NodeQualityCheckResult>>({})
  const [batchQualityState, setBatchQualityState] = useState<BatchQualityState | null>(null)
  const batchQualityControllerRef = useRef<AbortController | null>(null)

  // Batch selection
  const [selectedNodes, setSelectedNodes] = useState<Set<string>>(new Set())
  const [batchProcessing, setBatchProcessing] = useState(false)
  const [batchDeleteConfirm, setBatchDeleteConfirm] = useState(false)
  const [batchProbeProgress, setBatchProbeProgress] = useState<{ current: number; total: number } | null>(null)
  const [activeProbeJob, setActiveProbeJob] = useState<BatchProbeJob | null>(null)
  const [lastProbeJobStatus, setLastProbeJobStatus] = useState<string | null>(null)

  // Filters
  const [filter, setFilter] = useState('')
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('')
  const [regionFilter, setRegionFilter] = useState('')
  const [sourceFilter, setSourceFilter] = useState('')

  // Sort
  const [sortKey, setSortKey] = useState<ManageSortKey>('name')
  const [sortDir, setSortDir] = useState<SortDir>('asc')

  // Import state
  const [importModalOpen, setImportModalOpen] = useState(false)
  const [importContent, setImportContent] = useState('')
  const [importing, setImporting] = useState(false)
  const [importError, setImportError] = useState('')
  const [importResult, setImportResult] = useState<{ message: string; imported: number; errors?: string[] } | null>(null)

  // ---- Data loading ----

  const loadData = useCallback(async () => {
    try {
      setError('')
      const [configRes, monitorRes] = await Promise.all([
        fetchConfigNodes(),
        fetchNodes().catch(() => null), // monitor data is optional
      ])
      setConfigNodes(configRes.nodes || [])
      if (monitorRes) setMonitorData(monitorRes)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载节点失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    loadData()
  }, [loadData])

  useEffect(() => {
    if (success) {
      const timer = setTimeout(() => setSuccess(''), 5000)
      return () => clearTimeout(timer)
    }
  }, [success])

  useEffect(() => {
    return () => {
      batchQualityControllerRef.current?.abort()
    }
  }, [])

  useEffect(() => {
    let disposed = false

    const syncBatchProbeJob = async () => {
      try {
        const res = await fetchProbeBatchJobStatus()
        if (disposed) return
        setActiveProbeJob(res.job)
        if (res.job && (res.job.status === 'queued' || res.job.status === 'running')) {
          setBatchProbeProgress({ current: res.job.completed, total: res.job.total })
        } else {
          setBatchProbeProgress(null)
        }
      } catch {
        if (!disposed) {
          setActiveProbeJob(null)
          setBatchProbeProgress(null)
        }
      }
    }

    void syncBatchProbeJob()
    const timer = window.setInterval(() => {
      void syncBatchProbeJob()
    }, 1000)

    return () => {
      disposed = true
      window.clearInterval(timer)
    }
  }, [])

  // ---- Merge config + monitor data ----

  const mergedNodes = useMemo((): MergedNode[] => {
    return mergeManageNodes(configNodes, monitorData?.nodes || [])
  }, [configNodes, monitorData])

  // ---- Filtering ----

  const regions = useMemo(() => {
    const set = new Set<string>()
    for (const n of mergedNodes) {
      if (n.region) set.add(n.region)
    }
    return Array.from(set).sort()
  }, [mergedNodes])

  const sources = useMemo(() => {
    const set = new Set<string>()
    for (const n of mergedNodes) {
      if (n.source) set.add(n.source)
    }
    return Array.from(set).sort()
  }, [mergedNodes])

  const filteredNodes = useMemo(() => {
    return mergedNodes.filter(n => {
      if (filter) {
        const q = filter.toLowerCase()
        if (!n.name.toLowerCase().includes(q) &&
            !n.uri.toLowerCase().includes(q) &&
            !(n.country || '').toLowerCase().includes(q) &&
            !(n.region || '').toLowerCase().includes(q)) {
          return false
        }
      }
      if (statusFilter && n.runtimeStatus !== statusFilter) return false
      if (regionFilter && n.region !== regionFilter) return false
      if (sourceFilter && n.source !== sourceFilter) return false
      return true
    })
  }, [mergedNodes, filter, statusFilter, regionFilter, sourceFilter])

  const sortedNodes = useMemo(() => {
    return [...filteredNodes].sort((a, b) => compareManageNodes(a, b, sortKey, sortDir))
  }, [filteredNodes, sortKey, sortDir])

  const probeJobRunning = activeProbeJob?.status === 'queued' || activeProbeJob?.status === 'running'
  const qualityBatchRunning = batchQualityState?.status === 'running'

  useEffect(() => {
    const current = activeProbeJob?.status ?? null
    if (lastProbeJobStatus && current && current !== lastProbeJobStatus && current !== 'queued' && current !== 'running') {
      void loadData()
    }
    if (current !== lastProbeJobStatus) {
      setLastProbeJobStatus(current)
    }
  }, [activeProbeJob?.status, lastProbeJobStatus, loadData])

  // ---- Handlers ----

  const handleSort = (key: ManageSortKey) => {
    if (sortKey === key) {
      setSortDir(d => d === 'asc' ? 'desc' : 'asc')
    } else {
      setSortKey(key)
      setSortDir('asc')
    }
  }

  const openCreateModal = () => {
    setEditingNode(null)
    setForm(emptyPayload)
    setFormError('')
    setModalOpen(true)
  }

  const openEditModal = (node: MergedNode) => {
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
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.name.trim()) { setFormError('节点名称不能为空'); return }
    if (!form.uri.trim()) { setFormError('URI 不能为空'); return }

    setSubmitting(true)
    setFormError('')
    try {
      if (editingNode) {
        const res = await updateConfigNode(editingNode, form)
        setSuccess(res.message || '节点已更新')
      } else {
        const res = await createConfigNode(form)
        setSuccess(res.message || '节点已添加')
      }
      setNeedReload(true)
      setModalOpen(false)
      await loadData()
    } catch (err) {
      setFormError(err instanceof Error ? err.message : '操作失败')
    } finally {
      setSubmitting(false)
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      const res = await deleteConfigNode(deleteTarget)
      setSuccess(res.message || '节点已删除')
      setNeedReload(true)
      setDeleteTarget(null)
      await loadData()
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除失败')
    } finally {
      setDeleting(false)
    }
  }

  const handleToggle = async (node: MergedNode) => {
    const newEnabled = !!node.disabled
    setToggling(node.name)
    try {
      const res = await toggleConfigNode(node.name, newEnabled)
      setSuccess(res.message || (newEnabled ? '节点已启用' : '节点已禁用'))
      await loadData()
    } catch (err) {
      setError(err instanceof Error ? err.message : '操作失败')
    } finally {
      setToggling(null)
    }
  }

  const handleProbe = async (tag: string) => {
    setProbingTag(tag)
    try {
      await probeNode(tag)
      await loadData()
    } catch (err) {
      setError(err instanceof Error ? err.message : '探测失败')
    } finally {
      setProbingTag(null)
    }
  }

  const handleRelease = async (tag: string) => {
    try {
      await releaseNode(tag)
      setSuccess('已解除黑名单')
      await loadData()
    } catch (err) {
      setError(err instanceof Error ? err.message : '解除失败')
    }
  }

  const applyQualityResult = useCallback((nodeName: string, result: NodeQualityCheckResult) => {
    setQualityDetails(prev => ({ ...prev, [nodeName]: result }))
    setConfigNodes(prev => prev.map(node => (
      node.name === nodeName ? applyQualityResultToConfigNode(node, result) : node
    )))
  }, [])

  const handleQualityCheck = async (node: MergedNode) => {
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
      await loadData()
    } catch (err) {
      setError(err instanceof Error ? err.message : '质量检测失败')
    } finally {
      setQualityLoadingKey(current => current === loadingKey ? null : current)
    }
  }

  const handleToggleQualityDetails = async (node: MergedNode) => {
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
  }

  // ---- Batch ----

  const toggleSelectNode = (name: string) => {
    setSelectedNodes(prev => {
      const next = new Set(prev)
      if (next.has(name)) next.delete(name)
      else next.add(name)
      return next
    })
  }

  const toggleSelectAll = () => {
    if (selectedNodes.size === sortedNodes.length) {
      setSelectedNodes(new Set())
    } else {
      setSelectedNodes(new Set(sortedNodes.map(n => n.name)))
    }
  }

  const handleBatchToggle = async (enabled: boolean) => {
    if (selectedNodes.size === 0) return
    setBatchProcessing(true)
    try {
      const res = await batchToggleConfigNodes(Array.from(selectedNodes), enabled)
      setSuccess(res.message || '批量操作完成')
      setSelectedNodes(new Set())
      await loadData()
    } catch (err) {
      setError(err instanceof Error ? err.message : '批量操作失败')
    } finally {
      setBatchProcessing(false)
    }
  }

  const handleBatchProbe = async () => {
    const selection = getBatchProbeSelection(sortedNodes, selectedNodes)
    if (selection.probeable.length === 0) {
      setError('所选节点中没有可探测的节点（已禁用或无运行时标识的节点将被跳过）')
      return
    }

    try {
      const res = await startProbeBatchJob(selection.probeable.map(n => n.tag!))
      setActiveProbeJob(res.job)
      setBatchProbeProgress({ current: res.job.completed, total: res.job.total })
      if (selection.skippedTotal > 0) {
        const skippedParts: string[] = []
        if (selection.skippedNoTag > 0) skippedParts.push(`${selection.skippedNoTag} 个未加载到运行时的节点`)
        if (selection.skippedDisabled > 0) skippedParts.push(`${selection.skippedDisabled} 个已禁用节点`)
        setSuccess(`批量探测任务已启动，已跳过 ${skippedParts.join('、')}`)
      } else {
        setSuccess('批量探测任务已启动')
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : '批量探测启动失败')
    }
  }

  const handleBatchProbeCancel = async () => {
    if (!activeProbeJob) return
    try {
      await cancelProbeBatchJob(activeProbeJob.id)
      setSuccess('批量探测任务已取消')
      const res = await fetchProbeBatchJobStatus()
      setActiveProbeJob(res.job)
      if (!res.job || (res.job.status !== 'queued' && res.job.status !== 'running')) {
        setBatchProbeProgress(null)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : '取消批量探测失败')
    }
  }

  const handleBatchQualityCheck = async () => {
    const nodesToCheck = sortedNodes.filter(n => selectedNodes.has(n.name) && !n.disabled && n.tag)
    if (nodesToCheck.length === 0) {
      setError('所选节点中没有可做质量检测的节点（已禁用或无运行时标识的节点将被跳过）')
      return
    }

    batchQualityControllerRef.current?.abort()
    const nameByTag = new Map(nodesToCheck.map(node => [node.tag!, node.name]))
    setBatchQualityState({
      status: 'running',
      total: nodesToCheck.length,
      current: 0,
      success: 0,
      failed: 0,
      lastResult: null,
    })

    const controller = checkNodeQualityBatch(
      nodesToCheck.map(node => node.tag!),
      (event) => {
        setBatchQualityState(prev => reduceBatchQualityEvent(prev, event))

        if (event.type === 'progress' && event.status === 'success') {
          const nodeName = nameByTag.get(event.tag)
          if (nodeName) {
            const result = buildQualityResultFromBatchProgress(event)
            if (result) {
              applyQualityResult(nodeName, result)
            }
          }
        }

        if (event.type === 'complete') {
          batchQualityControllerRef.current = null
          setSuccess(`批量质量检测完成：成功 ${event.success}，失败 ${event.failed}`)
          void loadData()
        }
      },
      (err) => {
        batchQualityControllerRef.current = null
        setError(err instanceof Error ? err.message : '批量质量检测失败')
        setBatchQualityState(prev => prev ? { ...prev, status: 'completed' } : prev)
      },
    )

    batchQualityControllerRef.current = controller
  }

  const handleBatchDelete = async () => {
    if (selectedNodes.size === 0) return
    setBatchProcessing(true)
    setBatchDeleteConfirm(false)
    try {
      const res = await batchDeleteConfigNodes(Array.from(selectedNodes))
      setSuccess(res.message || '批量删除完成')
      setSelectedNodes(new Set())
      await loadData()
    } catch (err) {
      setError(err instanceof Error ? err.message : '批量删除失败')
    } finally {
      setBatchProcessing(false)
    }
  }

  // ---- Import / Export ----

  const openImportModal = () => {
    setImportContent('')
    setImportError('')
    setImportResult(null)
    setImportModalOpen(true)
  }

  const handleFileImport = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = (ev) => {
      const text = ev.target?.result
      if (typeof text === 'string') setImportContent(text)
    }
    reader.readAsText(file)
    e.target.value = ''
  }

  const handleImport = async () => {
    if (!importContent.trim()) { setImportError('请输入节点 URI'); return }
    setImporting(true)
    setImportError('')
    setImportResult(null)
    try {
      const res = await importNodes(importContent)
      setImportResult(res)
      if (res.imported > 0) {
        setNeedReload(true)
        setSuccess(res.message)
        await loadData()
      }
    } catch (err) {
      setImportError(err instanceof Error ? err.message : '导入失败')
    } finally {
      setImporting(false)
    }
  }

  const handleExport = async () => {
    try {
      const text = await exportProxies()
      if (!text.trim()) { setError('没有可导出的节点'); return }
      const blob = new Blob([text], { type: 'text/plain' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = 'nodes_export.txt'
      a.click()
      URL.revokeObjectURL(url)
      setSuccess('节点已导出')
    } catch (err) {
      setError(err instanceof Error ? err.message : '导出失败')
    }
  }

  const handleReload = async () => {
    try {
      setError('')
      const res = await triggerReload()
      setSuccess(res.message || '重载成功')
      setNeedReload(false)
      await loadData()
    } catch (err) {
      setError(err instanceof Error ? err.message : '重载失败')
    }
  }

  // ---- Source label ----
  const sourceLabel = (source?: string) => {
    switch (source) {
      case 'inline': return '配置文件'
      case 'nodes_file': return '节点文件'
      case 'subscription': return '订阅'
      case 'manual': return '手动添加'
      default: return source || '-'
    }
  }

  // ---- Stats ----
  const disabledCount = mergedNodes.filter(n => n.runtimeStatus === 'disabled').length
  const blacklistedCount = mergedNodes.filter(n => n.runtimeStatus === 'blacklisted').length
  const normalCount = mergedNodes.filter(n => n.runtimeStatus === 'normal').length

  // ---- Render ----

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <span className="loading loading-spinner loading-lg text-primary"></span>
      </div>
    )
  }

  const thClass = "font-semibold cursor-pointer select-none hover:text-primary transition-colors"

  return (
    <div className="flex flex-col min-h-full animate-in fade-in duration-500">
      {/* Header */}
      <div className="sticky top-0 z-30 bg-base-100/80 backdrop-blur-xl px-4 lg:px-8 py-4 border-b border-base-300/60 shadow-sm">
        <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4 max-w-[1600px] mx-auto w-full">
          <div>
            <h2 className="text-2xl font-bold flex items-center gap-3">
              <div className="w-10 h-10 rounded-xl bg-primary/10 flex items-center justify-center text-primary shrink-0 border border-primary/20">
                <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2.5" d="M4 6h16M4 10h16M4 14h16M4 18h16" />
                </svg>
              </div>
              节点管理
            </h2>
            <div className="text-sm font-medium text-base-content/50 mt-1.5 ml-[3.25rem] flex items-center gap-2">
              <span>共 <strong className="text-base-content/80">{mergedNodes.length}</strong> 个节点</span>
              {normalCount > 0 && <span className="badge badge-success badge-xs border-none bg-success/15 text-success">正常 {normalCount}</span>}
              {blacklistedCount > 0 && <span className="badge badge-error badge-xs border-none bg-error/15 text-error">黑名单 {blacklistedCount}</span>}
              {disabledCount > 0 && <span className="badge badge-ghost badge-xs bg-base-200 text-base-content/50">禁用 {disabledCount}</span>}
            </div>
          </div>
          <div className="flex items-center gap-2">
            <button className="btn btn-sm lg:btn-md btn-primary shadow-sm gap-2" onClick={openCreateModal}>
              <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2.5" d="M12 4v16m8-8H4" /></svg>
              添加节点
            </button>
            <div className="dropdown dropdown-end">
              <div tabIndex={0} role="button" className="btn btn-ghost border border-base-300 btn-sm lg:btn-md gap-2 shadow-sm">
                管理操作
                <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4 opacity-50" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M19 9l-7 7-7-7" /></svg>
              </div>
              <ul tabIndex={0} className="dropdown-content menu bg-base-100 border border-base-200 rounded-xl z-20 w-48 p-2 shadow-xl mt-2">
                <li><a onClick={openImportModal} className="hover:bg-primary/10 hover:text-primary gap-3"><svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-8l-4-4m0 0L8 8m4-4v12" /></svg> 导入节点配置</a></li>
                <li><a onClick={handleExport} className="hover:bg-primary/10 hover:text-primary gap-3"><svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" /></svg> 导出所有节点</a></li>
              </ul>
            </div>
            {needReload && (
              <button className="btn btn-warning btn-sm lg:btn-md shadow-sm gap-2 animate-pulse" onClick={handleReload}>
                <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" /></svg>
                重载生效
              </button>
            )}
          </div>
        </div>
      </div>

      <div className="p-4 lg:p-8 space-y-6 flex-1 max-w-[1600px] mx-auto w-full">
        {/* Alerts */}
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
          <span>配置已变更，请点击「重载配置」使其生效</span>
        </div>
      )}
      {activeProbeJob && (
        <div className={`rounded-2xl border px-5 py-4 shadow-sm ${probeJobRunning ? 'border-primary/30 bg-primary/5' : 'border-base-300/50 bg-base-100'}`}>
          <div className="flex flex-col lg:flex-row lg:items-center gap-3">
            <div className="flex-1">
              <div className="text-sm font-semibold text-base-content">
                批量探测任务
                <span className="ml-2 badge badge-sm">{activeProbeJob.status}</span>
              </div>
              <div className="text-xs text-base-content/60 mt-1">
                进度 {activeProbeJob.completed}/{activeProbeJob.total} · 成功 {activeProbeJob.success} · 失败 {activeProbeJob.failed} · 活跃工作线程 {activeProbeJob.active_workers}
              </div>
              {activeProbeJob.last_result && (
                <div className="text-xs text-base-content/50 mt-1">
                  最近完成: {activeProbeJob.last_result.name}
                  {activeProbeJob.last_result.error ? ` · ${activeProbeJob.last_result.error}` : ` · ${activeProbeJob.last_result.latency_ms} ms`}
                </div>
              )}
            </div>
            {probeJobRunning && (
              <button className="btn btn-sm btn-warning" onClick={handleBatchProbeCancel}>
                取消批量探测
              </button>
            )}
          </div>
          <progress
            className="progress progress-primary w-full h-2 mt-3"
            value={activeProbeJob.completed}
            max={activeProbeJob.total || 1}
          ></progress>
        </div>
      )}
      {batchQualityState && (
        <div className={`rounded-2xl border px-5 py-4 shadow-sm ${qualityBatchRunning ? 'border-secondary/30 bg-secondary/5' : 'border-base-300/50 bg-base-100'}`}>
          <div className="flex flex-col lg:flex-row lg:items-center gap-3">
            <div className="flex-1">
              <div className="text-sm font-semibold text-base-content">
                批量质量检测
                <span className="ml-2 badge badge-sm">{batchQualityState.status}</span>
              </div>
              <div className="text-xs text-base-content/60 mt-1">
                进度 {batchQualityState.current}/{batchQualityState.total} · 成功 {batchQualityState.success} · 失败 {batchQualityState.failed}
              </div>
              {batchQualityState.lastResult && (
                <div className="text-xs text-base-content/50 mt-1">
                  最近完成: {batchQualityState.lastResult.name}
                  {batchQualityState.lastResult.status === 'error'
                    ? ` · ${batchQualityState.lastResult.error || '检测失败'}`
                    : ` · ${batchQualityState.lastResult.quality_grade || '-'} / ${batchQualityState.lastResult.quality_status || 'unknown'}`}
                </div>
              )}
            </div>
          </div>
          <progress
            className="progress progress-secondary w-full h-2 mt-3"
            value={batchQualityState.current}
            max={batchQualityState.total || 1}
          ></progress>
        </div>
      )}

      {/* Filters Area */}
      <div className="bg-base-100 border border-base-300/50 rounded-2xl p-4 shadow-sm">
        <div className="flex flex-col lg:flex-row gap-4 items-center">
          <div className="relative flex-1 w-full">
            <div className="absolute inset-y-0 left-0 pl-3.5 flex items-center pointer-events-none text-base-content/40">
              <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" /></svg>
            </div>
            <input
              type="text"
              className="input input-md w-full pl-11 bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
              placeholder="搜索节点名称、URI 或 地区..."
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
          </div>
          
          <div className="flex flex-wrap sm:flex-nowrap gap-3 w-full lg:w-auto">
            <select className="select select-md bg-base-200/50 focus:bg-base-100 flex-1 sm:w-36" value={statusFilter} onChange={(e) => setStatusFilter(e.target.value as StatusFilter)}>
              <option value="">全部状态</option>
              <option value="normal">✅ 正常运行</option>
              <option value="unavailable">❌ 不可用</option>
              <option value="blacklisted">🔴 黑名单</option>
              <option value="pending">⚠️ 待检查</option>
              <option value="disabled">🚫 已禁用</option>
            </select>
            {regions.length > 0 && (
              <select className="select select-md bg-base-200/50 focus:bg-base-100 flex-1 sm:w-32" value={regionFilter} onChange={(e) => setRegionFilter(e.target.value)}>
                <option value="">全部地区</option>
                {regions.map(r => <option key={r} value={r}>{regionFlag(r)} {r.toUpperCase()}</option>)}
              </select>
            )}
            {sources.length > 1 && (
              <select className="select select-md bg-base-200/50 focus:bg-base-100 flex-1 sm:w-32" value={sourceFilter} onChange={(e) => setSourceFilter(e.target.value)}>
                <option value="">全部来源</option>
                {sources.map(s => <option key={s} value={s}>{sourceLabel(s)}</option>)}
              </select>
            )}
          </div>
        </div>
      </div>

      {/* Batch action bar */}
      <div className={`transition-all duration-300 overflow-hidden ${selectedNodes.size > 0 ? 'max-h-24 opacity-100' : 'max-h-0 opacity-0'}`}>
        <div className="flex flex-col gap-3 px-5 py-4 bg-primary/5 border border-primary/20 rounded-2xl shadow-inner relative">
          <div className="absolute left-0 top-0 bottom-0 w-1.5 bg-primary rounded-l-2xl"></div>
          <div className="flex items-center gap-4 flex-wrap">
            <span className="text-base font-medium text-base-content/80 flex items-center gap-2">
              <span className="badge badge-primary badge-md font-bold">{selectedNodes.size}</span> 项已选择
            </span>
            <div className="flex gap-2 ml-auto flex-wrap">
              <button
                className="btn btn-sm btn-primary shadow-sm gap-1.5"
                onClick={handleBatchProbe}
                disabled={batchProcessing || probeJobRunning || qualityBatchRunning}
                title="对选中的已启用节点逐个探测"
              >
                {batchProbeProgress
                  ? <><span className="loading loading-spinner loading-xs"></span> {batchProbeProgress.current}/{batchProbeProgress.total}</>
                  : <><svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M13 10V3L4 14h7v7l9-11h-7z" /></svg> 批量探测</>}
              </button>
              <button
                className="btn btn-sm btn-secondary shadow-sm gap-1.5"
                onClick={handleBatchQualityCheck}
                disabled={batchProcessing || probeJobRunning || qualityBatchRunning}
                title="对选中的已启用节点执行批量质量检测"
              >
                {qualityBatchRunning
                  ? <><span className="loading loading-spinner loading-xs"></span> {batchQualityState?.current || 0}/{batchQualityState?.total || 0}</>
                  : <><svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M9 12l2 2 4-4m5-2a9 9 0 11-18 0 9 9 0 0118 0z" /></svg> 批量质检</>}
              </button>
              <div className="w-px h-6 bg-base-300 mx-1 self-center"></div>
              <button
                className="btn btn-sm btn-success border-none bg-success/15 text-success hover:bg-success hover:text-success-content"
                onClick={() => handleBatchToggle(true)}
                disabled={batchProcessing || probeJobRunning || qualityBatchRunning}
              >
                启用
              </button>
              <button
                className="btn btn-sm btn-warning border-none bg-warning/15 text-warning-content hover:bg-warning hover:text-warning-content"
                onClick={() => handleBatchToggle(false)}
                disabled={batchProcessing || probeJobRunning || qualityBatchRunning}
              >
                禁用
              </button>
              <button
                className="btn btn-sm btn-error border-none bg-error/15 text-error hover:bg-error hover:text-error-content"
                onClick={() => setBatchDeleteConfirm(true)}
                disabled={batchProcessing || probeJobRunning || qualityBatchRunning}
              >
                删除
              </button>
              <div className="w-px h-6 bg-base-300 mx-1 self-center"></div>
              <button
                className="btn btn-sm btn-ghost hover:bg-base-300"
                onClick={() => setSelectedNodes(new Set())}
                disabled={batchProcessing || probeJobRunning || qualityBatchRunning}
              >
                取消选择
              </button>
            </div>
          </div>
          {batchProbeProgress && (
            <progress
              className="progress progress-primary w-full h-1.5 bg-primary/20"
              value={batchProbeProgress.current}
              max={batchProbeProgress.total}
            ></progress>
          )}
        </div>
      </div>

      {/* Node Table */}
      <div className="rounded-2xl border border-base-300/50 bg-base-100 shadow-sm overflow-hidden">
        <div className="overflow-x-auto overflow-y-auto max-h-[calc(100vh-280px)] min-h-[400px]">
          <table className="table table-md table-pin-rows">
            <thead>
              <tr className="bg-base-200/50 border-b border-base-300/50 shadow-sm text-base-content/70">
                <th className="w-8">
                  <input
                    type="checkbox"
                    className="checkbox checkbox-xs"
                    checked={sortedNodes.length > 0 && selectedNodes.size === sortedNodes.length}
                    onChange={toggleSelectAll}
                    ref={(el) => {
                      if (el) el.indeterminate = selectedNodes.size > 0 && selectedNodes.size < sortedNodes.length
                    }}
                  />
                </th>
                <th className={thClass} onClick={() => handleSort('name')}>
                  名称 <SortIcon active={sortKey === 'name'} dir={sortDir} />
                </th>
                <th className={thClass} onClick={() => handleSort('status')}>
                  状态 <SortIcon active={sortKey === 'status'} dir={sortDir} />
                </th>
                <th className={thClass} onClick={() => handleSort('latency')}>
                  延迟 <SortIcon active={sortKey === 'latency'} dir={sortDir} />
                </th>
                <th className={`hidden md:table-cell ${thClass}`} onClick={() => handleSort('region')}>
                  区域 <SortIcon active={sortKey === 'region'} dir={sortDir} />
                </th>
                <th className={`hidden md:table-cell ${thClass}`} onClick={() => handleSort('port')}>
                  端口 <SortIcon active={sortKey === 'port'} dir={sortDir} />
                </th>
                <th className={`hidden lg:table-cell ${thClass}`} onClick={() => handleSort('source')}>
                  来源 <SortIcon active={sortKey === 'source'} dir={sortDir} />
                </th>
                <th className="font-semibold">操作</th>
              </tr>
            </thead>
            <tbody>
              {sortedNodes.length === 0 ? (
                <tr>
                  <td colSpan={8} className="h-[300px] p-0">
                    <div className="flex flex-col items-center justify-center h-full w-full opacity-60">
                      <div className="w-16 h-16 bg-base-200 rounded-full flex items-center justify-center mb-4">
                        <svg xmlns="http://www.w3.org/2000/svg" className="h-8 w-8 text-base-content/40" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.5" d="M20 13V6a2 2 0 00-2-2H6a2 2 0 00-2 2v7m16 0v5a2 2 0 01-2 2H6a2 2 0 01-2-2v-5m16 0h-2.586a1 1 0 00-.707.293l-2.414 2.414a1 1 0 01-.707.293h-3.172a1 1 0 01-.707-.293l-2.414-2.414A1 1 0 006.586 13H4" />
                        </svg>
                      </div>
                      <p className="text-base font-medium text-base-content">
                        {filter || statusFilter || regionFilter || sourceFilter
                          ? '未找到匹配的节点数据'
                          : '暂无配置节点'}
                      </p>
                      {!(filter || statusFilter || regionFilter || sourceFilter) && (
                        <p className="text-sm text-base-content/50 mt-1">请点击右上角「添加节点」或导入配置以开始</p>
                      )}
                    </div>
                  </td>
                </tr>
              ) : (
                sortedNodes.map((node) => {
                  const detail = qualityDetails[node.name] ?? buildQualityCacheEntry(node)
                  const isExpanded = expandedQualityNode === node.name
                  const isCheckingQuality = qualityLoadingKey === `check:${node.name}`
                  const isLoadingDetail = qualityLoadingKey === `detail:${node.name}`
                  const canShowQuality = !!node.tag || !!detail

                  return (
                    <Fragment key={node.name}>
                      <tr
                        className={`
                          transition-colors border-b border-base-200/50 group
                          ${node.runtimeStatus === 'disabled' ? 'opacity-50 grayscale-[0.5]' : ''}
                          ${node.runtimeStatus === 'blacklisted' ? 'opacity-80' : ''}
                          ${selectedNodes.has(node.name) ? 'bg-primary/5' : 'hover:bg-base-200/40'}
                        `}
                      >
                        <td className="w-8">
                          <input
                            type="checkbox"
                            className="checkbox checkbox-sm"
                            checked={selectedNodes.has(node.name)}
                            onChange={() => toggleSelectNode(node.name)}
                          />
                        </td>
                        <td>
                          <div className="font-semibold text-sm flex items-center gap-2">
                            {node.region && <span className="text-lg leading-none filter drop-shadow-sm">{regionFlag(node.region)}</span>}
                            <span className="truncate max-w-[200px]" title={node.name}>{node.name}</span>
                          </div>
                          <div className="mt-1 flex flex-wrap items-center gap-2 text-xs">
                            {node.quality_grade ? (
                              <span className={`badge badge-sm ${qualityStatusTone(node.quality_status)}`}>
                                {node.quality_grade} · {node.quality_status || 'unknown'}
                              </span>
                            ) : (
                              <span className="text-base-content/35">未做质量检测</span>
                            )}
                            {typeof node.quality_score === 'number' && (
                              <span className="font-mono text-base-content/55">分数 {node.quality_score}</span>
                            )}
                            {(node.quality_checked || detail?.quality_checked_at) && (
                              <span className="text-base-content/45">
                                {node.quality_checked ? formatQualityChecked(node.quality_checked) : formatQualityCheckedAt(detail?.quality_checked_at)}
                              </span>
                            )}
                            {canShowQuality && (
                              <button
                                className="link link-hover text-info"
                                onClick={() => void handleToggleQualityDetails(node)}
                                type="button"
                              >
                                {isExpanded ? '收起详情' : '查看详情'}
                              </button>
                            )}
                          </div>
                        </td>
                        <td><StatusBadge status={node.runtimeStatus} /></td>
                        <td className={`font-mono text-sm font-medium ${latencyColor(node.latency_ms)}`}>
                          {node.latency_ms < 0 ? <span className="text-base-content/30">-</span> : `${node.latency_ms} ms`}
                        </td>
                        <td className="hidden md:table-cell text-sm text-base-content/70">
                          {node.country || node.region
                            ? <div className="badge badge-ghost badge-sm">{node.country || node.region}</div>
                            : '-'}
                        </td>
                        <td className="hidden md:table-cell font-mono text-sm text-base-content/70">{node.port || '-'}</td>
                        <td className="hidden lg:table-cell">
                          <div className="badge badge-ghost badge-sm opacity-70 bg-transparent border-base-300">{sourceLabel(node.source)}</div>
                        </td>
                        <td>
                          <div className="flex gap-1.5 opacity-60 group-hover:opacity-100 transition-opacity">
                            {!node.disabled && node.tag && (
                              <button
                                className="btn btn-sm btn-square btn-ghost text-primary hover:bg-primary/10"
                                onClick={() => handleProbe(node.tag!)}
                                disabled={probingTag === node.tag || qualityBatchRunning}
                                title="探测延迟"
                              >
                                {probingTag === node.tag
                                  ? <span className="loading loading-spinner loading-xs"></span>
                                  : <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M13 10V3L4 14h7v7l9-11h-7z" /></svg>}
                              </button>
                            )}
                            {!node.disabled && node.tag && (
                              <button
                                className="btn btn-sm btn-square btn-ghost text-secondary hover:bg-secondary/10"
                                onClick={() => void handleQualityCheck(node)}
                                disabled={isCheckingQuality || qualityBatchRunning}
                                title="执行质量检测"
                              >
                                {isCheckingQuality
                                  ? <span className="loading loading-spinner loading-xs"></span>
                                  : <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M9 12l2 2 4-4m5-2a9 9 0 11-18 0 9 9 0 0118 0z" /></svg>}
                              </button>
                            )}
                            {node.runtimeStatus === 'blacklisted' && node.tag && (
                              <button
                                className="btn btn-sm btn-square btn-ghost text-warning hover:bg-warning/10"
                                onClick={() => handleRelease(node.tag!)}
                                title="解除黑名单"
                              >
                                <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M8 11V7a4 4 0 118 0m-4 8v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2z" /></svg>
                              </button>
                            )}
                            <button
                              className={`btn btn-sm btn-square btn-ghost ${node.disabled ? 'text-success hover:bg-success/10' : 'text-warning hover:bg-warning/10'}`}
                              onClick={() => handleToggle(node)}
                              disabled={toggling === node.name || qualityBatchRunning}
                              title={node.disabled ? '启用该节点' : '禁用该节点'}
                            >
                              {toggling === node.name
                                ? <span className="loading loading-spinner loading-xs"></span>
                                : node.disabled
                                    ? <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M5 13l4 4L19 7" /></svg>
                                    : <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728A9 9 0 015.636 5.636m12.728 12.728L5.636 5.636" /></svg>
                              }
                            </button>
                            <button
                              className="btn btn-sm btn-square btn-ghost text-info hover:bg-info/10"
                              onClick={() => openEditModal(node)}
                              title="编辑节点配置"
                            >
                              <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" /></svg>
                            </button>
                            <button
                              className="btn btn-sm btn-square btn-ghost text-error hover:bg-error/10"
                              onClick={() => setDeleteTarget(node.name)}
                              title="删除节点"
                            >
                              <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" /></svg>
                            </button>
                          </div>
                        </td>
                      </tr>
                      {isExpanded && (
                        <tr className="bg-base-200/20 border-b border-base-200/50">
                          <td colSpan={8} className="px-4 py-4">
                            <div className="rounded-2xl border border-base-300/50 bg-base-100 px-4 py-4 shadow-sm">
                              <div className="flex flex-col lg:flex-row lg:items-start gap-4">
                                <div className="flex-1 space-y-3">
                                  <div className="flex flex-wrap items-center gap-2">
                                    <span className={`badge badge-sm ${qualityStatusTone(detail?.quality_status)}`}>
                                      {detail?.quality_grade || node.quality_grade || '-'} · {detail?.quality_status || node.quality_status || 'unknown'}
                                    </span>
                                    {typeof (detail?.quality_score ?? node.quality_score) === 'number' && (
                                      <span className="badge badge-ghost badge-sm">分数 {detail?.quality_score ?? node.quality_score}</span>
                                    )}
                                    <span className="text-xs text-base-content/50">
                                      {detail?.quality_checked_at ? formatQualityCheckedAt(detail.quality_checked_at) : formatQualityChecked(node.quality_checked)}
                                    </span>
                                    {isLoadingDetail && <span className="loading loading-spinner loading-xs text-primary"></span>}
                                  </div>
                                  <div className="text-sm text-base-content/75">
                                    {detail?.quality_summary || node.quality_summary || '暂无质量检测摘要'}
                                  </div>
                                  <div className="flex flex-wrap gap-2 text-xs text-base-content/60">
                                    {(detail?.exit_ip || node.exit_ip) && <span className="badge badge-ghost badge-sm">出口 IP {detail?.exit_ip || node.exit_ip}</span>}
                                    {(detail?.exit_country || node.exit_country) && (
                                      <span className="badge badge-ghost badge-sm">
                                        {(detail?.exit_country_code || node.exit_country_code || '').toUpperCase()} {detail?.exit_country || node.exit_country}
                                      </span>
                                    )}
                                    {(detail?.exit_region || node.exit_region) && <span className="badge badge-ghost badge-sm">区域 {detail?.exit_region || node.exit_region}</span>}
                                  </div>
                                </div>
                                {node.tag && (
                                  <button
                                    className="btn btn-sm btn-secondary"
                                    onClick={() => void handleQualityCheck(node)}
                                    disabled={isCheckingQuality}
                                  >
                                    {isCheckingQuality ? <span className="loading loading-spinner loading-xs"></span> : '重新检测'}
                                  </button>
                                )}
                              </div>
                              <div className="mt-4">
                                {detail?.items && detail.items.length > 0 ? (
                                  <div className="grid gap-2">
                                    {detail.items.map((item, index) => (
                                      <div key={`${item.target}-${index}`} className="flex flex-col lg:flex-row lg:items-center gap-2 rounded-xl border border-base-300/40 bg-base-200/20 px-3 py-2 text-sm">
                                        <div className="font-medium text-base-content min-w-[120px]">{item.target}</div>
                                        <div className={`badge badge-sm ${qualityStatusTone(item.status)}`}>{item.status}</div>
                                        {typeof item.http_status === 'number' && <div className="text-base-content/55">HTTP {item.http_status}</div>}
                                        {typeof item.latency_ms === 'number' && <div className="text-base-content/55">{item.latency_ms} ms</div>}
                                        {item.message && <div className="text-base-content/60 break-all">{item.message}</div>}
                                      </div>
                                    ))}
                                  </div>
                                ) : (
                                  <div className="text-sm text-base-content/45">
                                    {node.tag ? '暂无详细检查项，展开时会按需加载。' : '当前仅能展示已持久化的质量摘要。'}
                                  </div>
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

      {/* Summary */}
      {filteredNodes.length !== mergedNodes.length && (
        <div className="text-center text-xs text-base-content/30">
          筛选显示 {filteredNodes.length} / {mergedNodes.length} 个节点
        </div>
      )}

      {/* Create / Edit Modal */}
      {modalOpen && (
        <div className="modal modal-open">
          <div className="modal-box">
            <h3 className="font-bold text-xl mb-4">
              {editingNode ? `编辑节点: ${editingNode}` : '添加节点'}
            </h3>
            <form onSubmit={handleSubmit}>
              {formError && (
                <div className="alert alert-error mb-3 py-2 text-sm"><span>{formError}</span></div>
              )}
              <fieldset className="fieldset mb-3">
                <legend className="fieldset-legend">名称 *</legend>
                <input
                  type="text" className="input input-sm w-full" placeholder="节点名称"
                  value={form.name}
                  onChange={(e) => setForm(f => ({ ...f, name: e.target.value }))}
                  disabled={!!editingNode}
                />
              </fieldset>
              <fieldset className="fieldset mb-3">
                <legend className="fieldset-legend">URI *</legend>
                <input
                  type="text" className="input input-sm w-full font-mono text-xs"
                  placeholder="trojan://password@host:port?..."
                  value={form.uri}
                  onChange={(e) => setForm(f => ({ ...f, uri: e.target.value }))}
                />
              </fieldset>
              <fieldset className="fieldset mb-3">
                <legend className="fieldset-legend">本地代理端口</legend>
                <input
                  type="number" className="input input-sm w-full" placeholder="0 = 自动分配"
                  value={form.port || ''}
                  onChange={(e) => setForm(f => ({ ...f, port: parseInt(e.target.value) || 0 }))}
                  min={0} max={65535}
                />
              </fieldset>
              <div className="grid grid-cols-2 gap-3 mb-4">
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">用户名</legend>
                  <input
                    type="text" className="input input-sm w-full" placeholder="可选"
                    value={form.username}
                    onChange={(e) => setForm(f => ({ ...f, username: e.target.value }))}
                  />
                </fieldset>
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">密码</legend>
                  <input
                    type="text" className="input input-sm w-full" placeholder="可选"
                    value={form.password}
                    onChange={(e) => setForm(f => ({ ...f, password: e.target.value }))}
                  />
                </fieldset>
              </div>
              <div className="modal-action">
                <button type="button" className="btn btn-ghost" onClick={() => setModalOpen(false)}>取消</button>
                <button type="submit" className="btn btn-primary" disabled={submitting}>
                  {submitting ? <span className="loading loading-spinner loading-xs"></span> : (editingNode ? '更新' : '添加')}
                </button>
              </div>
            </form>
          </div>
          <form method="dialog" className="modal-backdrop" onClick={() => setModalOpen(false)}>
            <button>close</button>
          </form>
        </div>
      )}

      {/* Import Modal */}
      {importModalOpen && (
        <div className="modal modal-open">
          <div className="modal-box max-w-2xl">
            <h3 className="font-bold text-xl mb-4">导入节点</h3>
            {importError && (
              <div className="alert alert-error mb-3 py-2 text-sm"><span>{importError}</span></div>
            )}
            {importResult && (
              <div className={`alert mb-3 py-2 text-sm ${importResult.imported > 0 ? 'alert-success' : 'alert-warning'}`}>
                <div>
                  <span>{importResult.message}</span>
                  {importResult.errors && importResult.errors.length > 0 && (
                    <details className="mt-2">
                      <summary className="cursor-pointer text-xs opacity-70">{importResult.errors.length} 个错误</summary>
                      <ul className="text-xs mt-1 space-y-0.5">
                        {importResult.errors.map((err, i) => <li key={i} className="opacity-70">• {err}</li>)}
                      </ul>
                    </details>
                  )}
                </div>
              </div>
            )}
            <p className="text-sm text-base-content/60 mb-3">
              每行一个代理 URI（支持 trojan://、vless://、vmess://、ss://、hysteria2:// 等），
              可以直接粘贴导出文件的内容或从文件导入。
            </p>
            <div className="mb-3">
              <label className="btn btn-soft btn-sm">
                <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M7 16a4 4 0 01-.88-7.903A5 5 0 1115.9 6L16 6a5 5 0 011 9.9M15 13l-3-3m0 0l-3 3m3-3v12" /></svg>
                选择文件
                <input type="file" accept=".txt,.conf,.list" className="hidden" onChange={handleFileImport} />
              </label>
            </div>
            <textarea
              className="textarea textarea-bordered w-full font-mono text-xs h-48"
              placeholder={"trojan://password@host:port?sni=example.com#节点名称\nvless://uuid@host:port?encryption=none#另一个节点\n..."}
              value={importContent}
              onChange={(e) => setImportContent(e.target.value)}
            />
            <div className="text-xs text-base-content/40 mt-1">
              {importContent.trim() ? `${importContent.trim().split('\n').filter(l => l.trim() && !l.trim().startsWith('#')).length} 行有效内容` : '等待输入...'}
            </div>
            <div className="modal-action">
              <button type="button" className="btn btn-ghost" onClick={() => setImportModalOpen(false)}>
                {importResult?.imported ? '完成' : '取消'}
              </button>
              <button type="button" className="btn btn-primary" onClick={handleImport} disabled={importing || !importContent.trim()}>
                {importing ? <span className="loading loading-spinner loading-xs"></span> : '导入'}
              </button>
            </div>
          </div>
          <form method="dialog" className="modal-backdrop" onClick={() => !importing && setImportModalOpen(false)}>
            <button>close</button>
          </form>
        </div>
      )}

      {/* Delete Confirmation Modal */}
      {deleteTarget && (
        <div className="modal modal-open">
          <div className="modal-box max-w-sm">
            <h3 className="font-bold text-lg mb-2">确认删除</h3>
            <p className="text-base-content/70">
              确定要删除节点 <strong>{deleteTarget}</strong> 吗？此操作不可撤销。
            </p>
            <div className="modal-action">
              <button className="btn btn-ghost" onClick={() => setDeleteTarget(null)} disabled={deleting}>取消</button>
              <button className="btn btn-error" onClick={handleDelete} disabled={deleting}>
                {deleting ? <span className="loading loading-spinner loading-xs"></span> : '删除'}
              </button>
            </div>
          </div>
          <form method="dialog" className="modal-backdrop" onClick={() => !deleting && setDeleteTarget(null)}>
            <button>close</button>
          </form>
        </div>
      )}

      {/* Batch Delete Confirmation Modal */}
      {batchDeleteConfirm && (
        <div className="modal modal-open">
          <div className="modal-box max-w-sm">
            <h3 className="font-bold text-lg mb-2">确认批量删除</h3>
            <p className="text-base-content/70">
              确定要删除选中的 <strong>{selectedNodes.size}</strong> 个节点吗？此操作不可撤销。
            </p>
            <div className="modal-action">
              <button className="btn btn-ghost" onClick={() => setBatchDeleteConfirm(false)} disabled={batchProcessing || probeJobRunning || qualityBatchRunning}>取消</button>
              <button className="btn btn-error" onClick={handleBatchDelete} disabled={batchProcessing || probeJobRunning || qualityBatchRunning}>
                {batchProcessing || probeJobRunning || qualityBatchRunning ? <span className="loading loading-spinner loading-xs"></span> : `删除 ${selectedNodes.size} 个节点`}
              </button>
            </div>
          </div>
          <form method="dialog" className="modal-backdrop" onClick={() => !(batchProcessing || probeJobRunning || qualityBatchRunning) && setBatchDeleteConfirm(false)}>
            <button>close</button>
          </form>
        </div>
      )}
    </div>
    </div>
  )
}
