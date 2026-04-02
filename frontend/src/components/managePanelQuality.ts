import type {
  BatchQualityJob,
  BatchQualityJobResult,
  BatchQualityJobStatus,
  ConfigNodeConfig,
  ManageListResponse,
  NodeQualityCheckResult,
  QualityCheckBatchComplete,
  QualityCheckBatchEvent,
  QualityCheckBatchProgress,
  QualityCheckBatchStart,
} from '../types/index.ts'

const aiQualityVersion = 'ai_reachability_v2'

export interface BatchQualityLastResult {
  tag: string
  name: string
  status: 'success' | 'error'
  error: string
  quality_status?: string
  quality_openai_status?: string
  quality_anthropic_status?: string
  quality_score?: number
  quality_grade?: string
}

export interface BatchQualityState {
  jobId: string
  status: BatchQualityJobStatus
  total: number
  current: number
  success: number
  failed: number
  lastResult: BatchQualityLastResult | null
}

function isAIVersion(value?: string): boolean {
  return value === aiQualityVersion
}

export function buildQualityCacheEntry(node: ConfigNodeConfig): NodeQualityCheckResult | null {
  if (!isAIVersion(node.quality_version) || !node.quality_status) {
    return null
  }

  return {
    node_id: 0,
    quality_version: node.quality_version,
    quality_status: node.quality_status,
    quality_openai_status: node.quality_openai_status,
    quality_anthropic_status: node.quality_anthropic_status,
    quality_score: node.quality_score,
    quality_grade: node.quality_grade || '-',
    quality_summary: node.quality_summary || '',
    quality_checked_at: node.quality_checked ? new Date(node.quality_checked * 1000).toISOString() : undefined,
    items: [],
  }
}

export function applyQualityResultToConfigNode(node: ConfigNodeConfig, result: NodeQualityCheckResult): ConfigNodeConfig {
  return {
    ...node,
    quality_version: result.quality_version,
    quality_status: result.quality_status,
    quality_openai_status: result.quality_openai_status,
    quality_anthropic_status: result.quality_anthropic_status,
    quality_score: result.quality_score,
    quality_grade: result.quality_grade,
    quality_summary: result.quality_summary,
    quality_checked: result.quality_checked_at ? Math.floor(Date.parse(result.quality_checked_at) / 1000) : undefined,
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
  if (event.status !== 'success' || !isAIVersion(event.quality_version) || !event.quality_status) {
    return null
  }

  return {
    node_id: 0,
    quality_version: event.quality_version,
    quality_status: event.quality_status,
    quality_openai_status: event.quality_openai_status,
    quality_anthropic_status: event.quality_anthropic_status,
    quality_score: event.quality_score,
    quality_grade: event.quality_grade || '-',
    quality_summary: event.quality_summary || '',
    quality_checked_at: event.quality_checked_at,
    items: event.items || [],
  }
}

export function buildQualityResultFromJobResult(result?: BatchQualityJobResult): NodeQualityCheckResult | null {
  if (!result || result.error || !isAIVersion(result.quality_version) || !result.quality_status) {
    return null
  }

  return {
    node_id: 0,
    quality_version: result.quality_version,
    quality_status: result.quality_status,
    quality_openai_status: result.quality_openai_status,
    quality_anthropic_status: result.quality_anthropic_status,
    quality_score: result.quality_score,
    quality_grade: result.quality_grade || '-',
    quality_summary: result.quality_summary || '',
    quality_checked_at: result.quality_checked_at,
    items: result.items || [],
  }
}

function toLastResult(result?: BatchQualityJobResult): BatchQualityLastResult | null {
  if (!result) {
    return null
  }

  return {
    tag: result.tag,
    name: result.name,
    status: result.error ? 'error' : 'success',
    error: result.error || '',
    quality_status: result.quality_status,
    quality_openai_status: result.quality_openai_status,
    quality_anthropic_status: result.quality_anthropic_status,
    quality_score: result.quality_score,
    quality_grade: result.quality_grade,
  }
}

function reduceStart(event: QualityCheckBatchStart): BatchQualityState {
  return {
    jobId: event.job_id,
    status: event.status,
    total: event.total,
    current: event.completed,
    success: event.success,
    failed: event.failed,
    lastResult: null,
  }
}

function reduceProgress(event: QualityCheckBatchProgress): BatchQualityState {
  return {
    jobId: event.job_id,
    status: 'running',
    total: event.total,
    current: event.current,
    success: event.success,
    failed: event.failed,
    lastResult: {
      tag: event.tag,
      name: event.name,
      status: event.status,
      error: event.error,
      quality_status: event.quality_status,
      quality_openai_status: event.quality_openai_status,
      quality_anthropic_status: event.quality_anthropic_status,
      quality_score: event.quality_score,
      quality_grade: event.quality_grade,
    },
  }
}

function reduceComplete(state: BatchQualityState | null, event: QualityCheckBatchComplete): BatchQualityState {
  return {
    jobId: event.job_id,
    status: event.status,
    total: event.total,
    current: event.completed,
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
      return reduceProgress(event)
    case 'complete':
      return reduceComplete(state, event)
    default:
      return state as BatchQualityState
  }
}

export function mergeQualityJobSnapshot(
  state: BatchQualityState | null,
  job: BatchQualityJob | null,
): BatchQualityState | null {
  if (!job) {
    return null
  }

  return {
    jobId: job.id,
    status: job.status,
    total: job.total,
    current: job.completed,
    success: job.success,
    failed: job.failed,
    lastResult: toLastResult(job.last_result) || state?.lastResult || null,
  }
}
