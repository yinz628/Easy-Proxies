import assert from 'node:assert/strict'
import test from 'node:test'

import type { ManageListResponse } from '../src/types/index.ts'
import {
  buildManageFilterSnapshot,
  buildManageQueryKey,
  normalizeManageQuery,
  resolveManageResponsePage,
} from '../src/components/managePanelQuery.ts'

test('buildManageQueryKey normalizes default query values', () => {
  const keyA = buildManageQueryKey(normalizeManageQuery())
  const keyB = buildManageQueryKey({
    page: 1,
    page_size: 100,
    keyword: '   ',
    status: '',
    region: '',
    source: '',
    quality_status: '',
    sort_key: 'name',
    sort_dir: 'asc',
  })

  assert.equal(keyA, keyB)
})

test('buildManageFilterSnapshot strips page and sort fields from query state', () => {
  const snapshot = buildManageFilterSnapshot({
    page: 9,
    page_size: 20,
    keyword: ' hk ',
    status: 'normal',
    region: 'hk',
    source: 'manual',
    quality_status: 'healthy',
    sort_key: 'latency',
    sort_dir: 'desc',
  })

  assert.deepEqual(snapshot, {
    keyword: 'hk',
    status: 'normal',
    region: 'hk',
    source: 'manual',
    quality_status: 'healthy',
  })
})

test('resolveManageResponsePage clamps an empty out-of-range result back to page one', () => {
  const page = resolveManageResponsePage(
    normalizeManageQuery({
      page: 3,
      page_size: 100,
    }),
    {
      items: [],
      page: 3,
      page_size: 100,
      total: 0,
      filtered_total: 0,
      summary: {
        normal: 0,
        pending: 0,
        unavailable: 0,
        blacklisted: 0,
        disabled: 0,
      },
      facets: {
        regions: [],
        sources: [],
        quality_statuses: [],
      },
    } satisfies ManageListResponse,
  )

  assert.equal(page, 1)
})
