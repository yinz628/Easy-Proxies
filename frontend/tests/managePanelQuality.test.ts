import test from 'node:test'
import assert from 'node:assert/strict'

import type { ConfigNodeConfig, ManageListResponse, NodeQualityCheckResult, QualityCheckBatchEvent } from '../src/types/index.ts'
import {
  applyQualityResultToManageList,
  applyQualityResultToConfigNode,
  buildQualityResultFromBatchProgress,
  buildQualityCacheEntry,
  reduceBatchQualityEvent,
} from '../src/components/managePanelQuality.ts'

test('buildQualityCacheEntry normalizes a persisted config summary into detail-ready shape', () => {
  const node: ConfigNodeConfig = {
    name: 'node-a',
    uri: 'trojan://example',
    port: 0,
    username: '',
    password: '',
    quality_status: 'healthy',
    quality_score: 92,
    quality_grade: 'A',
    quality_summary: 'connectivity ok',
    quality_checked: 1711929600,
    exit_ip: '1.1.1.1',
    exit_country: 'United States',
    exit_country_code: 'US',
    exit_region: 'us',
  }

  const entry = buildQualityCacheEntry(node)

  assert.equal(entry?.quality_status, 'healthy')
  assert.equal(entry?.quality_score, 92)
  assert.equal(entry?.quality_grade, 'A')
  assert.equal(entry?.quality_summary, 'connectivity ok')
  assert.equal(entry?.quality_checked_at, '2024-04-01T00:00:00.000Z')
  assert.equal(entry?.items.length, 0)
  assert.equal(entry?.exit_country_code, 'US')
})

test('applyQualityResultToConfigNode copies the latest quality fields into the row summary', () => {
  const node: ConfigNodeConfig = {
    name: 'node-a',
    uri: 'trojan://example',
    port: 0,
    username: '',
    password: '',
  }

  const result: NodeQualityCheckResult = {
    node_id: 10,
    quality_status: 'degraded',
    quality_score: 63,
    quality_grade: 'C',
    quality_summary: 'latency is unstable',
    quality_checked_at: '2026-04-01T10:20:30Z',
    exit_ip: '8.8.8.8',
    exit_country: 'Singapore',
    exit_country_code: 'SG',
    exit_region: 'sg',
    items: [
      { target: 'connectivity', status: 'success', latency_ms: 88 },
    ],
  }

  const updated = applyQualityResultToConfigNode(node, result)

  assert.equal(updated.quality_status, 'degraded')
  assert.equal(updated.quality_score, 63)
  assert.equal(updated.quality_grade, 'C')
  assert.equal(updated.quality_summary, 'latency is unstable')
  assert.equal(updated.quality_checked, Date.parse('2026-04-01T10:20:30Z') / 1000)
  assert.equal(updated.exit_ip, '8.8.8.8')
  assert.equal(updated.exit_country, 'Singapore')
  assert.equal(updated.exit_country_code, 'SG')
  assert.equal(updated.exit_region, 'sg')
})

