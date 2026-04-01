import type {
  ConfigNodeConfig,
  ManageListResponse,
  NodeQualityCheckResult,
  QualityCheckBatchComplete,
  QualityCheckBatchEvent,
  QualityCheckBatchProgress,
  QualityCheckBatchStart,
} from '../types/index.ts'

export interface BatchQualityLastResult {
  tag: string
  name: string
  status: 'success' | 'error'
  error: string
  quality_status?: string
  quality_score?: number
  quality_grade?: string
}

export interface BatchQualityState {
  status: 'running' | 'completed'
  total: number
  current: number
  success: number
  failed: number
  lastResult: BatchQualityLastResult | null
}

export function buildQualityCacheEntry(node: ConfigNodeConfig): NodeQualityCheckResult | null {
  if (!node.quality_status && !node.quality_grade && !node.quality_summary && !node.quality_checked) {
    return null
  }

  return {
    node_id: 0,
    quality_status: node.quality_status || 'unknown',
    quality_score: node.quality_score,
    quality_grade: node.quality_grade || '-',
    quality_summary: node.quality_summary || '',
    quality_checked_at: node.quality_checked ? new Date(node.quality_checked * 1000).toISOString() : undefined,
    exit_ip: node.exit_ip,
    exit_country: node.exit_country,
    exit_country_code: node.exit_country_code,
    exit_region: node.exit_region,
    items: [],
  }
}

export function applyQualityResultToConfigNode(node: ConfigNodeConfig, result: NodeQualityCheckResult): ConfigNodeConfig {
  return {
    ...node,
    quality_status: result.quality_status,
    quality_score: result.quality_score,
    quality_grade: result.quality_grade,
    quality_summary: result.quality_summary,
    quality_checked: result.quality_checked_at ? Math.floor(Date.parse(result.quality_checked_at) / 1000) : undefined,
    exit_ip: result.exit_ip,
    exit_country: result.exit_country,
    exit_country_code: result.exit_country_code,
    exit_region: result.exit_region,
  }
}

export function applyQualityResultToManageList(
  page: ManageListResponse | null,
  nodeName: string,
  result: NodeQualityCheckResult,
): ManageListResponse | null {
  if (!page) {
    return page
  }

  return {
    ...page,
    items: page.items.map(item => (
      item.name === nodeName
        ? { ...item, ...applyQualityResultToConfigNode(item, result) }
        : item
    )),
  }
}

export function buildQualityResultFromBatchProgress(event: QualityCheckBatchProgress): NodeQualityCheckResult | null {
  if (event.status !== 'success') {
    return null
  }

  return {
    node_id: 0,
    quality_status: event.quality_status || 'unknown',
    quality_score: event.quality_score,
    quality_grade: event.quality_grade || '-',
    quality_summary: event.quality_summary || '',
    quality_checked_at: event.quality_checked_at,
    exit_ip: event.exit_ip,
    exit_country: event.exit_country,
    exit_country_code: event.exit_country_code,
    exit_region: event.exit_region,
    items: event.items || [],
  }
}

function reduceStart(event: QualityCheckBatchStart): BatchQualityState {
  return {
    status: 'running',
    total: event.total,
    current: 0,
    success: 0,
    failed: 0,
    lastResult: null,
  }
}

function reduceProgress(state: BatchQualityState | null, event: QualityCheckBatchProgress): BatchQualityState {
  return {
    status: 'running',
    total: event.total,
    current: event.current,
    success: event.status === 'success' ? (state?.success || 0) + 1 : state?.success || 0,
    failed: event.status === 'error' ? (state?.failed || 0) + 1 : state?.failed || 0,
    lastResult: {
      tag: event.tag,
      name: event.name,
      status: event.status,
      error: event.error,
      quality_status: event.quality_status,
      quality_score: event.quality_score,
      quality_grade: event.quality_grade,
    },
  }
}

function reduceComplete(state: BatchQualityState | null, event: QualityCheckBatchComplete): BatchQualityState {
  return {
    status: 'completed',
    total: event.total,
    current: event.total,
    success: event.success,
    failed: event.failed,
    lastResult: state?.lastResult || null,
  }
}

export function reduceBatchQualityEvent(
  state: BatchQualityState | null,
  event: QualityCheckBatchEvent,
): BatchQualityState {
  switch (event.type) {
    case 'start':
      return reduceStart(event)
    case 'progress':
      return reduceProgress(state, event)
    case 'complete':
      return reduceComplete(state, event)
    default:
      return state as BatchQualityState
  }
}
