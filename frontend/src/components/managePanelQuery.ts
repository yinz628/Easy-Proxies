import type { ManageFilterSnapshot, ManageListResponse, ManageQuery } from '../types/index.ts'

export const defaultManageQuery: ManageQuery = {
  page: 1,
  page_size: 100,
  keyword: '',
  status: '',
  region: '',
  source: '',
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
    quality_status: normalized.quality_status,
  }
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