test('applyQualityResultToManageList updates the matching page row without losing runtime fields', () => {
  const page: ManageListResponse = {
    items: [
      {
        name: 'node-a',
        uri: 'trojan://a',
        port: 1080,
        username: '',
        password: '',
        runtime_status: 'normal',
        latency_ms: 88,
        region: 'hk',
        country: 'Hong Kong',
        active_connections: 1,
        success_count: 10,
        failure_count: 2,
        tag: 'tag-a',
      },
      {
        name: 'node-b',
        uri: 'trojan://b',
        port: 1081,
        username: '',
        password: '',
        runtime_status: 'pending',
        latency_ms: -1,
        active_connections: 0,
        success_count: 0,
        failure_count: 0,
      },
    ],
    page: 1,
    page_size: 100,
    total: 2,
    filtered_total: 2,
    summary: {
      normal: 1,
      pending: 1,
      unavailable: 0,
      blacklisted: 0,
      disabled: 0,
    },
    facets: {
      regions: ['hk'],
      sources: [],
    },
  }

  const result: NodeQualityCheckResult = {
    node_id: 10,
    quality_status: 'healthy',
    quality_score: 93,
    quality_grade: 'A',
    quality_summary: 'connectivity ok',
    quality_checked_at: '2026-04-01T11:12:13Z',
    items: [],
  }

  const updated = applyQualityResultToManageList(page, 'node-a', result)

  assert.equal(updated?.items[0]?.runtime_status, 'normal')
  assert.equal(updated?.items[0]?.tag, 'tag-a')
  assert.equal(updated?.items[0]?.quality_status, 'healthy')
  assert.equal(updated?.items[0]?.quality_score, 93)
  assert.equal(updated?.items[0]?.quality_grade, 'A')
  assert.equal(updated?.items[1]?.quality_status, undefined)
})

test('reduceBatchQualityEvent tracks start, progress and completion', () => {
  let state = reduceBatchQualityEvent(null, {
    type: 'start',
    total: 3,
  })

  assert.deepEqual(state, {
    status: 'running',
    total: 3,
    current: 0,
    success: 0,
    failed: 0,
    lastResult: null,
  })

  state = reduceBatchQualityEvent(state, {
    type: 'progress',
    tag: 'node-a',
    name: 'Node A',
    status: 'success',
    error: '',
    quality_status: 'healthy',
    quality_score: 95,
    quality_grade: 'A',
    current: 1,
    total: 3,
  } satisfies QualityCheckBatchEvent)

  assert.equal(state?.current, 1)
  assert.equal(state?.success, 1)
  assert.equal(state?.failed, 0)
  assert.deepEqual(state?.lastResult, {
    tag: 'node-a',
    name: 'Node A',
    status: 'success',
    error: '',
    quality_status: 'healthy',
    quality_score: 95,
    quality_grade: 'A',
  })

  state = reduceBatchQualityEvent(state, {
    type: 'complete',
    total: 3,
    success: 2,
    failed: 1,
  })

  assert.equal(state?.status, 'completed')
  assert.equal(state?.total, 3)
  assert.equal(state?.success, 2)
  assert.equal(state?.failed, 1)
  assert.equal(state?.current, 3)
})

test('buildQualityResultFromBatchProgress converts a successful batch event into detail data', () => {
  const result = buildQualityResultFromBatchProgress({
    type: 'progress',
    tag: 'node-a',
    name: 'Node A',
    status: 'success',
    error: '',
    quality_status: 'warn',
    quality_score: 80,
    quality_grade: 'B',
    quality_summary: '通过 1 项，告警 2 项，失败 0 项',
    quality_checked_at: '2026-04-01T15:13:44Z',
    exit_ip: '72.37.216.68',
    exit_country: 'United States',
    exit_country_code: 'US',
    exit_region: 'California',
    items: [
      { target: 'base_connectivity', status: 'pass', http_status: 200, latency_ms: 120, message: 'proxy exit reachable' },
      { target: 'openai', status: 'warn', http_status: 401, latency_ms: 180, message: 'HTTP 401 but target reachable' },
      { target: 'anthropic', status: 'warn', http_status: 405, latency_ms: 190, message: 'HTTP 405 but target reachable' },
    ],
    current: 1,
    total: 1,
  } satisfies QualityCheckBatchEvent)

  assert.equal(result?.quality_status, 'warn')
  assert.equal(result?.quality_summary, '通过 1 项，告警 2 项，失败 0 项')
  assert.equal(result?.quality_checked_at, '2026-04-01T15:13:44Z')
  assert.equal(result?.exit_country_code, 'US')
  assert.equal(result?.items.length, 3)
  assert.deepEqual(result?.items.map(item => item.target), ['base_connectivity', 'openai', 'anthropic'])
})
