# 子计划 02 · 协议扩展、占位符词法、checksum 口径

**前置**：无（与 01 可并行）
**读这份之前先读** [00-overview.md](00-overview.md)。

**交付物**：`protocol` 包的 Kind 10–19 与十种消息、尺寸上限与动作码常量、`protocol/placeholder.go`、`protocol/checksum.go`，以及 agent 侧 WS 的 1 MiB 上限。

**为什么这些必须一次做对**：checksum 的拼装口径与占位符的词法是 hub 与 agent 必须**逐位一致**的两样东西（spec §2.2）。checksum 改了等于全部历史版本重新物化；占位符词法两侧不一致会让 `render(restore(x)) == x` 失效，而那是整个凭据机制的正确性支点（spec §6.1）。

---

### Task 1: Kind 10–19 与消息结构

**Files:**
- Modify: `protocol/envelope.go`
- Create: `protocol/messages_m1.go`
- Modify: `protocol/messages.go`（`MachineInfo` 追加两个字段）
- Test: `protocol/messages_m1_test.go`

**Interfaces:**
- Produces: 十个 Kind 常量、十种消息结构、`MaxFileSize` / `MaxBatchSize` / `MaxPayload`、`Action*` / `Drift*` / `Mode*` / `Reason*` / `Op*` / `Skip*` 常量组（逐字符见 00-overview）

- [ ] **Step 1: 写失败的测试**

Create `protocol/messages_m1_test.go`：

```go
package protocol_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/protocol"
)

func TestM1KindsAreKnown(t *testing.T) {
	for k := protocol.Kind(10); k <= 19; k++ {
		require.True(t, k.IsKnown(), "Kind %d 必须被认识", uint8(k))
		require.NotContains(t, k.String(), "unknown", "Kind %d 必须有名字", uint8(k))
	}
	// 20–29 是 M2 的段，M1 不许侵占。
	require.False(t, protocol.Kind(20).IsKnown())
}

func TestConfigSnapshotRoundTrip(t *testing.T) {
	want := protocol.ConfigSnapshot{
		ConfigSetID: "set1",
		RevisionID:  "rev7",
		Seq:         7,
		Manifest:    []byte(`{"version":1}`),
		Files: []protocol.FileEntry{
			{Path: ".claude/settings.json", Hash: "aa", Size: 12, Mode: 0o600},
			{Path: ".claude.json", Hash: "bb", Size: 34, Mode: 0o644, Keys: []string{"mcpServers"}},
		},
		Checksum:    "cc",
		Credentials: map[string]string{"anthropic_key": "sk-test"},
		Variables:   map[string]string{"workspace": "main"},
		IgnorePaths: []string{".claude/skills/scratch/**"},
		Mode:        protocol.ModeSurvey,
	}
	b, err := protocol.Encode(protocol.KindConfigSnapshot, nil, want)
	require.NoError(t, err)

	env, err := protocol.Decode(b)
	require.NoError(t, err)
	require.Equal(t, protocol.KindConfigSnapshot, env.Kind)

	got, err := protocol.DecodePayload[protocol.ConfigSnapshot](env)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestApplyAckRoundTrip(t *testing.T) {
	want := protocol.ApplyAck{
		RevisionID: "rev7",
		OK:         false,
		Results: []protocol.ApplyResult{
			{Path: ".claude/CLAUDE.md", Action: protocol.ActionOverwrite},
			{Path: ".claude/settings.json", Action: protocol.ActionCreate, Error: "permission denied"},
		},
		Error:      "写入失败",
		RolledBack: true,
		DurationMs: 42,
	}
	b, err := protocol.Encode(protocol.KindApplyAck, nil, want)
	require.NoError(t, err)
	env, err := protocol.Decode(b)
	require.NoError(t, err)
	got, err := protocol.DecodePayload[protocol.ApplyAck](env)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestDriftReportRoundTrip(t *testing.T) {
	want := protocol.DriftReport{
		Items: []protocol.DriftItem{
			{Path: ".claude/CLAUDE.md", Kind: protocol.DriftModified, BaseHash: "aa",
				Content: []byte("# 新内容 {{cred.k}}"), Mode: 0o644},
			{Path: ".claude/skills/foo/SKILL.md", Kind: protocol.DriftAdded, Content: []byte("x"), Mode: 0o644},
			{Path: ".claude/gone.md", Kind: protocol.DriftDeleted, BaseHash: "bb"},
			{Path: ".claude/huge.md", Kind: protocol.DriftModified, Truncated: true, RestorePartial: true},
		},
		Final: true,
		Full:  true,
	}
	b, err := protocol.Encode(protocol.KindDriftReport, nil, want)
	require.NoError(t, err)
	env, err := protocol.Decode(b)
	require.NoError(t, err)
	got, err := protocol.DecodePayload[protocol.DriftReport](env)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// 加字段必须向后兼容：老版本结构解新版本字节，多出来的字段被忽略。
func TestMachineInfoNewFieldsAreBackwardCompatible(t *testing.T) {
	b, err := protocol.Encode(protocol.KindMachineInfo, nil, protocol.MachineInfo{
		Hostname:    "mac",
		LocalPaused: true,
		ManagedHome: "/Users/x",
	})
	require.NoError(t, err)

	// 只有 M0 五个字段的结构体
	type oldInfo struct {
		Hostname     string            `cbor:"0,keyasint"`
		OS           string            `cbor:"1,keyasint"`
		Arch         string            `cbor:"2,keyasint"`
		AgentVersion string            `cbor:"3,keyasint"`
		ToolVersions map[string]string `cbor:"4,keyasint,omitempty"`
	}
	env, err := protocol.Decode(b)
	require.NoError(t, err)
	old, err := protocol.DecodePayload[oldInfo](env)
	require.NoError(t, err)
	require.Equal(t, "mac", old.Hostname)
}

func TestSizeLimits(t *testing.T) {
	require.Equal(t, 512*1024, protocol.MaxFileSize)
	require.Equal(t, 256*1024, protocol.MaxBatchSize)
	require.Equal(t, 1024*1024, protocol.MaxPayload)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./protocol/...`
