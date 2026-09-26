export function flattenTree<Node extends { children: Node[] }>(nodes: readonly Node[]): Node[] {
  return nodes.flatMap((node) => [node, ...flattenTree(node.children)])
}

export function treeIds<Node extends { id: string; children: Node[] }>(node: Node): Set<string> {
  return new Set(flattenTree([node]).map((item) => item.id))
}
