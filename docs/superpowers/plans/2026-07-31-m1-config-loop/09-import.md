# 子计划 09 · 导入向导后端与敏感项检测

**前置**：08
**读这份之前先读** [00-overview.md](00-overview.md)。

**交付物**：agent 侧对 `CollectRequest` 的应答、`hub/internal/importer`（采集编排 + 三层敏感项检测 + 抽取为凭据）、接进 `configsync.CollectResult`。

**为什么它要紧（spec §9）**：产品 §4.2 称导入向导为「个人版关键路径」——它是新用户从零到有配置集的唯一入口，卡在这里就等于卡在门口。

**一条纪律（spec §9.1 第 7 步）**：导入完成后走**正常的发布 → 指派 → 下发**路径，不写「导入后直接标记已对齐」的捷径。内容就是从这台机器采的，plan 几乎全是 skip。捷径会让 `state.json` 从未被真正写过一次，第一次真正的 apply 会在几周后以一种没人预期的方式发生。

---

### Task 1: agent 侧采集

**Files:**
- Create: `agent/internal/syncer/collect.go`
- Test: `agent/internal/syncer/collect_test.go`

**Interfaces:**
- Consumes: `manifest.Manifest.Expand`、`protocol.CollectRequest` / `CollectResult`
- Produces: `Syncer.Handle` 支持 `protocol.KindCollectRequest`

**规则（spec §5.4 / §9.1）**：按累计 **256 KiB** 分批，最后一批置 `Final`；恒排除与超限文件进 `Skipped`，带原因。

- [ ] **Step 1: 写失败的测试**

Create `agent/internal/syncer/collect_test.go`：

```go
package syncer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

func collectResults(t *testing.T, r *rig) []protocol.CollectResult {
	t.Helper()
	var out []protocol.CollectResult
	for _, p := range r.out.of(protocol.KindCollectResult) {
		out = append(out, p.(protocol.CollectResult))
	}
	return out
}

func TestCollectReturnsManagedFiles(t *testing.T) {
	r := newRig(t)
	writeManaged(t, r, ".claude/settings.json", `{"model":"opus"}`)
	writeManaged(t, r, ".claude/CLAUDE.md", "# 规矩")
	writeManaged(t, r, ".claude/skills/foo/SKILL.md", "skill")
	writeManaged(t, r, ".claude.json", `{"mcpServers":{}}`)

	mj, err := manifest.Default().JSON()
	require.NoError(t, err)
	r.s.Handle(envelope(t, protocol.KindCollectRequest,
		protocol.CollectRequest{Manifest: mj, Token: "tok-1"}))

	res := collectResults(t, r)
	require.NotEmpty(t, res)
	require.True(t, res[len(res)-1].Final, "最后一批必须置 Final")

	paths := map[string]string{}
	for _, batch := range res {
		require.Equal(t, "tok-1", batch.Token, "token 随结果回传，防串批")
		for _, f := range batch.Files {
			paths[f.Path] = string(f.Content)
		}
	}
	require.Equal(t, `{"model":"opus"}`, paths[".claude/settings.json"])
	require.Equal(t, "# 规矩", paths[".claude/CLAUDE.md"])
	require.Equal(t, "skill", paths[".claude/skills/foo/SKILL.md"])
	require.Contains(t, paths, ".claude.json")
}

// 恒排除的东西绝不上传，哪怕 hub 在 manifest 里要了它（spec §3.2）。
func TestCollectRefusesAlwaysExcludedEvenIfRequested(t *testing.T) {
	r := newRig(t)
	writeManaged(t, r, ".claude/.credentials.json", `{"oauth":"绝密"}`)
	writeManaged(t, r, ".claude/projects/a/session.jsonl", "会话历史")
	writeManaged(t, r, ".claude/CLAUDE.md", "正常内容")

	// 一个被篡改的 hub 试图把恒排除路径塞进 manifest
	evil := `{"version":1,"include":[
		{"path":".claude/**","mode":"tree"},
		{"path":".claude/.credentials.json","mode":"file"}
	]}`
	r.s.Handle(envelope(t, protocol.KindCollectRequest,
		protocol.CollectRequest{Manifest: []byte(evil), Token: "tok-2"}))

	res := collectResults(t, r)
	for _, batch := range res {
		for _, f := range batch.Files {
			require.NotContains(t, f.Path, ".credentials.json")
			require.NotContains(t, f.Path, "/projects/")
			require.NotContains(t, string(f.Content), "绝密")
		}
	}
}

func TestCollectReportsSkipped(t *testing.T) {
	r := newRig(t)
	writeManaged(t, r, ".claude/CLAUDE.md", "正常")
	writeManaged(t, r, ".claude/skills/foo/HUGE.md",
		strings.Repeat("x", protocol.MaxFileSize+1))

	mj, err := manifest.Default().JSON()
	require.NoError(t, err)
	r.s.Handle(envelope(t, protocol.KindCollectRequest,
		protocol.CollectRequest{Manifest: mj, Token: "tok-3"}))

	var skipped []protocol.SkippedFile
	for _, batch := range collectResults(t, r) {
		skipped = append(skipped, batch.Skipped...)
	}
	require.Len(t, skipped, 1)
	require.Equal(t, ".claude/skills/foo/HUGE.md", skipped[0].Path)
	require.Equal(t, protocol.SkipTooLarge, skipped[0].Reason)
}

// 分批：单批累计不超过 256 KiB（spec §5.4）。
func TestCollectBatchesLargePayloads(t *testing.T) {
	r := newRig(t)
	chunk := strings.Repeat("x", 100*1024) // 100 KiB
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		writeManaged(t, r, ".claude/skills/"+name+"/SKILL.md", chunk)
	}
	mj, err := manifest.Default().JSON()
	require.NoError(t, err)
	r.s.Handle(envelope(t, protocol.KindCollectRequest,
		protocol.CollectRequest{Manifest: mj, Token: "tok-4"}))

	res := collectResults(t, r)
	require.Greater(t, len(res), 1, "500 KiB 必须分批")
	for _, batch := range res {
		total := 0
		for _, f := range batch.Files {
			total += len(f.Content)
		}
		require.LessOrEqual(t, total, protocol.MaxBatchSize)
	}
	require.True(t, res[len(res)-1].Final)
}

// manifest 解不开时明确报回去，不发一份空结果让 hub 以为「这台机器啥都没有」。
func TestCollectReportsManifestError(t *testing.T) {
	r := newRig(t)
	r.s.Handle(envelope(t, protocol.KindCollectRequest,
		protocol.CollectRequest{Manifest: []byte("不是 json"), Token: "tok-5"}))

	res := collectResults(t, r)
	require.Len(t, res, 1)
	require.True(t, res[0].Final)
	require.NotEmpty(t, res[0].Error)
	require.Empty(t, res[0].Files)
}
```

