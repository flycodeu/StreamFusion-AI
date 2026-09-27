import { createRenderer } from 'vue'

export interface Host {
  type: string
  text: string
  props: Record<string, unknown>
  children: Host[]
  parent: Host | null
}

export const node = (type: string, text = ''): Host => ({
  type,
  text,
  props: {},
  children: [],
  parent: null,
})
export const renderer = createRenderer<Host, Host>({
  // Static decorative SVG/HTML has no handlers; preserve it as one opaque host node.
  insertStaticContent(content, parent, anchor) {
    const target = node('#static', content)
    const position = anchor ? parent.children.indexOf(anchor) : -1
    parent.children.splice(position < 0 ? parent.children.length : position, 0, target)
    target.parent = parent
    return [target, target]
  },
  createElement: (type) => node(type),
  createText: (text) => node('#text', text),
  createComment: (text) => node('#comment', text),
  setText: (target, text) => {
    target.text = text
  },
  setElementText: (target, text) => {
    target.text = text
    target.children = []
  },
  patchProp: (target, key, _, value) => {
    target.props[key] = value
  },
  insert(target, parent, anchor) {
    if (target.parent) target.parent.children.splice(target.parent.children.indexOf(target), 1)
    const position = anchor ? parent.children.indexOf(anchor) : -1
    parent.children.splice(position < 0 ? parent.children.length : position, 0, target)
    target.parent = parent
  },
  remove(target) {
    if (target.parent) target.parent.children.splice(target.parent.children.indexOf(target), 1)
    target.parent = null
  },
  parentNode: (target) => target.parent,
  nextSibling: (target) => {
    const siblings = target.parent?.children || []
    return siblings[siblings.indexOf(target) + 1] || null
  },
})

export const nodes = (root: Host): Host[] => [root, ...root.children.flatMap(nodes)]
export const text = (root: Host): string => root.text + root.children.map(text).join('')