Expected: FAIL，`undefined: protocol.KindConfigSnapshot`

- [ ] **Step 3: 加 Kind**

在 `protocol/envelope.go` 的 const 块追加，并扩展 `IsKnown` 与 `String`：

```go
const (
	// M1 配置闭环（spec §5）。10–19 用满；M1 若再需要新消息，
	// 从 40 起新开一段，不侵占 M2 的 20–29。
	KindConfigNotify   Kind = 10 // hub   → agent
	KindConfigPull     Kind = 11 // agent → hub
	KindConfigSnapshot Kind = 12 // hub   → agent
	KindBlobRequest    Kind = 13 // agent → hub
	KindBlobData       Kind = 14 // hub   → agent
	KindApplyAck       Kind = 15 // agent → hub
	KindDriftReport    Kind = 16 // agent → hub
	KindDriftCommand   Kind = 17 // hub   → agent
	KindCollectRequest Kind = 18 // hub   → agent
	KindCollectResult  Kind = 19 // agent → hub
)
```

`IsKnown` 的 case 追加这十个；`String` 追加：

```go
	case KindConfigNotify:
		return "config_notify"
	case KindConfigPull:
		return "config_pull"
	case KindConfigSnapshot:
		return "config_snapshot"
	case KindBlobRequest:
		return "blob_request"
	case KindBlobData:
		return "blob_data"
	case KindApplyAck:
		return "apply_ack"
	case KindDriftReport:
		return "drift_report"
	case KindDriftCommand:
		return "drift_command"
	case KindCollectRequest:
		return "collect_request"
	case KindCollectResult:
		return "collect_result"
```

- [ ] **Step 4: 加消息结构**

Create `protocol/messages_m1.go`，字段编号逐字符照抄 spec §5.2：

