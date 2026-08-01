/**
 * JSON 校验，带行列定位。
 *
 * 含占位符的 JSON 在编辑器里是常态（{"key": "{{cred.k}}"}），
 * 甚至会出现在值的位置上（{"n": {{var.count}}}）。校验前先把占位符
 * 替换成一个合法的字面量，否则用户每敲一个占位符就看到一片红。
 */

export interface JsonProblem {
  line: number
  column: number
  message: string
}

export function lintJson(text: string): JsonProblem[] {
  if (text.trim() === '') return []
  // 先保护转义的 {{{{，再分别处理已加引号与裸露的占位符。
  // 已加引号的 "{{cred.k}}" 不能再包一层引号，否则变成 ""__PH__""。
  const normalized = text
    .replace(/\{\{\{\{/g, '__ESCAPED__')
    .replace(/"\{\{[^{}]*\}\}"/g, '"__PLACEHOLDER__"')
    .replace(/\{\{[^{}]*\}\}/g, '"__PLACEHOLDER__"')
    .replace(/__ESCAPED__/g, '{{')
  try {
    JSON.parse(normalized)
    return []
  } catch (e) {
    return [toProblem(text, e as Error)]
  }
}

function toProblem(text: string, e: Error): JsonProblem {
  const msg = e.message || 'JSON 语法错误'
  // 新 V8: "... at position 10 (line 3 column 5 of the JSON data)"
  // 老 V8: "... at position 12"
  // Safari: "JSON.parse: unexpected character at line 1 column 12 of the JSON data"
  // 最新 V8 有时只说 Unexpected token '}' 不带位置——回退到 token 定位。
  const lcInParens = msg.match(/\(line\s+(\d+)\s+column\s+(\d+)/i)
  if (lcInParens) {
    return { line: Number(lcInParens[1]), column: Number(lcInParens[2]), message: msg }
  }
  const lcMatch = msg.match(/line\s+(\d+)\s+column\s+(\d+)/i)
  if (lcMatch) {
    return { line: Number(lcMatch[1]), column: Number(lcMatch[2]), message: msg }
  }
  const posMatch = msg.match(/position\s+(\d+)/i)
  if (posMatch) {
    const { line, column } = offsetToLineCol(text, Number(posMatch[1]))
    return { line, column, message: msg }
  }
  // "Unexpected token '}'" / "Unexpected token }" —— 取该字符最后一次出现的位置。
  // 对 "b":\n} 这类错误，最后的 } 就是出错点，足够给编辑器跳转。
  const tokMatch = msg.match(/Unexpected token ['"]?(.)['"]?/)
  if (tokMatch) {
    const idx = text.lastIndexOf(tokMatch[1])
    if (idx >= 0) {
      const { line, column } = offsetToLineCol(text, idx)
      return { line, column, message: msg }
    }
  }
  return { line: 1, column: 1, message: msg }
}

function offsetToLineCol(text: string, offset: number): { line: number; column: number } {
  let line = 1
  let col = 1
  const limit = Math.min(offset, text.length)
  for (let i = 0; i < limit; i++) {
    if (text[i] === '\n') {
      line++
      col = 1
    } else {
      col++
    }
  }
  return { line, column: col }
}
