import type { ManageQuery, ManageSelectionRequest, SelectionState } from '../types/index.ts'
import { buildManageFilterSnapshot } from './managePanelQuery.ts'

export function buildSelectionRequest(state: SelectionState): ManageSelectionRequest {
  if (state.mode === 'uris') {
    return {
      selection: {
        mode: 'uris',
        uris: Array.from(state.uris),
      },
    }
  }

  return {
    selection: {
      mode: 'filter',
      filter: state.filter,
      exclude_uris: Array.from(state.excludeUris),
    },
  }
}

export function toggleNodeSelection(state: SelectionState, uri: string): SelectionState {
  if (!uri) {
    return state
  }

  if (state.mode === 'uris') {
    const next = new Set(state.uris)
    if (next.has(uri)) {
      next.delete(uri)
    } else {
      next.add(uri)
    }
    return { mode: 'uris', uris: next }
  }

  const next = new Set(state.excludeUris)
  if (next.has(uri)) {
    next.delete(uri)
  } else {
    next.add(uri)
  }
  return { ...state, excludeUris: next }
}

export function togglePageSelection(state: SelectionState, pageUris: string[]): SelectionState {
  const uniquePageUris = Array.from(new Set(pageUris.filter(Boolean)))
  if (uniquePageUris.length === 0) {
    return state
  }

  if (state.mode === 'uris') {
    const next = new Set(state.uris)
    const allSelected = uniquePageUris.every(uri => next.has(uri))
    for (const uri of uniquePageUris) {
      if (allSelected) {
        next.delete(uri)
      } else {
        next.add(uri)
      }
    }
    return { mode: 'uris', uris: next }
  }

  const next = new Set(state.excludeUris)
  const allSelected = uniquePageUris.every(uri => !next.has(uri))
  for (const uri of uniquePageUris) {
    if (allSelected) {
      next.add(uri)
    } else {
      next.delete(uri)
    }
  }
  return { ...state, excludeUris: next }
}

export function isNodeSelected(state: SelectionState, uri: string, query: ManageQuery): boolean {
  if (state.mode === 'uris') {
    return state.uris.has(uri)
  }

  const currentFilter = buildManageFilterSnapshot(query)
  if (
    state.filter.keyword !== currentFilter.keyword ||
    state.filter.status !== currentFilter.status ||
    state.filter.region !== currentFilter.region ||
    state.filter.source !== currentFilter.source ||
    state.filter.lifecycle_state !== currentFilter.lifecycle_state ||
    state.filter.manual_probe_status !== currentFilter.manual_probe_status ||
    state.filter.activation_ready !== currentFilter.activation_ready ||
    state.filter.quality_status !== currentFilter.quality_status
  ) {
    return false
  }
  return !state.excludeUris.has(uri)
}

export function getSelectionCount(state: SelectionState, filteredTotal: number, query: ManageQuery): number {
  if (state.mode === 'uris') {
    return state.uris.size
  }

  const currentFilter = buildManageFilterSnapshot(query)
  if (
    state.filter.keyword !== currentFilter.keyword ||
    state.filter.status !== currentFilter.status ||
    state.filter.region !== currentFilter.region ||
    state.filter.source !== currentFilter.source ||
    state.filter.lifecycle_state !== currentFilter.lifecycle_state ||
    state.filter.manual_probe_status !== currentFilter.manual_probe_status ||
    state.filter.activation_ready !== currentFilter.activation_ready ||
    state.filter.quality_status !== currentFilter.quality_status
  ) {
    return 0
  }

  return Math.max(filteredTotal - state.excludeUris.size, 0)
}

export function getPageSelectionState(
  state: SelectionState,
  pageUris: string[],
  query: ManageQuery,
): { selectedCount: number; allSelected: boolean; partiallySelected: boolean } {
  const uniquePageUris = Array.from(new Set(pageUris.filter(Boolean)))
  const selectedCount = uniquePageUris.filter(uri => isNodeSelected(state, uri, query)).length
  return {
    selectedCount,
    allSelected: uniquePageUris.length > 0 && selectedCount === uniquePageUris.length,
    partiallySelected: selectedCount > 0 && selectedCount < uniquePageUris.length,
  }
}