```go
package protocol

// M1 的全部消息（spec §5.2）。字段编号只增不改不复用。

// 尺寸上限（spec §5.4）。两侧共用同一份常量。
const (
	// MaxFileSize 是单个受管文件的上限。超限：采集时跳过并记入 Skipped，
	// 发布时校验拒绝，漂移时只报路径并置 Truncated。
	MaxFileSize = 512 << 10
	// MaxBatchSize 是携带内容的消息的分批阈值，最后一批置 Final。
	MaxBatchSize = 256 << 10
	// MaxPayload 是 WS 的 ReadMaxPayloadSize，hub 与 agent 两侧都要显式设。
	MaxPayload = 1 << 20
)

// ApplyResult.Action
const (
	ActionSkip      uint8 = 1
	ActionCreate    uint8 = 2
	ActionOverwrite uint8 = 3
	ActionDelete    uint8 = 4
	ActionMerge     uint8 = 5
)

// DriftItem.Kind
const (
	DriftAdded    uint8 = 1
	DriftModified uint8 = 2
	DriftDeleted  uint8 = 3
)

// ConfigSnapshot.Mode
const (
	ModeApply  uint8 = 0
	ModeSurvey uint8 = 1
)

// ConfigNotify.Reason
const (
	ReasonPublished = "published"
	ReasonAdopted   = "adopted"
	ReasonRotated   = "rotated"
	ReasonAssigned  = "assigned"
)

// DriftCommand.Op
const (
	OpRestore = "restore"
	OpIgnore  = "ignore"
)

// 跳过原因（CollectResult.Skipped 与 manifest 展开共用）
const (
	SkipTooLarge       = "too_large"
	SkipNotRegular     = "not_regular"
	SkipAlwaysExcluded = "always_excluded"
	SkipUnreadable     = "unreadable"
)

// ConfigNotify 只发信号不带内容（spec §5.1）：凭据轮换可以复用同一条消息，
// 且不产生新 Revision。
type ConfigNotify struct {
	ConfigSetID string `cbor:"0,keyasint"`
	RevisionID  string `cbor:"1,keyasint,omitempty"` // 空 = 仅 secrets 变更
	Seq         uint32 `cbor:"2,keyasint,omitempty"`
	Reason      string `cbor:"3,keyasint,omitempty"`
}

type ConfigPull struct {
	Have string `cbor:"0,keyasint,omitempty"` // 本地已应用的 RevisionID，供 hub 记日志
}

type ConfigSnapshot struct {
	ConfigSetID string            `cbor:"0,keyasint"`
	RevisionID  string            `cbor:"1,keyasint"`
	Seq         uint32            `cbor:"2,keyasint"`
	Manifest    []byte            `cbor:"3,keyasint"` // 原样 JSON，与发布时冻结的一致
	Files       []FileEntry       `cbor:"4,keyasint"`
	Checksum    string            `cbor:"5,keyasint"`
	Credentials map[string]string `cbor:"6,keyasint,omitempty"` // 只含本 Revision 引用到的
	Variables   map[string]string `cbor:"7,keyasint,omitempty"`
	IgnorePaths []string          `cbor:"8,keyasint,omitempty"`
	Mode        uint8             `cbor:"9,keyasint,omitempty"`
}

type FileEntry struct {
	Path string   `cbor:"0,keyasint"`
	Hash string   `cbor:"1,keyasint"` // keys 模式为受管键子树的规范化 hash
	Size uint32   `cbor:"2,keyasint"`
	Mode uint32   `cbor:"3,keyasint"` // 0600 / 0644
	Keys []string `cbor:"4,keyasint,omitempty"`
}

type BlobRequest struct {
	Hashes []string `cbor:"0,keyasint"`
}

type BlobData struct {
	Hash    string `cbor:"0,keyasint"`
	Content []byte `cbor:"1,keyasint"`
	Missing bool   `cbor:"2,keyasint,omitempty"` // hub 侧找不到，agent 据此中止 apply
}

type ApplyAck struct {
	RevisionID string        `cbor:"0,keyasint"`
	OK         bool          `cbor:"1,keyasint"`
	Results    []ApplyResult `cbor:"2,keyasint,omitempty"`
	Error      string        `cbor:"3,keyasint,omitempty"`
	RolledBack bool          `cbor:"4,keyasint,omitempty"`
	DurationMs uint32        `cbor:"5,keyasint,omitempty"`
}

type ApplyResult struct {
	Path   string `cbor:"0,keyasint"`
	Action uint8  `cbor:"1,keyasint"`
	Error  string `cbor:"2,keyasint,omitempty"`
}

// DriftReport 携带**已还原为占位符**的完整内容：diff 由 hub 计算，
// 收编因此退化成纯 hub 侧操作（spec §8.2）。
type DriftReport struct {
	Items []DriftItem `cbor:"0,keyasint"`
	Final bool        `cbor:"1,keyasint,omitempty"`
	Full  bool        `cbor:"2,keyasint,omitempty"` // survey 模式的全量对账
}

type DriftItem struct {
	Path           string `cbor:"0,keyasint"`
	Kind           uint8  `cbor:"1,keyasint"`
	BaseHash       string `cbor:"2,keyasint,omitempty"`
	Content        []byte `cbor:"3,keyasint,omitempty"`
	Mode           uint32 `cbor:"4,keyasint,omitempty"`
	RestorePartial bool   `cbor:"5,keyasint,omitempty"`
	Truncated      bool   `cbor:"6,keyasint,omitempty"`
}

type DriftCommand struct {
	Op    string   `cbor:"0,keyasint"`
	Paths []string `cbor:"1,keyasint"`
}

type CollectRequest struct {
	Manifest []byte `cbor:"0,keyasint"`
	Token    string `cbor:"1,keyasint"` // 本次采集的标识，随结果回传，防串批
}

type CollectResult struct {
	Token   string          `cbor:"0,keyasint"`
	Files   []CollectedFile `cbor:"1,keyasint,omitempty"`
	Skipped []SkippedFile   `cbor:"2,keyasint,omitempty"`
	Final   bool            `cbor:"3,keyasint,omitempty"`
	Error   string          `cbor:"4,keyasint,omitempty"`
}

type CollectedFile struct {
	Path    string `cbor:"0,keyasint"`
	Content []byte `cbor:"1,keyasint"`
	Mode    uint32 `cbor:"2,keyasint"`
}

type SkippedFile struct {
	Path   string `cbor:"0,keyasint"`
	Reason string `cbor:"1,keyasint"`
}
```

