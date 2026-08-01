import { useEffect, useRef } from 'react'
import { EditorState, type Extension } from '@codemirror/state'
import { EditorView, keymap, lineNumbers, highlightActiveLine } from '@codemirror/view'
import { defaultKeymap, history, historyKeymap } from '@codemirror/commands'
import { json } from '@codemirror/lang-json'
import { markdown } from '@codemirror/lang-markdown'
import { linter, type Diagnostic } from '@codemirror/lint'
import { autocompletion, type CompletionContext } from '@codemirror/autocomplete'
import { lintJson } from '@/lib/jsonLint'
import { completions, parsePlaceholders, undefinedRefs } from '@/lib/placeholder'

export interface CodeEditorProps {
  value: string
  path: string
  onChange: (value: string) => void
  /** 形如 "cred.foo" / "var.bar" 的已知引用 */
  knownRefs: string[]
  readOnly?: boolean
}

function langFor(path: string): Extension {
  if (path.endsWith('.json')) return json()
  if (path.endsWith('.md') || path.endsWith('.markdown')) return markdown()
  return []
}

function buildLinter(path: string, known: Set<string>) {
  return linter((view) => {
    const text = view.state.doc.toString()
    const diags: Diagnostic[] = []
    if (path.endsWith('.json')) {
      for (const p of lintJson(text)) {
        const line = view.state.doc.line(Math.min(p.line, view.state.doc.lines))
        const from = Math.min(line.from + p.column - 1, line.to)
        diags.push({ from, to: Math.min(from + 1, line.to), severity: 'error', message: p.message })
      }
    }
    const { errors } = parsePlaceholders(text)
    for (const msg of errors) {
      diags.push({ from: 0, to: 0, severity: 'error', message: msg })
    }
    for (const r of undefinedRefs(text, known)) {
      diags.push({
        from: 0,
        to: 0,
        severity: 'warning',
        message: `未定义的引用 ${r.kind}.${r.name}`,
      })
    }
    return diags
  })
}

function buildCompletion(known: string[]) {
  return autocompletion({
    override: [
      (ctx: CompletionContext) => {
        const before = ctx.matchBefore(/\{\{[\w.-]*/ )
        if (!before) return null
        const prefix = before.text.slice(2) // 去掉 {{
        const options = completions(prefix, known).map((c) => ({
          label: c,
          apply: `{{${c}}}`,
          type: 'variable' as const,
        }))
        return { from: before.from, options, filter: false }
      },
    ],
  })
}

export function CodeEditor({ value, path, onChange, knownRefs, readOnly }: CodeEditorProps) {
  const host = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView | null>(null)
  const onChangeRef = useRef(onChange)
  onChangeRef.current = onChange

  useEffect(() => {
    if (!host.current) return
    const known = new Set(knownRefs)
    const extensions: Extension[] = [
      lineNumbers(),
      highlightActiveLine(),
      history(),
      keymap.of([...defaultKeymap, ...historyKeymap]),
      langFor(path),
      buildLinter(path, known),
      buildCompletion(knownRefs),
      EditorView.updateListener.of((u) => {
        if (u.docChanged) onChangeRef.current(u.state.doc.toString())
      }),
      EditorView.theme({
        '&': { height: '100%', fontSize: '13px' },
        '.cm-scroller': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' },
      }),
      EditorState.readOnly.of(!!readOnly),
    ]
    const state = EditorState.create({ doc: value, extensions })
    const view = new EditorView({ state, parent: host.current })
    viewRef.current = view
    return () => {
      view.destroy()
      viewRef.current = null
    }
    // 路径 / 已知引用 / 只读变化时重建；value 由下面的 effect 同步
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, knownRefs.join('\0'), readOnly])

  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const cur = view.state.doc.toString()
    if (cur !== value) {
      view.dispatch({
        changes: { from: 0, to: cur.length, insert: value },
      })
    }
  }, [value])

  return <div ref={host} className="h-full min-h-[320px] overflow-hidden rounded border border-line bg-surface" />
}