`writeManaged` 是 `rig` 上的小辅助（写到 `r.home` 下），加进 `syncer_test.go`：

```go
func writeManaged(t *testing.T, r *rig, rel, content string) {
	t.Helper()
	p := filepath.Join(r.home, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./agent/internal/syncer/ -run Collect -v`
Expected: FAIL，没有任何 `CollectResult` 被发出

- [ ] **Step 3: 实现**

Create `agent/internal/syncer/collect.go`：

```go
package syncer

import (
	"os"

	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

// collect 按给定 manifest 采集本机现状，分批回传（spec §9.1 第 3 步）。
func (s *Syncer) collect(req protocol.CollectRequest) {
	m, err := manifest.Parse(req.Manifest)
	if err != nil {
		// 明确报错，而不是发一份空结果让 hub 以为「这台机器啥都没有」
		// ——后者会生成一个空配置集，用户点下发布就把主力机清空了。
		s.log.Warn("采集请求里的 manifest 无法解析", "error", err)
		_ = s.d.Send(protocol.KindCollectResult, protocol.CollectResult{
			Token: req.Token, Final: true, Error: err.Error(),
		})
		return
	}

	exp, err := m.Expand(s.d.ManagedHome)
	if err != nil {
		_ = s.d.Send(protocol.KindCollectResult, protocol.CollectResult{
			Token: req.Token, Final: true, Error: err.Error(),
		})
		return
	}

	var (
		batch   []protocol.CollectedFile
		skipped []protocol.SkippedFile
		size    int
	)
	for _, sk := range exp.Skipped {
		skipped = append(skipped, protocol.SkippedFile{Path: sk.Rel, Reason: sk.Reason})
	}

	flush := func(final bool) {
		if err := s.d.Send(protocol.KindCollectResult, protocol.CollectResult{
			Token: req.Token, Files: batch, Skipped: skipped, Final: final,
		}); err != nil {
			s.log.Warn("回传采集结果失败", "error", err)
		}
		batch, skipped, size = nil, nil, 0
	}

	for _, f := range exp.Files {
		// 恒排除在展开时已经判过一次，这里不必再判——Expand 走的是同一份
		// 清单，且它把命中的路径放进了 Skipped 而不是 Files。
		content, err := os.ReadFile(f.Abs)
		if err != nil {
			skipped = append(skipped, protocol.SkippedFile{
				Path: f.Rel, Reason: protocol.SkipUnreadable,
			})
			continue
		}
		if len(content) > protocol.MaxFileSize {
			skipped = append(skipped, protocol.SkippedFile{
				Path: f.Rel, Reason: protocol.SkipTooLarge,
			})
			continue
		}
		// 攒够 256 KiB 就发一批（spec §5.4）：WS 帧上限 1 MiB，
		// 留足信封与其他字段的余量。
		if size+len(content) > protocol.MaxBatchSize && len(batch) > 0 {
			flush(false)
		}
		batch = append(batch, protocol.CollectedFile{
			Path: f.Rel, Content: content, Mode: uint32(f.Mode),
		})
		size += len(content)
	}
	flush(true)

	s.log.Info("采集完成", "token", req.Token,
		"files", len(exp.Files), "skipped", len(exp.Skipped))
}
```

在 `Handle` 的 switch 里追加：

```go
	case protocol.KindCollectRequest:
		req, err := protocol.DecodePayload[protocol.CollectRequest](env)
		if err != nil {
			s.log.Warn("解析 CollectRequest 失败", "error", err)
			return
		}
		s.collect(req)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./agent/internal/syncer/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/internal/syncer/
git commit -m "feat: agent 按 manifest 采集本机现状"
```

---

### Task 2: 敏感项检测

**Files:**
- Create: `hub/internal/importer/scan.go`
- Test: `hub/internal/importer/scan_test.go`

**Interfaces:**
- Produces: `Finding` / `Scan` / `Mask` / `SuggestName`（逐字符见 00-overview）

**三层规则（spec §9.2）**

1. **结构化位置**（准确率最高，优先展示）：`.claude/settings.json` 的 `env` 对象所有键值对；`.claude.json` 的 `mcpServers.*.env`
2. **键名特征**：`(?i)(key|token|secret|password|passwd|credential|auth)`
3. **值特征**：已知前缀 `sk-` / `sk-ant-` / `ghp_` / `gho_` / `github_pat_` / `xoxb-` / `AKIA` / `AIza` / `glpat-`；或长度 ≥ 32 且 Shannon 熵 ≥ 3.5 的 `[A-Za-z0-9+/_=-]+` 串
4. **全文兜底**：所有文本文件跑一遍前缀正则

**误报可接受，漏报不可接受**——规则宁滥勿缺，交互上让用户一键跳过。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/importer/scan_test.go`：

```go
package importer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/importer"
)