在 `protocol/messages.go` 的 `MachineInfo` 里追加两个字段：

```go
	// M1 追加（spec §5.2）。keyasint 加字段向后兼容，老版本解码时忽略。
	LocalPaused bool   `cbor:"5,keyasint,omitempty"` // orciny-agent pause
	ManagedHome string `cbor:"6,keyasint,omitempty"` // 面板上显示「管的是哪个 home」
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./protocol/...`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add protocol/
git commit -m "feat: 协议 Kind 10-19 与 M1 消息结构"
```

---

### Task 2: 占位符词法

**Files:**
- Create: `protocol/placeholder.go`
- Test: `protocol/placeholder_test.go`

**Interfaces:**
- Produces: `RefKind` / `Ref` / `Segment` / `Parse` / `Refs` / `Render` / `EscapeLiteral` / `MissingRefError` / `ErrBadPlaceholder` / `MachineKeys`（逐字符见 00-overview）

**语法（spec §6.1）**

```
{{cred.<name>}}   {{var.<name>}}   {{machine.name|hostname|os|arch}}
<name> 字符集 [A-Za-z0-9_-]+
字面量 "{{" 写作 "{{{{"
```

- [ ] **Step 1: 写失败的测试**

Create `protocol/placeholder_test.go`：

```go
package protocol_test

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/protocol"
)

func TestParseLiteralOnly(t *testing.T) {
	segs, err := protocol.Parse([]byte("hello world"))
	require.NoError(t, err)
	require.Len(t, segs, 1)
	require.Nil(t, segs[0].Ref)
	require.Equal(t, "hello world", segs[0].Text)
}

func TestParseRefs(t *testing.T) {
	segs, err := protocol.Parse([]byte("a{{cred.my-key_1}}b{{var.ws}}c{{machine.hostname}}"))
	require.NoError(t, err)

	var got []string
	for _, s := range segs {
		if s.Ref != nil {
			got = append(got, s.Ref.String())
		}
	}
	require.Equal(t, []string{"cred.my-key_1", "var.ws", "machine.hostname"}, got)
}

