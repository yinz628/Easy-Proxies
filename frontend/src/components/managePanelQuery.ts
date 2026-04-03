import type { ManageFilterSnapshot, ManageListResponse, ManageQuery } from '../types/index.ts'

export const defaultManageQuery: ManageQuery = {
  page: 1,
  page_size: 100,
  keyword: '',
  status: '',
  region: '',
  source: '',
  lifecycle_state: '',
  manual_probe_status: '',
  activation_ready: '',
  quality_status: '',
  sort_key: 'name',
  sort_dir: 'asc',
}

export function normalizeManageQuery(input: Partial<ManageQuery> = {}): ManageQuery {
  const page = typeof input.page === 'number' && input.page > 0 ? Math.floor(input.page) : defaultManageQuery.page
  const pageSize = typeof input.page_size === 'number' && input.page_size > 0 ? Math.floor(input.page_size) : defaultManageQuery.page_size
  return {
    page,
    page_size: pageSize,
    keyword: input.keyword?.trim() ?? '',
    status: input.status ?? '',
    region: input.region?.trim() ?? '',
    source: input.source?.trim() ?? '',
    lifecycle_state: input.lifecycle_state ?? '',
    manual_probe_status: input.manual_probe_status ?? '',
    activation_ready: input.activation_ready ?? '',
    quality_status: input.quality_status?.trim() ?? '',
    sort_key: input.sort_key ?? defaultManageQuery.sort_key,
    sort_dir: input.sort_dir === 'desc' ? 'desc' : 'asc',
  }
}

export function buildManageFilterSnapshot(query: ManageQuery): ManageFilterSnapshot {
  const normalized = normalizeManageQuery(query)
  return {
    keyword: normalized.keyword,
    status: normalized.status,
    region: normalized.region,
    source: normalized.source,
    lifecycle_state: normalized.lifecycle_state,
    manual_probe_status: normalized.manual_probe_status,
    activation_ready: normalized.activation_ready,
    quality_status: normalized.quality_status,
  }
}

export function resolveVisibleManageQuery(query: ManageQuery, keywordInput: string): ManageQuery {
  return normalizeManageQuery({
    ...query,
    keyword: keywordInput,
  })
}

export function hasPendingManageKeywordChange(query: ManageQuery, keywordInput: string): boolean {
  return normalizeManageQuery(query).keyword !== resolveVisibleManageQuery(query, keywordInput).keyword
}

export function hasActiveManageFilters(query: ManageQuery, keywordInput: string): boolean {
  const visible = resolveVisibleManageQuery(query, keywordInput)
  return Boolean(
    visible.keyword
    || visible.status
    || visible.region
    || visible.source
    || visible.lifecycle_state
    || visible.manual_probe_status
    || visible.activation_ready
    || visible.quality_status
  )
}

export function canSelectFilteredResults(query: ManageQuery, keywordInput: string, filteredTotal: number): boolean {
  if (filteredTotal <= 0) {
    return false
  }
  if (!hasActiveManageFilters(query, keywordInput)) {
    return false
  }
  return !hasPendingManageKeywordChange(query, keywordInput)
}

export function buildManageQueryKey(query: ManageQuery): string {
  const normalized = normalizeManageQuery(query)
  return JSON.stringify(normalized)
}

export function resolveManageResponsePage(query: ManageQuery, response: ManageListResponse): number {
  const normalized = normalizeManageQuery(query)
  if (response.filtered_total <= 0) {
    return defaultManageQuery.page
  }

  const totalPages = Math.max(1, Math.ceil(response.filtered_total / Math.max(response.page_size, 1)))
  return Math.min(normalized.page, totalPages)
}