func rules(fs []importer.Finding) map[string]importer.Finding {
	out := map[string]importer.Finding{}
	for _, f := range fs {
		out[f.Location] = f
	}
	return out
}

func TestScanStructuredEnvInSettings(t *testing.T) {
	got := importer.Scan(".claude/settings.json", []byte(`{
		"model": "opus",
		"env": {
			"ANTHROPIC_AUTH_TOKEN": "sk-ant-abcdefghijklmnop",
			"HTTP_PROXY": "http://127.0.0.1:7890"
		}
	}`))

	byLoc := rules(got)
	f, ok := byLoc["env.ANTHROPIC_AUTH_TOKEN"]
	require.True(t, ok, "settings.json 的 env 是最高优先级的结构化位置")
	require.Equal(t, "structured", f.Rule)
	require.Equal(t, "ANTHROPIC_AUTH_TOKEN", f.Key)
	require.Equal(t, "anthropic_auth_token", f.Suggested)
	require.Equal(t, "sk-a…mnop", f.Masked)

	// env 里的每一项都要报出来，哪怕看起来不像密钥——误报可接受，漏报不可
	require.Contains(t, byLoc, "env.HTTP_PROXY")
}

func TestScanMcpServersEnv(t *testing.T) {
	got := importer.Scan(".claude.json", []byte(`{
		"mcpServers": {
			"github": {
				"command": "npx",
				"env": {"GITHUB_TOKEN": "ghp_abcdefghijklmnopqrst"}
			}
		}
	}`))
	byLoc := rules(got)
	f, ok := byLoc["mcpServers.github.env.GITHUB_TOKEN"]
	require.True(t, ok)
	require.Equal(t, "structured", f.Rule)
	require.Equal(t, "github_token", f.Suggested)
}

func TestScanKeyNamePattern(t *testing.T) {
	got := importer.Scan(".claude/settings.json", []byte(`{
		"someApiKey": "短的也要报",
		"harmless": "普通值"
	}`))
	byLoc := rules(got)
	require.Contains(t, byLoc, "someApiKey")
	require.Equal(t, "key_name", byLoc["someApiKey"].Rule)
	require.NotContains(t, byLoc, "harmless")
}

func TestScanValuePrefixes(t *testing.T) {
	for _, v := range []string{
		"sk-abcdefghijklmn", "sk-ant-abcdefghijkl", "ghp_abcdefghijklmnop",
		"gho_abcdefghijklmnop", "github_pat_abcdefghij", "xoxb-1234-5678-abcd",
		"AKIAIOSFODNN7EXAMPLE", "AIzaSyA-abcdefghijklmnop", "glpat-abcdefghijklmn",
	} {
		got := importer.Scan(".claude/CLAUDE.md", []byte("我的 key 是 "+v+" 别外传"))
		require.NotEmpty(t, got, "%q 必须被全文兜底抓到", v)
		require.Equal(t, "value_prefix", got[0].Rule)
	}
}

func TestScanHighEntropyValue(t *testing.T) {
	// 长度 ≥ 32 且熵 ≥ 3.5
	got := importer.Scan(".claude/settings.json",
		[]byte(`{"opaque":"aZ9x2Kq7Lm4Pw8Rt5Yv1Bn6Cd3Ef0Gh2Jk"}`))
	require.NotEmpty(t, got)
	require.Equal(t, "value_entropy", got[0].Rule)
}

// 低熵的长串不该报：重复字符、纯路径。
func TestScanIgnoresLowEntropyLongValues(t *testing.T) {
	for _, v := range []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"00000000000000000000000000000000",
	} {
		got := importer.Scan(".claude/settings.json", []byte(`{"v":"`+v+`"}`))
		require.Empty(t, got, "%q 熵太低，不该报", v)
	}
}

func TestScanFullTextFallback(t *testing.T) {
	got := importer.Scan(".claude/CLAUDE.md",
		[]byte("# 备忘\n\n临时把 key 记在这里：sk-ant-api03-abcdefghij\n"))
	require.Len(t, got, 1)
	require.Equal(t, "value_prefix", got[0].Rule)
	require.Equal(t, ".claude/CLAUDE.md", got[0].Path)
}

func TestMask(t *testing.T) {
	require.Equal(t, "sk-a…mnop", importer.Mask("sk-ant-abcdefghijklmnop"))
	// 太短的值全部遮掉：前 4 后 4 会把它整个露出来
	require.Equal(t, "…", importer.Mask("short"))
	require.Equal(t, "…", importer.Mask(""))
}

func TestSuggestName(t *testing.T) {
	require.Equal(t, "anthropic_auth_token", importer.SuggestName("ANTHROPIC_AUTH_TOKEN"))
	require.Equal(t, "some_api_key", importer.SuggestName("someApiKey"))
	require.Equal(t, "github_token", importer.SuggestName("GITHUB-TOKEN"))
	require.Equal(t, "cred", importer.SuggestName(""), "空名字要有兜底")
}

