import { useMemo, useState } from 'react'
import { ChevronDown, ChevronRight, File, Folder } from 'lucide-react'

export interface TreeNode {
  name: string
  path: string // 目录以 / 结尾；文件是完整相对路径
  children: TreeNode[]
}

/**
 * 把扁平路径折成树。
 * 排序：同层文件在前、目录在后，各自按名字。
 */
export function buildTree(paths: string[]): TreeNode[] {
  const root: TreeNode[] = []

  for (const p of paths.slice().sort()) {
    const parts = p.split('/').filter(Boolean)
    let level = root
    let acc = ''
    for (let i = 0; i < parts.length; i++) {
      const name = parts[i]
      acc = acc ? `${acc}/${name}` : name
      const isFile = i === parts.length - 1
      let node = level.find((n) => n.name === name && (isFile ? n.children.length === 0 || !n.path.endsWith('/') : n.path.endsWith('/')))
      // 更简单：按 name 找；若是文件且已有同名目录则并存
      if (!node) {
        node = level.find((n) => n.name === name)
      }
      if (!node) {
        node = {
          name,
          path: isFile ? p : acc + '/',
          children: [],
        }
        level.push(node)
      }
      if (!isFile) {
        level = node.children
      }
    }
  }

  sortLevel(root)
  return root
}

function sortLevel(nodes: TreeNode[]) {
  nodes.sort((a, b) => {
    const aDir = a.children.length > 0 || a.path.endsWith('/')
    const bDir = b.children.length > 0 || b.path.endsWith('/')
    // 文件在前，目录在后（与计划测试一致）
    if (aDir !== bDir) return aDir ? 1 : -1
    return a.name.localeCompare(b.name)
  })
  for (const n of nodes) sortLevel(n.children)
}

export function FileTree({
  paths,
  selected,
  onSelect,
}: {
  paths: string[]
  selected: string
  onSelect: (path: string) => void
}) {
  const tree = useMemo(() => buildTree(paths), [paths])
  return (
    <ul className="text-sm select-none" role="tree">
      {tree.map((n) => (
        <TreeItem key={n.path} node={n} selected={selected} onSelect={onSelect} depth={0} />
      ))}
    </ul>
  )
}

function TreeItem({
  node,
  selected,
  onSelect,
  depth,
}: {
  node: TreeNode
  selected: string
  onSelect: (path: string) => void
  depth: number
}) {
  const isDir = node.children.length > 0 || node.path.endsWith('/')
  const [open, setOpen] = useState(true)
  const isSelected = !isDir && selected === node.path

  return (
    <li role="treeitem" aria-expanded={isDir ? open : undefined}>
      <button
        type="button"
        onClick={() => {
          if (isDir) setOpen((o) => !o)
          else onSelect(node.path)
        }}
        className={`flex w-full items-center gap-1 rounded px-1 py-0.5 text-left hover:bg-wash ${
          isSelected ? 'bg-accent-soft text-accent' : 'text-ink2'
        }`}
        style={{ paddingLeft: 4 + depth * 12 }}
      >
        {isDir ? (
          open ? <ChevronDown size={12} /> : <ChevronRight size={12} />
        ) : (
          <span className="w-3" />
        )}
        {isDir ? <Folder size={14} className="shrink-0 text-ink3" /> : <File size={14} className="shrink-0 text-ink3" />}
        <span className="truncate">{node.name}</span>
      </button>
      {isDir && open && (
        <ul role="group">
          {node.children.map((c) => (
            <TreeItem key={c.path} node={c} selected={selected} onSelect={onSelect} depth={depth + 1} />
          ))}
        </ul>
      )}
    </li>
  )
}