// 转义：CLAUDE.md 里讲模板语法时会出现字面的 "{{"，不给转义就没法管理这类文件。
func TestParseEscape(t *testing.T) {
	segs, err := protocol.Parse([]byte("写 {{{{cred.x}} 表示字面量"))
	require.NoError(t, err)
	var sb strings.Builder
	for _, s := range segs {
		require.Nil(t, s.Ref, "转义后不该产生引用")
		sb.WriteString(s.Text)
	}
	require.Equal(t, "写 {{cred.x}} 表示字面量", sb.String())
}

func TestEscapeLiteralRoundTrips(t *testing.T) {
	raw := "模板写法是 {{cred.x}}，两个花括号 {{ 也要转义"
	segs, err := protocol.Parse([]byte(protocol.EscapeLiteral(raw)))
	require.NoError(t, err)
	var sb strings.Builder
	for _, s := range segs {
		require.Nil(t, s.Ref)
		sb.WriteString(s.Text)
	}
	require.Equal(t, raw, sb.String())
}

func TestParseRejectsBadSyntax(t *testing.T) {
	for _, bad := range []string{
		"{{cred.x",           // 未闭合
		"{{cred.}}",          // 空名
		"{{cred.a b}}",       // 非法字符
		"{{unknown.x}}",      // 未知前缀
		"{{cred}}",           // 缺 . 分隔
		"{{machine.secret}}", // machine 只认四个内置名
		"{{cred.{{x}}}}",     // 嵌套
	} {
		_, err := protocol.Parse([]byte(bad))
		require.ErrorIs(t, err, protocol.ErrBadPlaceholder, "%q 必须被拒", bad)
	}
}

func TestRefsAreDedupedAndSorted(t *testing.T) {
	refs, err := protocol.Refs([]byte("{{var.b}}{{cred.a}}{{var.b}}{{cred.a}}"))
	require.NoError(t, err)
	var got []string
	for _, r := range refs {
		got = append(got, r.String())
	}
	require.Equal(t, []string{"cred.a", "var.b"}, got)
}

func TestRenderFillsValues(t *testing.T) {
	segs, err := protocol.Parse([]byte(`{"key":"{{cred.k}}","ws":"{{var.w}}"}`))
	require.NoError(t, err)
	out, err := protocol.Render(segs, func(r protocol.Ref) (string, bool) {
		switch r.String() {
		case "cred.k":
			return "sk-secret", true
		case "var.w":
			return "main", true
		}
		return "", false
	})
	require.NoError(t, err)
	require.Equal(t, `{"key":"sk-secret","ws":"main"}`, string(out))
}

// 未定义引用绝不能渲染成字面 "{{cred.x}}" 落到 Claude Code 手里（spec §6.1）。
func TestRenderMissingRefIsAnError(t *testing.T) {
	segs, err := protocol.Parse([]byte("{{cred.gone}}"))
	require.NoError(t, err)
	_, err = protocol.Render(segs, func(protocol.Ref) (string, bool) { return "", false })
	require.Error(t, err)
	var missing *protocol.MissingRefError
	require.ErrorAs(t, err, &missing)
	require.Equal(t, "cred.gone", missing.Ref.String())
}

