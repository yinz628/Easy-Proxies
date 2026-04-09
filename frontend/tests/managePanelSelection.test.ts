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
  lifecycle_state: '',
  manual_probe_status: '',
  activation_ready: '',
  quality_status: 'healthy',
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
      lifecycle_state: '',
      manual_probe_status: '',
      activation_ready: '',
      quality_status: 'healthy',
    },
    excludeUris: new Set(['socks5://node-b']),
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
        lifecycle_state: '',
      manual_probe_status: '',
      activation_ready: '',
      quality_status: 'healthy',
      },
      exclude_uris: ['socks5://node-b'],
    },
  })
})

test('togglePageSelection only affects the current page uris', () => {
  const state: SelectionState = {
    mode: 'uris',
    uris: new Set(['socks5://node-a', 'socks5://node-c']),
  }

  const next = togglePageSelection(state, ['socks5://node-b', 'socks5://node-c'])

  assert.equal(next.mode, 'uris')
  if (next.mode !== 'uris') {
    throw new Error('expected uris mode')
  }
  assert.deepEqual(Array.from(next.uris).sort(), ['socks5://node-a', 'socks5://node-b', 'socks5://node-c'])
})

test('togglePageSelection in uris mode keeps selections from other pages intact', () => {
  const state: SelectionState = {
    mode: 'uris',
    uris: new Set(['socks5://page2-node', 'socks5://page3-node']),
  }

  const next = togglePageSelection(state, ['socks5://page1-node-a', 'socks5://page1-node-b'])

  assert.equal(next.mode, 'uris')
  if (next.mode !== 'uris') {
    throw new Error('expected uris mode')
  }
  assert.deepEqual(Array.from(next.uris).sort(), [
    'socks5://page1-node-a',
    'socks5://page1-node-b',
    'socks5://page2-node',
    'socks5://page3-node',
  ])
})

test('isNodeSelected respects filter mode exclusions', () => {
  const state: SelectionState = {
    mode: 'filter',
    filter: {
      keyword: 'hk',
      status: 'normal',
      region: 'hk',
      source: 'manual',
      lifecycle_state: '',
      manual_probe_status: '',
      activation_ready: '',
      quality_status: 'healthy',
    },
    excludeUris: new Set(['socks5://node-b']),
  }

  assert.equal(isNodeSelected(state, 'socks5://node-a', baseQuery), true)
  assert.equal(isNodeSelected(state, 'socks5://node-b', baseQuery), false)
  assert.equal(isNodeSelected(state, 'socks5://node-a', { ...baseQuery, region: 'us' }), false)
})

test('toggleNodeSelection only flips the requested node in uris mode', () => {
  const state: SelectionState = {
    mode: 'uris',
    uris: new Set(['socks5://node-a']),
  }

  const next = toggleNodeSelection(state, 'socks5://node-b')

  assert.equal(next.mode, 'uris')
  if (next.mode !== 'uris') {
    throw new Error('expected uris mode')
  }
  assert.deepEqual(Array.from(next.uris).sort(), ['socks5://node-a', 'socks5://node-b'])
})

test('getPageSelectionState reports all selected for current filter page', () => {
  const state: SelectionState = {
    mode: 'filter',
    filter: {
      keyword: 'hk',
      status: 'normal',
      region: 'hk',
      source: 'manual',
      lifecycle_state: '',
      manual_probe_status: '',
      activation_ready: '',
      quality_status: 'healthy',
    },
    excludeUris: new Set(['socks5://node-z']),
  }

  const pageState = getPageSelectionState(state, ['socks5://node-a', 'socks5://node-b'], baseQuery)

  assert.deepEqual(pageState, {
    selectedCount: 2,
    allSelected: true,
    partiallySelected: false,
  })
})

test('togglePageSelection in filter mode only excludes the current page uris', () => {
  const state: SelectionState = {
    mode: 'filter',
    filter: {
      keyword: 'hk',
      status: 'normal',
      region: 'hk',
      source: 'manual',
      lifecycle_state: '',
      manual_probe_status: '',
      activation_ready: '',
      quality_status: 'healthy',
    },
    excludeUris: new Set(['socks5://page2-node']),
  }

  const next = togglePageSelection(state, ['socks5://page1-node-a', 'socks5://page1-node-b'])

  assert.equal(next.mode, 'filter')
  if (next.mode !== 'filter') {
    throw new Error('expected filter mode')
  }
  assert.deepEqual(Array.from(next.excludeUris).sort(), [
    'socks5://page1-node-a',
    'socks5://page1-node-b',
    'socks5://page2-node',
  ])
})

test('togglePageSelection in filter mode reselects current page without clearing other pages', () => {
  const state: SelectionState = {
    mode: 'filter',
    filter: {
      keyword: 'hk',
      status: 'normal',
      region: 'hk',
      source: 'manual',
      lifecycle_state: '',
      manual_probe_status: '',
      activation_ready: '',
      quality_status: 'healthy',
    },
    excludeUris: new Set(['socks5://page1-node-a', 'socks5://page2-node']),
  }

  const next = togglePageSelection(state, ['socks5://page1-node-a', 'socks5://page1-node-b'])

  assert.equal(next.mode, 'filter')
  if (next.mode !== 'filter') {
    throw new Error('expected filter mode')
  }
  assert.deepEqual(Array.from(next.excludeUris).sort(), ['socks5://page2-node'])
})

test('filter selection remains valid when only page and page size change', () => {
  const state: SelectionState = {
    mode: 'filter',
    filter: {
      keyword: 'hk',
      status: 'normal',
      region: 'hk',
      source: 'manual',
      lifecycle_state: '',
      manual_probe_status: '',
      activation_ready: '',
      quality_status: 'healthy',
    },
    excludeUris: new Set(['socks5://node-a']),
  }

  const changedPageQuery: ManageQuery = {
    ...baseQuery,
    page: 1,
    page_size: 50,
    sort_key: 'name',
    sort_dir: 'asc',
  }

  assert.equal(isNodeSelected(state, 'socks5://node-b', changedPageQuery), true)
  assert.equal(getSelectionCount(state, 18, changedPageQuery), 17)
  assert.deepEqual(getPageSelectionState(state, ['socks5://node-a', 'socks5://node-b'], changedPageQuery), {
    selectedCount: 1,
    allSelected: false,
    partiallySelected: true,
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
      lifecycle_state: '',
      manual_probe_status: '',
      activation_ready: '',
      quality_status: 'healthy',
    },
    excludeUris: new Set(['socks5://node-a']),
  }

  assert.equal(getSelectionCount(state, 18, { ...baseQuery, quality_status: 'warn' }), 0)
  assert.equal(getSelectionCount(state, 18, baseQuery), 17)
  assert.equal(getSelectionCount(state, 18, { ...baseQuery, region: 'us' }), 0)
})
