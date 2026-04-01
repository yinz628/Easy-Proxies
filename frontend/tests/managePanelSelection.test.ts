import assert from 'node:assert/strict'
import test from 'node:test'

import {
  buildSelectionRequest,
  getPageSelectionState,
  getSelectionCount,
  isNodeSelected,
  toggleNodeSelection,
  togglePageSelection,
} from '../src/components/managePanelSelection.ts'
import type { ManageQuery, SelectionState } from '../src/types/index.ts'

const baseQuery: ManageQuery = {
  page: 3,
  page_size: 100,
  keyword: 'hk',
  status: 'normal',
  region: 'hk',
  source: 'manual',
  sort_key: 'latency',
  sort_dir: 'desc',
}

test('buildSelectionRequest strips page and sort from filter mode', () => {
  const state: SelectionState = {
    mode: 'filter',
    filter: {
      keyword: 'hk',
      status: 'normal',
      region: 'hk',
      source: 'manual',
    },
    excludeNames: new Set(['node-b']),
  }

  const request = buildSelectionRequest(state, baseQuery)

  assert.deepEqual(request, {
    selection: {
      mode: 'filter',
      filter: {
        keyword: 'hk',
        status: 'normal',
        region: 'hk',
        source: 'manual',
      },
      exclude_names: ['node-b'],
    },
  })
})

test('togglePageSelection only affects the current page names', () => {
  const state: SelectionState = {
    mode: 'names',
    names: new Set(['node-a', 'node-c']),
  }

  const next = togglePageSelection(state, ['node-b', 'node-c'])

  assert.equal(next.mode, 'names')
  if (next.mode !== 'names') {
    throw new Error('expected names mode')
  }
  assert.deepEqual(Array.from(next.names).sort(), ['node-a', 'node-b', 'node-c'])
})

test('isNodeSelected respects filter mode exclusions', () => {
  const state: SelectionState = {
    mode: 'filter',
    filter: {
      keyword: 'hk',
      status: 'normal',
      region: 'hk',
      source: 'manual',
    },
    excludeNames: new Set(['node-b']),
  }

  assert.equal(isNodeSelected(state, 'node-a', baseQuery), true)
  assert.equal(isNodeSelected(state, 'node-b', baseQuery), false)
  assert.equal(isNodeSelected(state, 'node-a', { ...baseQuery, region: 'us' }), false)
})

test('toggleNodeSelection only flips the requested node in names mode', () => {
  const state: SelectionState = {
    mode: 'names',
    names: new Set(['node-a']),
  }

  const next = toggleNodeSelection(state, 'node-b')

  assert.equal(next.mode, 'names')
  if (next.mode !== 'names') {
    throw new Error('expected names mode')
  }
  assert.deepEqual(Array.from(next.names).sort(), ['node-a', 'node-b'])
})

test('getPageSelectionState reports all selected for current filter page', () => {
  const state: SelectionState = {
    mode: 'filter',
    filter: {
      keyword: 'hk',
      status: 'normal',
      region: 'hk',
      source: 'manual',
    },
    excludeNames: new Set(['node-z']),
  }

  const pageState = getPageSelectionState(state, ['node-a', 'node-b'], baseQuery)

  assert.deepEqual(pageState, {
    selectedCount: 2,
    allSelected: true,
    partiallySelected: false,
  })
})

test('getSelectionCount returns zero when filter selection no longer matches active query', () => {
  const state: SelectionState = {
    mode: 'filter',
    filter: {
      keyword: 'hk',
      status: 'normal',
      region: 'hk',
      source: 'manual',
    },
    excludeNames: new Set(['node-a']),
  }

  assert.equal(getSelectionCount(state, 18, baseQuery), 17)
  assert.equal(getSelectionCount(state, 18, { ...baseQuery, region: 'us' }), 0)
})