// 往返律：把任意内容 escape 之后 parse + render 回来必须一字不差。
// 这是整个凭据机制的正确性支点（spec §6.1 / §10.1）。
func TestParseRenderRoundTripProperty(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	alphabet := []string{"a", "b", "{", "}", "{{", "}}", "\n", "{{cred.x}}", "中文", " "}

	for range 500 {
		var sb strings.Builder
		for range rng.IntN(20) {
			sb.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		raw := sb.String()

		segs, err := protocol.Parse([]byte(protocol.EscapeLiteral(raw)))
		require.NoError(t, err, "输入 %q", raw)
		out, err := protocol.Render(segs, func(protocol.Ref) (string, bool) { return "", false })
		require.NoError(t, err, "输入 %q", raw)
		require.Equal(t, raw, string(out), "往返不一致：%q", raw)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./protocol/ -run Placeholder -v`
Expected: FAIL，`undefined: protocol.Parse`

- [ ] **Step 3: 实现**

Create `protocol/placeholder.go`：

```go
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
	RefCred    RefKind = 1
	RefVar     RefKind = 2
	RefMachine RefKind = 3
)

func (k RefKind) String() string {
	switch k {
	case RefCred:
		return "cred"
	case RefVar:
		return "var"
	case RefMachine:
		return "machine"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(k))
	}
}

// MachineKeys 是 {{machine.*}} 允许的全部名字。
// 收在这里是因为它属于「词法词汇表」：两侧必须认同同一组内置名。
var MachineKeys = []string{"name", "hostname", "os", "arch"}

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
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./protocol/...`
Expected: PASS，含 500 次随机往返

- [ ] **Step 5: 提交**

```bash
git add protocol/placeholder.go protocol/placeholder_test.go
git commit -m "feat: 占位符词法、转义与引用提取"
```

---

### Task 3: checksum 口径

**Files:**
- Create: `protocol/checksum.go`
- Test: `protocol/checksum_test.go`

**Interfaces:**
- Consumes: Task 1 的 `FileEntry`
- Produces: `func Checksum(files []FileEntry) string`

- [ ] **Step 1: 写失败的测试**

Create `protocol/checksum_test.go`：

```go
package protocol_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/protocol"
)

// 写死期望值：改了拼装顺序或分隔符而无人察觉，会让全部历史版本失去意义
// （spec §10.1 点名要这条）。
func TestChecksumIsPinned(t *testing.T) {
	files := []protocol.FileEntry{
		{Path: ".claude/settings.json", Hash: "aa", Size: 1, Mode: 0o600},
		{Path: ".claude/CLAUDE.md", Hash: "bb", Size: 2, Mode: 0o644},
	}
	// 手工按口径拼一遍：按 path 字典序，每条 path\x00hash\x00mode\n，
	// mode 为八进制无前导 0。
	want := sha256.Sum256([]byte(
		".claude/CLAUDE.md\x00bb\x00644\n" +
			".claude/settings.json\x00aa\x00600\n"))
	require.Equal(t, hex.EncodeToString(want[:]), protocol.Checksum(files))
}

func TestChecksumIgnoresInputOrder(t *testing.T) {
	a := []protocol.FileEntry{
		{Path: "b", Hash: "2", Mode: 0o644},
		{Path: "a", Hash: "1", Mode: 0o600},
	}
	b := []protocol.FileEntry{
		{Path: "a", Hash: "1", Mode: 0o600},
		{Path: "b", Hash: "2", Mode: 0o644},
	}
	require.Equal(t, protocol.Checksum(a), protocol.Checksum(b))
}

func TestChecksumChangesWithMode(t *testing.T) {
	a := []protocol.FileEntry{{Path: "a", Hash: "1", Mode: 0o600}}
	b := []protocol.FileEntry{{Path: "a", Hash: "1", Mode: 0o644}}
	require.NotEqual(t, protocol.Checksum(a), protocol.Checksum(b))
}

// Size 不进 checksum：它由 Hash 唯一决定，进去只是多一处可能不一致的地方。
func TestChecksumIgnoresSize(t *testing.T) {
	a := []protocol.FileEntry{{Path: "a", Hash: "1", Size: 10, Mode: 0o600}}
	b := []protocol.FileEntry{{Path: "a", Hash: "1", Size: 999, Mode: 0o600}}
	require.Equal(t, protocol.Checksum(a), protocol.Checksum(b))
}

