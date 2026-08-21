package protocol

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// 占位符的词法与转义规则（spec §6.1）。
//
// 放在 protocol 的理由（spec §2.2）：hub 要用它做发布期校验与引用提取，
// agent 要用它做渲染与还原，两侧必须逐位一致。
//
// 本文件**不含任何取值逻辑**——值从哪来是 hub 与 agent 各自的事，
// Render 通过调用方传入的闭包取值。

var ErrBadPlaceholder = errors.New("protocol: 占位符语法错误")

type RefKind uint8

const (
	RefCred     RefKind = 1
	RefVar      RefKind = 2
	RefMachine  RefKind = 3
	RefProvider RefKind = 4
)

func (k RefKind) String() string {
	switch k {
	case RefCred:
		return "cred"
	case RefVar:
		return "var"
	case RefMachine:
		return "machine"
	case RefProvider:
		return "provider"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(k))
	}
}

// MachineKeys 是 {{machine.*}} 允许的全部名字。
// 收在这里是因为它属于「词法词汇表」：两侧必须认同同一组内置名。
var MachineKeys = []string{"name", "hostname", "os", "arch"}

// ProviderKeys 是 {{provider.*}} 允许的全部名字（M1.5 spec §3.1）。
//
// Ref.Name 是**字段名，不是 Provider 名**：绑定在配置集里唯一，不需要指名。
// 与 MachineKeys 同理，它属于「词法词汇表」——两侧必须认同同一组内置名。
// 顺序即 UI 展示顺序，不要重排。
var ProviderKeys = []string{
	"base_url", "auth_token", "model", "model_opus", "model_sonnet", "model_haiku",
}

type Ref struct {
	Kind RefKind
	Name string
}

func (r Ref) String() string { return r.Kind.String() + "." + r.Name }

// Segment 要么是字面段（Ref == nil），要么是一处引用（Text 为空）。
type Segment struct {
	Text string
	Ref  *Ref
}

// MissingRefError 表示渲染时某个引用没有值。
type MissingRefError struct{ Ref Ref }

func (e *MissingRefError) Error() string {
	return "protocol: 未定义的占位符引用 " + e.Ref.String()
}

const openTok = "{{"

// Parse 把内容切成字面段与引用段。
func Parse(content []byte) ([]Segment, error) {
	var segs []Segment
	var lit bytes.Buffer
	s := string(content)

	flush := func() {
		if lit.Len() > 0 {
			segs = append(segs, Segment{Text: lit.String()})
			lit.Reset()
		}
	}

	for i := 0; i < len(s); {
		if !strings.HasPrefix(s[i:], openTok) {
			lit.WriteByte(s[i])
			i++
			continue
		}
		// "{{{{" 是字面的 "{{"
		if strings.HasPrefix(s[i:], openTok+openTok) {
			lit.WriteString(openTok)
			i += 4
			continue
		}
		end := strings.Index(s[i+2:], "}}")
		if end < 0 {
			return nil, fmt.Errorf("%w: 未闭合的 {{ 于偏移 %d", ErrBadPlaceholder, i)
		}
		body := s[i+2 : i+2+end]
		ref, err := parseRef(body)
		if err != nil {
			return nil, err
		}
		flush()
		segs = append(segs, Segment{Ref: &ref})
		i += 2 + end + 2
	}
	flush()
	if len(segs) == 0 {
		segs = append(segs, Segment{})
	}
	return segs, nil
}

func parseRef(body string) (Ref, error) {
	prefix, name, ok := strings.Cut(body, ".")
	if !ok {
		return Ref{}, fmt.Errorf("%w: %q 缺少 . 分隔", ErrBadPlaceholder, body)
	}
	if !validName(name) {
		return Ref{}, fmt.Errorf("%w: %q 的名字非法（只允许 [A-Za-z0-9_-]+）", ErrBadPlaceholder, body)
	}
	switch prefix {
	case "cred":
		return Ref{Kind: RefCred, Name: name}, nil
	case "var":
		return Ref{Kind: RefVar, Name: name}, nil
	case "machine":
		for _, k := range MachineKeys {
			if k == name {
				return Ref{Kind: RefMachine, Name: name}, nil
			}
		}
		return Ref{}, fmt.Errorf("%w: machine.%s 不是内置名（只有 %v）", ErrBadPlaceholder, name, MachineKeys)
	case "provider":
		for _, k := range ProviderKeys {
			if k == name {
				return Ref{Kind: RefProvider, Name: name}, nil
			}
		}
		return Ref{}, fmt.Errorf("%w: provider.%s 不是内置名（只有 %v）",
			ErrBadPlaceholder, name, ProviderKeys)
	default:
		return Ref{}, fmt.Errorf("%w: 未知前缀 %q", ErrBadPlaceholder, prefix)
	}
}

func validName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// Refs 提取内容里出现的全部引用，去重并按 String() 升序。
// 发布期把它存进 revisions.refs，下发时按它过滤凭据（spec §5.3）。
func Refs(content []byte) ([]Ref, error) {
	segs, err := Parse(content)
	if err != nil {
		return nil, err
	}
	seen := map[string]Ref{}
	for _, s := range segs {
		if s.Ref != nil {
			seen[s.Ref.String()] = *s.Ref
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Ref, 0, len(keys))
	for _, k := range keys {
		out = append(out, seen[k])
	}
	return out, nil
}

// Render 是 Parse 的逆运算：把引用段换成值，字面段原样输出。
// lookup 返回 false 即视为未定义引用，返回 *MissingRefError。
func Render(segs []Segment, lookup func(Ref) (string, bool)) ([]byte, error) {
	var buf bytes.Buffer
	for _, s := range segs {
		if s.Ref == nil {
			buf.WriteString(s.Text)
			continue
		}
		v, ok := lookup(*s.Ref)
		if !ok {
			return nil, &MissingRefError{Ref: *s.Ref}
		}
		buf.WriteString(v)
	}
	return buf.Bytes(), nil
}

// EscapeLiteral 把内容里所有字面的 "{{" 写成 "{{{{"，
// 使 Parse 之后能原样还原。采集与收编写入 blob 之前用它。
func EscapeLiteral(s string) string {
	return strings.ReplaceAll(s, openTok, openTok+openTok)
}
