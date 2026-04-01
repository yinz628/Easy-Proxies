import test from 'node:test'
import assert from 'node:assert/strict'

import type { ConfigNodeConfig, NodeQualityCheckResult, QualityCheckBatchEvent } from '../src/types/index.ts'
import {
  applyQualityResultToConfigNode,
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
