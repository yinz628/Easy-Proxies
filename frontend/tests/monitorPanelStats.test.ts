import assert from 'node:assert/strict'
import test from 'node:test'

import type { ConfigNodesResponse, NodesResponse } from '../src/types/index.ts'
import { buildMonitorPanelStats } from '../src/components/monitorPanelStats.ts'

test('buildMonitorPanelStats uses runtime node count for dashboard total card', () => {
  const data: NodesResponse = {
    nodes: [
      {
        tag: 'node-1',
        name: 'runtime-1',
        uri: 'socks5://1.1.1.1:1080',
        mode: 'pool',
        failure_count: 0,
        success_count: 1,
        blacklisted: false,
        blacklisted_until: '',
        active_connections: 0,
        last_latency_ms: 1200,
        available: true,
        initial_check_done: true,
        total_upload: 0,
        total_download: 0,
      },
      {
        tag: 'node-2',
        name: 'runtime-2',
        uri: 'socks5://2.2.2.2:1080',
        mode: 'pool',
        failure_count: 0,
        success_count: 1,
        blacklisted: false,
        blacklisted_until: '',
        active_connections: 0,
        last_latency_ms: 1400,
        available: true,
        initial_check_done: true,
        total_upload: 0,
        total_download: 0,
      },
    ],
    total_nodes: 2,
    total_upload: 0,
    total_download: 0,
    region_stats: {},
    region_healthy: {},
  }

  const configData: ConfigNodesResponse = {
    nodes: [
      { name: 'runtime-1', uri: 'socks5://1.1.1.1:1080', port: 0, username: '', password: '', lifecycle_state: 'active' },
      { name: 'runtime-2', uri: 'socks5://2.2.2.2:1080', port: 0, username: '', password: '', lifecycle_state: 'active' },
      { name: 'staged-1', uri: 'socks5://3.3.3.3:1080', port: 0, username: '', password: '', lifecycle_state: 'staged' },
      { name: 'staged-2', uri: 'socks5://4.4.4.4:1080', port: 0, username: '', password: '', lifecycle_state: 'staged' },
      { name: 'disabled-1', uri: 'socks5://5.5.5.5:1080', port: 0, username: '', password: '', lifecycle_state: 'disabled', disabled: true },
    ],
  }

  const stats = buildMonitorPanelStats(data, configData)

  assert.equal(stats.runtimeNodeCount, 2)
  assert.equal(stats.availableNodes, 2)
  assert.equal(stats.stagedNodes, 2)
  assert.equal(stats.disabledNodes, 1)
  assert.equal(stats.pendingNodes, 0)
})
