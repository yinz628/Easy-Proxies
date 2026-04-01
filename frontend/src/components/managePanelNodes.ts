import type { ConfigNodeConfig, NodeSnapshot } from '../types/index.ts'

export interface MergedManageNode extends ConfigNodeConfig {
  runtimeStatus: 'normal' | 'unavailable' | 'blacklisted' | 'pending' | 'disabled'
  latency_ms: number
  region?: string
  country?: string
  active_connections: number
  success_count: number
  failure_count: number
  tag?: string
}

export interface BatchProbeSelection {
  probeable: MergedManageNode[]
  skippedDisabled: number
  skippedNoTag: number
  skippedTotal: number
}

function normalizeLookupKey(value?: string): string {
  return value?.trim() || ''
}

function resolveRuntimeStatus(snapshot: NodeSnapshot): MergedManageNode['runtimeStatus'] {
  if (snapshot.blacklisted) return 'blacklisted'
  if (!snapshot.initial_check_done) return 'pending'
  if (snapshot.available) return 'normal'
  return 'unavailable'
}

function fallbackPendingNode(node: ConfigNodeConfig): MergedManageNode {
  return {
    ...node,
    runtimeStatus: 'pending',
    latency_ms: -1,
    region: undefined,
    country: undefined,
    active_connections: 0,
    success_count: 0,
    failure_count: 0,
    tag: undefined,
  }
}

function disabledNode(node: ConfigNodeConfig): MergedManageNode {
  return {
    ...node,
    runtimeStatus: 'disabled',
    latency_ms: -1,
    region: undefined,
    country: undefined,
    active_connections: 0,
    success_count: 0,
    failure_count: 0,
    tag: undefined,
  }
}

export function mergeManageNodes(
  configNodes: ConfigNodeConfig[],
  snapshots: NodeSnapshot[],
): MergedManageNode[] {
  const snapshotsByURI = new Map<string, NodeSnapshot>()
  const snapshotsByName = new Map<string, NodeSnapshot>()

  for (const snapshot of snapshots) {
    const uriKey = normalizeLookupKey(snapshot.uri)
    if (uriKey && !snapshotsByURI.has(uriKey)) {
      snapshotsByURI.set(uriKey, snapshot)
    }

    const nameKey = normalizeLookupKey(snapshot.name)
    if (nameKey && !snapshotsByName.has(nameKey)) {
      snapshotsByName.set(nameKey, snapshot)
    }
  }

  return configNodes.map((node): MergedManageNode => {
    if (node.disabled) {
      return disabledNode(node)
    }

    const snapshot = snapshotsByURI.get(normalizeLookupKey(node.uri))
      ?? snapshotsByName.get(normalizeLookupKey(node.name))

    if (!snapshot) {
      return fallbackPendingNode(node)
    }

    return {
      ...node,
      runtimeStatus: resolveRuntimeStatus(snapshot),
      latency_ms: snapshot.last_latency_ms,
      region: snapshot.region,
      country: snapshot.country,
      active_connections: snapshot.active_connections,
      success_count: typeof snapshot.success_count === 'number' ? snapshot.success_count : 0,
      failure_count: snapshot.failure_count,
      tag: snapshot.tag,
    }
  })
}

export function getBatchProbeSelection(
  nodes: MergedManageNode[],
  selectedNames: Set<string>,
): BatchProbeSelection {
  const probeable: MergedManageNode[] = []
  let skippedDisabled = 0
  let skippedNoTag = 0

  for (const node of nodes) {
    if (!selectedNames.has(node.name)) continue
    if (node.disabled) {
      skippedDisabled++
      continue
    }
    if (!node.tag) {
      skippedNoTag++
      continue
    }
    probeable.push(node)
  }

  return {
    probeable,
    skippedDisabled,
    skippedNoTag,
    skippedTotal: skippedDisabled + skippedNoTag,
  }
}
