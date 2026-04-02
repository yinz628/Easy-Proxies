import assert from 'node:assert/strict'
import test from 'node:test'

import {
  buildQualityCacheEntry,
  buildQualityResultFromJobResult,
  mergeQualityJobSnapshot,
  reduceBatchQualityEvent,
} from '../src/components/managePanelQuality.ts'
import type { BatchQualityState, BatchQualityLastResult } from '../src/components/managePanelQuality.ts'

test('buildQualityCacheEntry only restores ai_reachability_v2 results', () => {
  const restored = buildQualityCacheEntry({
    name: 'node-a',
    uri: 'socks5://127.0.0.1:1080',
    port: 1080,
    username: '',
    password: '',
    quality_version: 'ai_reachability_v2',
    quality_status: 'openai_only',
    quality_openai_status: 'pass',
    quality_anthropic_status: 'fail',
    quality_score: 80,
    quality_grade: 'B',
    quality_summary: 'OpenAI 可用',
    quality_checked: 1712016000,
  })

  assert.equal(restored?.quality_status, 'openai_only')
  assert.equal(restored?.quality_openai_status, 'pass')
  assert.equal(restored?.quality_anthropic_status, 'fail')

  const ignored = buildQualityCacheEntry({
    name: 'node-b',
    uri: 'socks5://127.0.0.1:1081',
    port: 1081,
    username: '',
    password: '',
    quality_version: 'legacy',
    quality_status: 'dual_available',
  })

  assert.equal(ignored, null)
})

test('buildQualityResultFromJobResult restores persisted provider results', () => {
  const result = buildQualityResultFromJobResult({
    tag: 'tag-a',
    name: 'Node A',
    quality_version: 'ai_reachability_v2',
    quality_status: 'dual_available',
    quality_openai_status: 'pass',
    quality_anthropic_status: 'pass',
    quality_score: 100,
    quality_grade: 'A',
    quality_summary: '双端可用',
    quality_checked_at: '2026-04-02T00:00:00Z',
    items: [
      {
        target: 'openai_reachability',
        status: 'pass',
      },
    ],
  })

  assert.equal(result?.quality_status, 'dual_available')
  assert.equal(result?.quality_openai_status, 'pass')
  assert.equal(result?.quality_anthropic_status, 'pass')
  assert.equal(result?.items.length, 1)
})

test('reduceBatchQualityEvent keeps success and failure counters from progress events', () => {
  const initial: BatchQualityState | null = {
    jobId: 'job-1',
    status: 'queued',
    total: 2,
    current: 0,
    success: 0,
    failed: 0,
    lastResult: null,
  }

  const next = reduceBatchQualityEvent(initial, {
    type: 'progress',
    job_id: 'job-1',
    tag: 'tag-a',
    name: 'Node A',
    status: 'success',
    error: '',
    quality_version: 'ai_reachability_v2',
    quality_status: 'openai_only',
    quality_openai_status: 'pass',
    quality_anthropic_status: 'fail',
    quality_score: 75,
    quality_grade: 'B',
    quality_summary: '仅 OpenAI 可用',
    quality_checked_at: '2026-04-02T00:00:00Z',
    items: [],
    current: 1,
    total: 2,
    success: 1,
    failed: 0,
  })

  assert.equal(next.status, 'running')
  assert.equal(next.current, 1)
  assert.equal(next.success, 1)
  assert.equal(next.failed, 0)
  assert.equal(next.lastResult?.name, 'Node A')
})

test('mergeQualityJobSnapshot preserves last streamed result when snapshot omits it', () => {
  const lastResult: BatchQualityLastResult = {
    tag: 'tag-a',
    name: 'Node A',
    status: 'success',
    error: '',
    quality_status: 'dual_available',
    quality_openai_status: 'pass',
    quality_anthropic_status: 'pass',
    quality_score: 100,
    quality_grade: 'A',
  }

  const merged = mergeQualityJobSnapshot({
    jobId: 'job-1',
    status: 'running',
    total: 2,
    current: 1,
    success: 1,
    failed: 0,
    lastResult,
  }, {
    id: 'job-1',
    status: 'running',
    started_at: '2026-04-02T00:00:00Z',
    updated_at: '2026-04-02T00:00:01Z',
    total: 2,
    completed: 1,
    success: 1,
    failed: 0,
    active_workers: 1,
  })

  assert.equal(merged?.lastResult?.name, 'Node A')
  assert.equal(merged?.success, 1)
})
