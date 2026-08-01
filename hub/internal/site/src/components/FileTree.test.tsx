import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { FileTree, buildTree } from '@/components/FileTree'

describe('buildTree', () => {
  it('把扁平路径折成树', () => {
    const tree = buildTree([
      '.claude.json',
      '.claude/CLAUDE.md',
      '.claude/skills/foo/SKILL.md',
      '.claude/skills/bar/SKILL.md',
    ])
    expect(tree.map((n) => n.name)).toEqual(['.claude.json', '.claude'])
    const claude = tree[1]
    expect(claude.children.map((n) => n.name)).toEqual(['CLAUDE.md', 'skills'])
    const skills = claude.children[1]
    expect(skills.children.map((n) => n.name)).toEqual(['bar', 'foo'])
  })

  it('目录排在文件之后，各自按名字排序', () => {
    const tree = buildTree(['.claude/z.md', '.claude/a/x.md', '.claude/b.md'])
    expect(tree[0].children.map((n) => n.name)).toEqual(['b.md', 'z.md', 'a'])
  })
})

describe('FileTree', () => {
  it('渲染全部叶子路径', () => {
    render(<FileTree paths={['.claude/CLAUDE.md', '.claude.json']} selected="" onSelect={() => {}} />)
    expect(screen.getByText('CLAUDE.md')).toBeInTheDocument()
    expect(screen.getByText('.claude.json')).toBeInTheDocument()
  })
})
