interface ExportableNode {
  name: string
  uri: string
}

export function buildSelectedExportText(
  nodes: ExportableNode[],
  selectedNames: Set<string>,
): string {
  if (selectedNames.size === 0) {
    return ''
  }

  return nodes
    .filter((node) => selectedNames.has(node.name) && node.uri.trim())
    .map((node) => node.uri.trim())
    .join('\n')
}
