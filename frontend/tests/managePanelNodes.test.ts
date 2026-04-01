import assert from 'node:assert/strict'
import test from 'node:test'

import type { ConfigNodeConfig, NodeSnapshot } from '../src/types/index.ts'
import { getBatchProbeSelection, mergeManageNodes } from '../src/components/managePanelNodes.ts'

test('mergeManageNodes falls back to URI when snapshot name changed', () => {
  const configNodes: ConfigNodeConfig[] = [
    {
      name: 'config-name',
      uri: 'socks5://209.13.0.1:1080',
      port: 0,
      username: '',
      password: '',
    },
  ]

  const snapshots: NodeSnapshot[] = [
    {
      tag: 'node-1',
      name: 'runtime-name',
      uri: 'socks5://209.13.0.1:1080',
      mode: 'pool',
      failure_count: 1,
      success_count: 3,
      blacklisted: false,
      blacklisted_until: '',
      active_connections: 0,
      last_latency_ms: -1,
      available: false,
      initial_check_done: true,
      total_upload: 0,
      total_download: 0,
    },
  ]

  const [merged] = mergeManageNodes(configNodes, snapshots)

  assert.equal(merged.runtimeStatus, 'unavailable')
  assert.equal(merged.tag, 'node-1')
  assert.equal(merged.failure_count, 1)
})

test('getBatchProbeSelection reports skipped unloaded and disabled nodes', () => {
  const nodes = [
    {
      name: 'probeable',
      uri: 'socks5://1.1.1.1:1080',
      port: 0,
      username: '',
      password: '',
      runtimeStatus: 'normal' as const,
      latency_ms: 50,
      active_connections: 0,
      success_count: 1,
      failure_count: 0,
      tag: 'tag-1',
    },
    {
      name: 'unloaded',
      uri: 'socks5://2.2.2.2:1080',
      port: 0,
      username: '',
      password: '',
      runtimeStatus: 'pending' as const,
      latency_ms: -1,
      active_connections: 0,
      success_count: 0,
      failure_count: 0,
      tag: undefined,
    },
    {
      name: 'disabled',
      uri: 'socks5://3.3.3.3:1080',
      port: 0,
      username: '',
      password: '',
      disabled: true,
      runtimeStatus: 'disabled' as const,
      latency_ms: -1,
      active_connections: 0,
      success_count: 0,
      failure_count: 0,
      tag: 'tag-3',
    },
  ]

  const selection = getBatchProbeSelection(nodes, new Set(['probeable', 'unloaded', 'disabled']))

  assert.equal(selection.probeable.length, 1)
  assert.equal(selection.probeable[0]?.name, 'probeable')
  assert.equal(selection.skippedNoTag, 1)
  assert.equal(selection.skippedDisabled, 1)
  assert.equal(selection.skippedTotal, 2)
})