func TestChecksumOfEmptyIsStable(t *testing.T) {
	require.Equal(t, protocol.Checksum(nil), protocol.Checksum([]protocol.FileEntry{}))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./protocol/ -run Checksum -v`
Expected: FAIL，`undefined: protocol.Checksum`

- [ ] **Step 3: 实现**

Create `protocol/checksum.go`：

```go
package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// Checksum 是一份文件清单的指纹（spec §7.2）。
//
// 口径（hub 与 agent 必须逐位一致，改动等于全部历史版本重新物化）：
// 对每个文件取 path\x00hash\x00mode\n —— mode 为八进制、无前导 0 ——
// 按 path 字典序拼接后整体 sha256，hex 输出。
//
// Size 不参与：它由 Hash 唯一决定，进去只是多一处可能不一致的地方。
// Keys 也不参与：keys 模式的 Hash 已经是受管键子树的规范化 hash。
func Checksum(files []FileEntry) string {
	sorted := make([]FileEntry, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	var sb strings.Builder
	for _, f := range sorted {
		sb.WriteString(f.Path)
		sb.WriteByte(0)
		sb.WriteString(f.Hash)
		sb.WriteByte(0)
		sb.WriteString(strconv.FormatUint(uint64(f.Mode), 8))
		sb.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./protocol/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add protocol/checksum.go protocol/checksum_test.go
git commit -m "feat: 版本 checksum 的拼装口径"
```

---

### Task 4: agent 侧 WS 的 1 MiB 上限

**Files:**
- Modify: `agent/internal/conn/dial.go:205`（`gws.NewClient` 的 `ClientOption`）
- Modify: `hub/internal/ws/handler.go:80`（改用常量，不改数值）
- Test: `agent/internal/conn/dial_test.go`

**Interfaces:**
- Consumes: `protocol.MaxPayload`

M0 只在 hub 侧设了 `ReadMaxPayloadSize`，agent 侧走的是 gws 库默认值（spec §5.4）。M1 的 `ConfigSnapshot` 与 `BlobData` 会逼近这个上限，两侧必须一致，否则会以「WS 连接莫名断开」的形式暴露。

- [ ] **Step 1: 写失败的测试**

追加到 `agent/internal/conn/dial_test.go`：

```go
// hub 与 agent 的读上限必须同源。M0 只设了 hub 侧，agent 走库默认值，
// 而 M1 的 ConfigSnapshot / BlobData 会逼近 1 MiB（spec §5.4）。
func TestClientReadMaxPayloadMatchesProtocol(t *testing.T) {
	require.Equal(t, 1<<20, protocol.MaxPayload)
	require.Equal(t, int(protocol.MaxPayload), conn.ClientReadMaxPayloadSize())
}
```

若该测试文件尚未 import `protocol`，补上。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./agent/internal/conn/ -run ReadMaxPayload -v`
Expected: FAIL，`undefined: conn.ClientReadMaxPayloadSize`

- [ ] **Step 3: 实现**

在 `agent/internal/conn/dial.go` 里，把 `gws.NewClient` 的选项改成：

```go
	socket, _, err := gws.NewClient(h, &gws.ClientOption{
		Addr:             wsURL(cfg.HubURL),
		HandshakeTimeout: cfg.HandshakeTimeout,
		// 必须与 hub 侧同值：M1 的 ConfigSnapshot / BlobData 会逼近上限，
		// 走库默认值会让超限连接以「莫名断开」的形式失败（spec §5.4）。
		ReadMaxPayloadSize: protocol.MaxPayload,
	})
```

并在同文件加一个导出的取值函数，供测试断言：

```go
// ClientReadMaxPayloadSize 返回 agent 侧 WS 的读上限，供测试核对两侧一致。
func ClientReadMaxPayloadSize() int { return protocol.MaxPayload }
```

在 `hub/internal/ws/handler.go` 把字面量换成常量（数值不变）：

```go
	h.upgrader = gws.NewUpgrader(h, &gws.ServerOption{
		// 与 agent 侧同源，见 protocol.MaxPayload。
		ReadMaxPayloadSize: protocol.MaxPayload,
	})
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./agent/... ./hub/... ./protocol/...`
Expected: PASS

- [ ] **Step 5: 全量回归并提交**

Run: `go test -tags=testing ./...`
Expected: PASS

```bash
git add agent/internal/conn/ hub/internal/ws/
git commit -m "fix: agent 侧 WS 显式设 1 MiB 读上限，与 hub 同源"
```