// 掩码后的字符串里绝不能出现完整值——UI 会直接把它渲染出来。
func TestFindingsNeverCarryFullValue(t *testing.T) {
	secret := "sk-ant-abcdefghijklmnopqrstuvwx"
	got := importer.Scan(".claude/settings.json", []byte(`{"env":{"K":"`+secret+`"}}`))
	require.NotEmpty(t, got)
	for _, f := range got {
		require.NotContains(t, f.Masked, secret)
		require.NotContains(t, f.Location, secret)
		require.NotContains(t, f.Suggested, secret)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/importer/...`
Expected: FAIL，包不存在

- [ ] **Step 3: 实现**

Create `hub/internal/importer/scan.go`：

```go
// Package importer 是导入向导的后端：采集编排与敏感项检测（spec §9）。
package importer

import (
	"math"
	"regexp"
	"strings"
	"unicode"

	"github.com/tidwall/gjson"
)

// Finding 是一条待人工处置的敏感项。三个动作由 UI 给出：
// 抽取为凭据 / 保留明文（二次确认）/ 把该文件移出纳管范围。
type Finding struct {
	Path      string `json:"path"`
	Location  string `json:"location"`  // 结构化位置，如 env.ANTHROPIC_AUTH_TOKEN
	Key       string `json:"key"`
	Masked    string `json:"masked"`    // 前 4 后 4，中间省略
	Suggested string `json:"suggested"` // 由键名派生的建议凭据名
	Rule      string `json:"rule"`
}

// 命中规则的名字，UI 按它排优先级。
const (
	RuleStructured    = "structured"
	RuleKeyName       = "key_name"
	RuleValuePrefix   = "value_prefix"
	RuleValueEntropy  = "value_entropy"
)

// 检测阈值（spec §9.2）。
const (
	minEntropyLen = 32
	minEntropy    = 3.5
)

var (
	keyNamePattern = regexp.MustCompile(`(?i)(key|token|secret|password|passwd|credential|auth)`)
	// 已知前缀。全文兜底也用它——CLAUDE.md 里粘了一个 key 也要能抓到。
	valuePrefixes = []string{
		"sk-ant-", "sk-", "ghp_", "gho_", "github_pat_", "xoxb-", "AKIA", "AIza", "glpat-",
	}
	prefixPattern = regexp.MustCompile(
		`(sk-ant-|sk-|ghp_|gho_|github_pat_|xoxb-|AKIA|AIza|glpat-)[A-Za-z0-9\-_]{8,}`)
	opaquePattern = regexp.MustCompile(`^[A-Za-z0-9+/_=-]+$`)
)

// Scan 对一份内容跑三层检测。
//
// 误报可接受，漏报不可接受——规则宁滥勿缺，交互上让用户一键跳过（spec §9.2）。
func Scan(path string, content []byte) []Finding {
	var out []Finding
	seen := map[string]bool{}
	add := func(f Finding) {
		if seen[f.Location] {
			return
		}
		seen[f.Location] = true
		out = append(out, f)
	}

	if gjson.ValidBytes(content) {
		scanStructured(path, content, add)
		scanJSONKeys(path, content, add)
	}
	scanFullText(path, content, add)
	return out
}

// scanStructured 扫两处结构化位置：settings.json 的 env、
// .claude.json 的 mcpServers.*.env。这两处准确率最高，UI 优先展示。
func scanStructured(path string, content []byte, add func(Finding)) {
	emit := func(prefix string, obj gjson.Result) {
		obj.ForEach(func(k, v gjson.Result) bool {
			// env 里的每一项都报——它们按定义就是要注入进进程环境的东西。
			add(Finding{
				Path: path, Location: prefix + k.String(), Key: k.String(),
				Masked: Mask(v.String()), Suggested: SuggestName(k.String()),
				Rule: RuleStructured,
			})
			return true
		})
	}

	base := strings.TrimSuffix(path, "/")
	switch {
	case strings.HasSuffix(base, ".claude/settings.json"), base == "settings.json":
		emit("env.", gjson.GetBytes(content, "env"))
	case strings.HasSuffix(base, ".claude.json"), base == ".claude.json":
		gjson.GetBytes(content, "mcpServers").ForEach(func(name, srv gjson.Result) bool {
			emit("mcpServers."+name.String()+".env.", srv.Get("env"))
			return true
		})
	}
}

// scanJSONKeys 走键名与值特征，覆盖结构化位置之外的地方。
func scanJSONKeys(path string, content []byte, add func(Finding)) {
	var walk func(prefix string, r gjson.Result)
	walk = func(prefix string, r gjson.Result) {
		r.ForEach(func(k, v gjson.Result) bool {
			loc := k.String()
			if prefix != "" {
				loc = prefix + "." + k.String()
			}
			switch {
			case v.IsObject():
				walk(loc, v)
			case v.Type == gjson.String:
				val := v.String()
				switch {
				case keyNamePattern.MatchString(k.String()):
					add(Finding{Path: path, Location: loc, Key: k.String(),
						Masked: Mask(val), Suggested: SuggestName(k.String()), Rule: RuleKeyName})
				case hasKnownPrefix(val):
					add(Finding{Path: path, Location: loc, Key: k.String(),
						Masked: Mask(val), Suggested: SuggestName(k.String()), Rule: RuleValuePrefix})
				case looksOpaque(val):
					add(Finding{Path: path, Location: loc, Key: k.String(),
						Masked: Mask(val), Suggested: SuggestName(k.String()), Rule: RuleValueEntropy})
				}
			}
			return true
		})
	}
	walk("", gjson.ParseBytes(content))
}

// scanFullText 是兜底：CLAUDE.md 里粘了一个 key 也要能抓到（spec §9.2）。
func scanFullText(path string, content []byte, add func(Finding)) {
	for i, m := range prefixPattern.FindAllString(string(content), -1) {
		add(Finding{
			Path:      path,
			Location:  fullTextLocation(i),
			Key:       "",
			Masked:    Mask(m),
			Suggested: SuggestName(path),
			Rule:      RuleValuePrefix,
		})
	}
}

func fullTextLocation(i int) string {
	if i == 0 {
		return "全文匹配"
	}
	return "全文匹配 #" + itoa(i+1)
}

func hasKnownPrefix(v string) bool {
	for _, p := range valuePrefixes {
		if strings.HasPrefix(v, p) {
			return true
		}
	}
	return false
}

// looksOpaque：长度 ≥ 32、字符集像 base64/token、且 Shannon 熵 ≥ 3.5。
// 三个条件缺一不可——只看长度会把文件路径全报出来。
func looksOpaque(v string) bool {
	if len(v) < minEntropyLen || !opaquePattern.MatchString(v) {
		return false
	}
	return shannon(v) >= minEntropy
}

func shannon(s string) float64 {
	if s == "" {
		return 0
	}
	counts := map[rune]int{}
	for _, c := range s {
		counts[c]++
	}
	var h float64
	n := float64(len([]rune(s)))
	for _, c := range counts {
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}

// Mask 留前 4 后 4。太短的值全部遮掉——前 4 后 4 会把它整个露出来。
func Mask(v string) string {
	if len(v) < 12 {
		return "…"
	}
	return v[:4] + "…" + v[len(v)-4:]
}

// SuggestName 由键名派生凭据名：ANTHROPIC_AUTH_TOKEN → anthropic_auth_token，
// someApiKey → some_api_key。字符集与占位符语法一致（[A-Za-z0-9_-]+）。
func SuggestName(key string) string {
	var b strings.Builder
	prevLower := false
	for _, c := range key {
		switch {
		case unicode.IsUpper(c):
			if prevLower {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(c))
			prevLower = false
		case unicode.IsLower(c) || unicode.IsDigit(c):
			b.WriteRune(c)
			prevLower = true
		default:
			b.WriteByte('_')
			prevLower = false
		}
	}
	name := strings.Trim(collapseUnderscores(b.String()), "_")
	if name == "" {
		return "cred"
	}
	return name
}

func collapseUnderscores(s string) string {
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}
	return s
}
```

`itoa` 用 `strconv.Itoa`，import 补上 `strconv`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/importer/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/internal/importer/
git commit -m "feat: 三层敏感项检测"
```

---

### Task 3: 采集编排与抽取为凭据

**Files:**
- Create: `hub/internal/importer/service.go`
- Test: `hub/internal/importer/service_test.go`

**Interfaces:**
- Produces: `Service` / `NewService` / `Start` / `HandleResult` / `Findings` / `Extract`

**流程（spec §9.1）**

1. hub 发 `CollectRequest{Manifest, Token}`
2. agent 分批回 `CollectResult`
3. hub 落 blob，**直接建一个草稿态 config_set**（`head` 为空，`draft` = 采集结果）。不需要单独的 `import_sessions` collection——草稿本来就是这个形状
4. `Extract` 把某处的值抽成凭据：建凭据 → 把该文件内容里的值替成 `{{cred.name}}` → 重写草稿

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/importer/service_test.go`：

```go
package importer_test

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/credentials"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/importer"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/protocol"
)

type fakeSender struct {
	sent []protocol.CollectRequest
}

func (f *fakeSender) SendTo(_ string, kind protocol.Kind, payload any) error {
	if kind == protocol.KindCollectRequest {
		f.sent = append(f.sent, payload.(protocol.CollectRequest))
	}
	return nil
}
func (f *fakeSender) Online(string) bool { return true }

type rig struct {
	app    *tests.TestApp
	sets   *configsets.Service
	creds  *credentials.Store
	blobs  *blobs.Store
	sender *fakeSender
	svc    *importer.Service
	m      string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	b := blobs.New(app)
	ev := events.NewWriter(app)
	key, err := credentials.LoadMasterKey(t.TempDir())
	require.NoError(t, err)

	r := &rig{
		app: app, blobs: b,
		sets:   configsets.NewService(app, b, ev),
		creds:  credentials.NewStore(app, key, ev),
		sender: &fakeSender{},
	}
	r.svc = importer.NewService(app, b, r.sets, r.creds, ev, r.sender)

	mc, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	rec := core.NewRecord(mc)
	rec.Set("fingerprint", "fp")
	rec.Set("pub_key", "pk")
	rec.Set("status", "online")
	rec.Set("name", "主力机")
	require.NoError(t, app.Save(rec))
	r.m = rec.Id
	return r
}

func TestStartSendsCollectRequestWithDefaultManifest(t *testing.T) {
	r := newRig(t)
	token, setID, err := r.svc.Start(r.m)
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.NotEmpty(t, setID)
	require.Len(t, r.sender.sent, 1)
	require.Equal(t, token, r.sender.sent[0].Token)
	require.Contains(t, string(r.sender.sent[0].Manifest), ".claude/settings.json")

	set, err := r.app.FindRecordById("config_sets", setID)
	require.NoError(t, err)
	require.Empty(t, set.GetString("head"), "导入中的配置集处于草稿态")
}

func TestHandleResultFillsDraft(t *testing.T) {
	r := newRig(t)
	token, setID, err := r.svc.Start(r.m)
	require.NoError(t, err)

	require.NoError(t, r.svc.HandleResult(r.m, protocol.CollectResult{
		Token: token,
		Files: []protocol.CollectedFile{
			{Path: ".claude/CLAUDE.md", Content: []byte("# 规矩"), Mode: 0o644},
		},
	}))
	require.NoError(t, r.svc.HandleResult(r.m, protocol.CollectResult{
		Token: token,
		Files: []protocol.CollectedFile{
			{Path: ".claude/settings.json", Content: []byte(`{"env":{"K":"sk-ant-abcdefghij"}}`), Mode: 0o600},
		},
		Skipped: []protocol.SkippedFile{{Path: ".claude/big.md", Reason: protocol.SkipTooLarge}},
		Final:   true,
	}))

	draft, err := r.sets.Draft(setID)
	require.NoError(t, err)
	require.Len(t, draft, 2, "两批都要进同一份草稿")

	kinds := map[string]bool{}
	recs, err := r.app.FindAllRecords("events")
	require.NoError(t, err)
	for _, rec := range recs {
		kinds[rec.GetString("kind")] = true
	}
	require.True(t, kinds[events.KindImportCompleted], "Final 之后才写完成事件")
}

// token 对不上的结果直接丢弃：防串批（spec §5.2）。
func TestHandleResultRejectsWrongToken(t *testing.T) {
	r := newRig(t)
	_, setID, err := r.svc.Start(r.m)
	require.NoError(t, err)

	require.Error(t, r.svc.HandleResult(r.m, protocol.CollectResult{
		Token: "别的批次",
		Files: []protocol.CollectedFile{{Path: "a", Content: []byte("x"), Mode: 0o644}},
	}))
	draft, err := r.sets.Draft(setID)
	require.NoError(t, err)
	require.Empty(t, draft)
}

func TestFindingsScanDraft(t *testing.T) {
	r := newRig(t)
	token, setID, err := r.svc.Start(r.m)
	require.NoError(t, err)
	require.NoError(t, r.svc.HandleResult(r.m, protocol.CollectResult{
		Token: token, Final: true,
		Files: []protocol.CollectedFile{
			{Path: ".claude/settings.json",
				Content: []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-ant-abcdefghijkl"}}`), Mode: 0o600},
		},
	}))

	found, err := r.svc.Findings(setID)
	require.NoError(t, err)
	require.NotEmpty(t, found)
	require.Equal(t, ".claude/settings.json", found[0].Path)
	require.Equal(t, "anthropic_auth_token", found[0].Suggested)
}

// 抽取：建凭据 + 把值替成占位符 + 重写草稿。库里此后查不到明文。
func TestExtractReplacesValueWithPlaceholder(t *testing.T) {
	r := newRig(t)
	token, setID, err := r.svc.Start(r.m)
	require.NoError(t, err)
	secret := "sk-ant-abcdefghijklmnop"
	require.NoError(t, r.svc.HandleResult(r.m, protocol.CollectResult{
		Token: token, Final: true,
		Files: []protocol.CollectedFile{
			{Path: ".claude/settings.json",
				Content: []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"` + secret + `"}}`), Mode: 0o600},
		},
	}))

	require.NoError(t, r.svc.Extract(setID, ".claude/settings.json",
		"env.ANTHROPIC_AUTH_TOKEN", "anthropic_auth_token"))

	draft, err := r.sets.Draft(setID)
	require.NoError(t, err)
	content, err := r.blobs.Get(draft[0].Hash)
	require.NoError(t, err)
	require.Contains(t, string(content), "{{cred.anthropic_auth_token}}")
	require.NotContains(t, string(content), secret)

	v, err := r.creds.Value("anthropic_auth_token")
	require.NoError(t, err)
	require.Equal(t, secret, v)

	// 抽取之后重扫，这一条不该再出现
	found, err := r.svc.Findings(setID)
	require.NoError(t, err)
	for _, f := range found {
		require.NotEqual(t, "env.ANTHROPIC_AUTH_TOKEN", f.Location)
	}
}

// 同一个值出现在多处：一次抽取全部替掉，否则漏一处就等于没脱敏。
func TestExtractReplacesEveryOccurrence(t *testing.T) {
	r := newRig(t)
	token, setID, err := r.svc.Start(r.m)
	require.NoError(t, err)
	secret := "sk-ant-abcdefghijklmnop"
	body := `{"env":{"A":"` + secret + `","B":"` + secret + `"}}`
	require.NoError(t, r.svc.HandleResult(r.m, protocol.CollectResult{
		Token: token, Final: true,
		Files: []protocol.CollectedFile{
			{Path: ".claude/settings.json", Content: []byte(body), Mode: 0o600},
		},
	}))

	require.NoError(t, r.svc.Extract(setID, ".claude/settings.json", "env.A", "k"))

	draft, err := r.sets.Draft(setID)
	require.NoError(t, err)
	content, err := r.blobs.Get(draft[0].Hash)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(string(content), "{{cred.k}}"))
	require.NotContains(t, string(content), secret)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/importer/ -run 'Start|HandleResult|Findings|Extract' -v`
Expected: FAIL，`undefined: importer.NewService`

- [ ] **Step 3: 实现**

Create `hub/internal/importer/service.go`：

```go
package importer

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/tidwall/gjson"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/credentials"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

// Sender 与 configsync.Sender 同形。这里重新声明一遍是为了不让 importer
// 依赖 configsync——两者是平级的编排者，没有上下游关系。
type Sender interface {
	SendTo(machineID string, kind protocol.Kind, payload any) error
	Online(machineID string) bool
}

type Service struct {
	app    core.App
	blobs  *blobs.Store
	sets   *configsets.Service
	creds  *credentials.Store
	ev     *events.Writer
	sender Sender

	// 进行中的采集：machineID → {token, setID}。
	// 不落库：一次采集只跨几秒钟，hub 重启后重来一次即可，
	// 而多一张表就多一份要清理的垃圾。
	pending map[string]session
}

type session struct {
	token string
	setID string
}

func NewService(app core.App, b *blobs.Store, sets *configsets.Service,
	creds *credentials.Store, ev *events.Writer, sender Sender) *Service {
	return &Service{
		app: app, blobs: b, sets: sets, creds: creds, ev: ev, sender: sender,
		pending: map[string]session{},
	}
}

// Start 触发一次采集，同时建好草稿态配置集。
//
// 不需要单独的 import_sessions collection——草稿本来就是这个形状（spec §9.1）。
func (s *Service) Start(machineID string) (string, string, error) {
	m, err := s.app.FindRecordById("machines", machineID)
	if err != nil {
		return "", "", fmt.Errorf("importer: 机器 %s 不存在: %w", machineID, err)
	}
	name := m.GetString("name")
	if name == "" {
		name = m.GetString("hostname")
	}
	if name == "" {
		name = "导入的配置集"
	}

	set, err := s.sets.Create(uniqueName(s.app, name+" 的配置"), "从 "+name+" 采集")
	if err != nil {
		return "", "", err
	}

	mj, err := manifest.Default().JSON()
	if err != nil {
		return "", "", err
	}
	token, err := newToken()
	if err != nil {
		return "", "", err
	}
	s.pending[machineID] = session{token: token, setID: set.Id}

	if err := s.sender.SendTo(machineID, protocol.KindCollectRequest,
		protocol.CollectRequest{Manifest: mj, Token: token}); err != nil {
		return "", "", fmt.Errorf("importer: 机器不在线，无法采集: %w", err)
	}
	return token, set.Id, nil
}

// HandleResult 落一批采集结果。分批到达，Final 之后收尾。
func (s *Service) HandleResult(machineID string, res protocol.CollectResult) error {
	sess, ok := s.pending[machineID]
	if !ok {
		return fmt.Errorf("importer: 机器 %s 没有进行中的采集", machineID)
	}
	// token 随结果回传，防串批（spec §5.2）：用户连点两次「采集」时，
	// 第一批的结果不该落进第二次的草稿。
	if res.Token != sess.token {
		return fmt.Errorf("importer: 采集批次不匹配，已丢弃")
	}
	if res.Error != "" {
		delete(s.pending, machineID)
		return fmt.Errorf("importer: agent 采集失败: %s", res.Error)
	}

	for _, f := range res.Files {
		// 恒排除在 hub 侧再判一次（spec §3.2 的双侧纵深防御）。
		if manifest.IsAlwaysExcluded(f.Path) {
			s.app.Logger().Warn("采集结果里出现恒排除路径，已丢弃",
				"machine", machineID, "path", f.Path)
			continue
		}
		// 采集到的是**原始内容**，其中字面的 "{{" 必须转义，
		// 否则用户 CLAUDE.md 里讲模板语法的那段会被当成占位符（spec §6.1）。
		escaped := []byte(protocol.EscapeLiteral(string(f.Content)))
		if _, err := s.sets.SetDraftFile(sess.setID, f.Path, escaped, f.Mode, keysFor(f.Path)); err != nil {
			s.app.Logger().Warn("落草稿失败", "path", f.Path, "error", err)
		}
	}

	if !res.Final {
		return nil
	}
	delete(s.pending, machineID)

	detail := map[string]any{"config_set": sess.setID, "skipped": len(res.Skipped)}
	if len(res.Skipped) > 0 {
		var paths []string
		for _, sk := range res.Skipped {
			paths = append(paths, sk.Path+"("+sk.Reason+")")
		}
		detail["skipped_paths"] = paths
	}
	if err := s.ev.Write(events.KindImportCompleted, machineID, detail); err != nil {
		s.app.Logger().Warn("写 import.completed 事件失败", "error", err)
	}
	return nil
}

// Findings 对草稿的全部内容跑一遍敏感项检测。
func (s *Service) Findings(setID string) ([]Finding, error) {
	draft, err := s.sets.Draft(setID)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, f := range draft {
		content, err := s.blobs.Get(f.Hash)
		if err != nil {
			continue
		}
		out = append(out, Scan(f.Path, content)...)
	}
	return out, nil
}

// Extract 把某处的值抽成凭据：建凭据 → 把该文件里**每一处**该值替成占位符
// → 重写草稿。
//
// 「每一处」是要紧的：同一个 key 常常同时出现在 env 与某段说明文字里，
// 漏一处就等于没脱敏，而 Revision 是不可变的，写进去就洗不掉（spec §1.4）。
func (s *Service) Extract(setID, path, location, credName string) error {
	draft, err := s.sets.Draft(setID)
	if err != nil {
		return err
	}
	var entry *protocol.FileEntry
	for i := range draft {
		if draft[i].Path == path {
			entry = &draft[i]
			break
		}
	}
	if entry == nil {
		return fmt.Errorf("importer: 草稿里没有 %s", path)
	}
	content, err := s.blobs.Get(entry.Hash)
	if err != nil {
		return err
	}

	value := gjson.GetBytes(content, location).String()
	if value == "" {
		return fmt.Errorf("importer: %s 的 %s 取不到值", path, location)
	}
	if len(value) < credentials.MinValueLen {
		return fmt.Errorf("importer: %s 的值只有 %d 个字符，短于 %d，"+
			"抽成凭据后还原时会到处误匹配；请保留明文或改用变量",
			location, len(value), credentials.MinValueLen)
	}

	if _, err := s.creds.Create(credName, value, "由导入向导从 "+path+" 抽取"); err != nil {
		return err
	}

	replaced := strings.ReplaceAll(string(content), value,
		"{{cred."+credName+"}}")
	if _, err := s.sets.SetDraftFile(setID, path, []byte(replaced), entry.Mode, entry.Keys); err != nil {
		return err
	}
	return nil
}

// keysFor 给 .claude.json 补上 keys 模式的受管键（spec §3.1）。
func keysFor(path string) []string {
	if strings.HasSuffix(path, ".claude.json") && !strings.Contains(path, "/") {
		return []string{"mcpServers"}
	}
	return nil
}

func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("importer: 生成采集 token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// uniqueName 在重名时追加后缀。配置集名有唯一索引，重复导入不该直接失败。
func uniqueName(app core.App, base string) string {
	name := base
	for i := 2; i < 100; i++ {
		if r, _ := app.FindFirstRecordByData("config_sets", "name", name); r == nil {
			return name
		}
		name = fmt.Sprintf("%s %d", base, i)
	}
	return base
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/importer/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/internal/importer/
git commit -m "feat: 导入采集编排与凭据抽取"
```

---

### Task 4: 接线与端到端导入测试

**Files:**
- Modify: `hub/hub.go`（构造 `importer.Service`）
- Modify: `hub/internal/configsync/service.go`（`CollectResult` 转交）
- Modify: `hub/api.go`（加 `StartImport` / `ImportFindings` / `ExtractCredential`）
- Create: `internal/testsupport/import_test.go`

**Interfaces:**
- Produces:
  ```go
  // configsync.Deps 追加
  Importer interface {
      HandleResult(machineID string, r protocol.CollectResult) error
  }

  // Hub
  func (h *Hub) StartImport(machineID string) (token, setID string, err error)
  func (h *Hub) ImportFindings(setID string) ([]importer.Finding, error)
  func (h *Hub) ExtractCredential(setID, path, location, name string) error
  ```

- [ ] **Step 1: 写端到端测试**

Create `internal/testsupport/import_test.go`：

```go
//go:build testing

package testsupport_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/internal/testsupport"
)

// DoD 第 1 条：导入向导 → 配置集 v1；敏感项被抽成凭据；
// 查库确认 blob 里不含任何明文密钥。
func TestImportWizardEndToEnd(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th, t.TempDir())
	secret := "sk-ant-abcdefghijklmnopqrst"

	ta.WriteManaged(t, ".claude/settings.json",
		[]byte(`{"model":"opus","env":{"ANTHROPIC_AUTH_TOKEN":"`+secret+`"}}`), 0o600)
	ta.WriteManaged(t, ".claude/CLAUDE.md", []byte("# 我的规矩\n"), 0o644)
	ta.WriteManaged(t, ".claude/skills/foo/SKILL.md", []byte("skill 内容\n"), 0o644)
	// 恒排除的东西绝不该上来
	ta.WriteManaged(t, ".claude/.credentials.json", []byte(`{"oauth":"绝密"}`), 0o600)

	ta.RunSync(t, th)

	_, setID, err := th.Hub.StartImport(ta.MachineID)
	require.NoError(t, err)

	// 采集是异步的：等草稿被填满
	require.Eventually(t, func() bool {
		set, err := th.App.FindRecordById("config_sets", setID)
		if err != nil {
			return false
		}
		var draft []map[string]any
		_ = set.UnmarshalJSONField("draft", &draft)
		return len(draft) >= 3
	}, 3*time.Second, 20*time.Millisecond, "采集结果未落进草稿")

	// 敏感项被检出
	found, err := th.Hub.ImportFindings(setID)
	require.NoError(t, err)
	require.NotEmpty(t, found)

	require.NoError(t, th.Hub.ExtractCredential(setID,
		".claude/settings.json", "env.ANTHROPIC_AUTH_TOKEN", "anthropic_auth_token"))

	// 发布并指派——没有捷径，走的就是正常路径（spec §9.1 第 7 步）
	revID, err := th.Hub.PublishConfigSet(setID, "导入")
	require.NoError(t, err)
	require.NotEmpty(t, revID)

	th.Assign(t, ta.MachineID, setID, configsets.ModeApply)
	th.RequireAssignmentState(t, ta.MachineID, configsets.StateAligned)

	// 落回磁盘的仍是真值
	require.Contains(t, string(ta.ReadManaged(t, ".claude/settings.json")), secret)
	// 但库里查不到明文
	th.RequireNoPlaintextInBlobs(t, secret)
	th.RequireNoPlaintextInBlobs(t, "绝密")

	// 恒排除的文件既没被采集，也没被 apply 动过
	require.Equal(t, `{"oauth":"绝密"}`, string(ta.ReadManaged(t, ".claude/.credentials.json")))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./internal/testsupport/ -run ImportWizard -v`
Expected: FAIL，`th.Hub.StartImport undefined`

- [ ] **Step 3: 实现接线**

`configsync.Deps` 追加：

```go
	// Importer 处理采集结果。configsync 只做转交——把它做成接口而不是
	// 直接依赖 importer 包，是因为 importer 也要 Sender，直接互相 import 会成环。
	Importer interface {
		HandleResult(machineID string, r protocol.CollectResult) error
	}
```

`configsync.Service.CollectResult` 改成：

```go
func (s *Service) CollectResult(machineID string, r protocol.CollectResult) {
	if s.d.Importer == nil {
		s.log.Debug("未装配导入服务，忽略采集结果", "machine", machineID)
		return
	}
	if err := s.d.Importer.HandleResult(machineID, r); err != nil {
		s.log.Warn("处理采集结果失败", "machine", machineID, "error", err)
	}
}
```

`hub/hub.go` 里在 `configsync.NewService` 之前构造 importer，之后把它塞进去。因为两者互相需要，用「先建 importer（它要 Sender = h.machines），再建 configsync（它要 Importer）」的顺序即可，不存在真正的循环：

```go
		h.importer = importer.NewService(e.App, h.blobs, h.sets, h.creds, h.events, h.machines)
		h.sync = configsync.NewService(configsync.Deps{
			App: e.App, Blobs: h.blobs, Sets: h.sets, Revs: h.revs,
			Creds: h.creds, Events: h.events, Sender: h.machines,
			Importer: h.importer, Logger: e.App.Logger(),
		})
```

`hub/api.go` 追加：

```go
// StartImport 触发一次导入采集，返回采集 token 与新建的草稿配置集 id。
func (h *Hub) StartImport(machineID string) (string, string, error) {
	return h.importer.Start(machineID)
}

// ImportFindings 对草稿跑一遍敏感项检测。
func (h *Hub) ImportFindings(setID string) ([]importer.Finding, error) {
	return h.importer.Findings(setID)
}

// ExtractCredential 把草稿里某处的值抽成凭据。
func (h *Hub) ExtractCredential(setID, path, location, name string) error {
	return h.importer.Extract(setID, path, location, name)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/ internal/testsupport/
git commit -m "feat: 导入向导端到端接线"
```
