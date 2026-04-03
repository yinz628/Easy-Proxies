import type { ConfigNodeConfig, ConfigNodesResponse, NodeSnapshot, NodesResponse } from '../types'

export interface MonitorPanelStats {
  runtimeNodeCount: number
  stagedNodes: number
  disabledNodes: number
  availableNodes: number
  unavailableNodes: number
  blacklistedNodes: number
  pendingNodes: number
}

export function buildMonitorPanelStats(
  data: NodesResponse | null,
  configData: ConfigNodesResponse | null,
): MonitorPanelStats {
  const runtimeNodes = data?.nodes || []
  const configNodes = configData?.nodes || []

  return {
    runtimeNodeCount: data?.total_nodes ?? runtimeNodes.length,
    stagedNodes: configNodes.filter((node: ConfigNodeConfig) => !node.disabled && node.lifecycle_state === 'staged').length,
    disabledNodes: configNodes.filter((node: ConfigNodeConfig) => node.disabled).length,
    availableNodes: runtimeNodes.filter((node: NodeSnapshot) => node.initial_check_done && node.available && !node.blacklisted).length,
    unavailableNodes: runtimeNodes.filter((node: NodeSnapshot) => node.initial_check_done && !node.available && !node.blacklisted).length,
    blacklistedNodes: runtimeNodes.filter((node: NodeSnapshot) => node.blacklisted).length,
    pendingNodes: runtimeNodes.filter((node: NodeSnapshot) => !node.initial_check_done && !node.blacklisted).length,
  }
}
