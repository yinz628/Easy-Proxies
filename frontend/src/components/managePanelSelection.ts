import type { ManageQuery, ManageSelectionRequest, SelectionState } from '../types/index.ts'
import { buildManageFilterSnapshot } from './managePanelQuery.ts'

export function buildSelectionRequest(state: SelectionState, _query: ManageQuery): ManageSelectionRequest {
  if (state.mode === 'names') {
    return {
      selection: {
        mode: 'names',
        names: Array.from(state.names),
      },
    }
  }

  return {
    selection: {
      mode: 'filter',
      filter: state.filter,
      exclude_names: Array.from(state.excludeNames),
    },
  }
}

export function toggleNodeSelection(state: SelectionState, name: string): SelectionState {
  if (!name) {
    return state
  }

  if (state.mode === 'names') {
    const next = new Set(state.names)
    if (next.has(name)) {
      next.delete(name)
    } else {
      next.add(name)
    }
    return { mode: 'names', names: next }
  }

  const next = new Set(state.excludeNames)
  if (next.has(name)) {
    next.delete(name)
  } else {
    next.add(name)
  }
  return { ...state, excludeNames: next }
}

export function togglePageSelection(state: SelectionState, pageNames: string[]): SelectionState {
  const uniquePageNames = Array.from(new Set(pageNames.filter(Boolean)))
  if (uniquePageNames.length === 0) {
    return state
  }

  if (state.mode === 'names') {
    const next = new Set(state.names)
    const allSelected = uniquePageNames.every(name => next.has(name))
    for (const name of uniquePageNames) {
      if (allSelected) {
        next.delete(name)
      } else {
        next.add(name)
      }
    }
    return { mode: 'names', names: next }
  }

  const next = new Set(state.excludeNames)
  const allSelected = uniquePageNames.every(name => !next.has(name))
  for (const name of uniquePageNames) {
    if (allSelected) {
      next.add(name)
    } else {
      next.delete(name)
    }
  }
  return { ...state, excludeNames: next }
}

export function isNodeSelected(state: SelectionState, name: string, query: ManageQuery): boolean {
  if (state.mode === 'names') {
    return state.names.has(name)
  }

  const currentFilter = buildManageFilterSnapshot(query)
  if (
    state.filter.keyword !== currentFilter.keyword ||
    state.filter.status !== currentFilter.status ||
    state.filter.region !== currentFilter.region ||
    state.filter.source !== currentFilter.source
  ) {
    return false
  }
  return !state.excludeNames.has(name)
}

export function getSelectionCount(state: SelectionState, filteredTotal: number, query: ManageQuery): number {
  if (state.mode === 'names') {
    return state.names.size
  }

  const currentFilter = buildManageFilterSnapshot(query)
  if (
    state.filter.keyword !== currentFilter.keyword ||
    state.filter.status !== currentFilter.status ||
    state.filter.region !== currentFilter.region ||
    state.filter.source !== currentFilter.source
  ) {
    return 0
  }

  return Math.max(filteredTotal - state.excludeNames.size, 0)
}

export function getPageSelectionState(
  state: SelectionState,
  pageNames: string[],
  query: ManageQuery,
): { selectedCount: number; allSelected: boolean; partiallySelected: boolean } {
  const uniquePageNames = Array.from(new Set(pageNames.filter(Boolean)))
  const selectedCount = uniquePageNames.filter(name => isNodeSelected(state, name, query)).length
  return {
    selectedCount,
    allSelected: uniquePageNames.length > 0 && selectedCount === uniquePageNames.length,
    partiallySelected: selectedCount > 0 && selectedCount < uniquePageNames.length,
  }
}
