// Package watcher 监视受管目录的漂移：fsnotify + 去抖 + 定时全量对账 + 上报节流。
//
// 比对口径只比 rendered hash（spec §6.3）。keys 模式只比受管键子树的规范化
// JSON hash——用户或 Claude Code 改动其他键完全不算漂移（spec §7.5）。
package watcher

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/tidwall/gjson"

	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
	"github.com/FlintyLemming/orciny/agent/internal/render"
	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

const (
	defaultDebounce  = 2 * time.Second
	defaultReconcile = 5 * time.Minute
	defaultThrottle  = 30 * time.Second
)

// Options 配置一个 Watcher。
type Options struct {
	Dir         string
	ManagedHome string
	Clock       clock.Clock
	Debounce    time.Duration // 0 → 2s
	Reconcile   time.Duration // 0 → 5m
	Throttle    time.Duration // 0 → 30s
	Report      func(items []protocol.DriftItem, full bool) error
	Logger      *slog.Logger
}

// Watcher 持有当前基线与 secrets，对磁盘做对账扫描，并可跑 fsnotify 主循环。
type Watcher struct {
	o       Options
	clk     clock.Clock
	log     *slog.Logger
	cache   *blobcache.Cache
	report  func(items []protocol.DriftItem, full bool) error
	debounce      time.Duration
	reconcile     time.Duration
	throttleWindow time.Duration

	mu           sync.Mutex
	st           *state.State
	manifest     manifest.Manifest
	sec          *secrets.File
	lastReported map[string]time.Time

	// manual 接收 Notify 注入的事件（测试与 CLI）。容量 1，非阻塞。
	manual chan struct{}
}

// New 构造 Watcher。调用 Reload 装载基线后才能 Scan。
func New(o Options) (*Watcher, error) {
	if o.ManagedHome == "" {
		return nil, fmt.Errorf("watcher: ManagedHome 不能为空")
	}
	if o.Dir == "" {
		return nil, fmt.Errorf("watcher: Dir 不能为空")
	}
	clk := o.Clock
	if clk == nil {
		clk = clock.System()
	}
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	report := o.Report
	if report == nil {
		report = func([]protocol.DriftItem, bool) error { return nil }
	}
	deb := o.Debounce
	if deb <= 0 {
		deb = defaultDebounce
	}
	rec := o.Reconcile
	if rec <= 0 {
		rec = defaultReconcile
	}
	thr := o.Throttle
	if thr <= 0 {
		thr = defaultThrottle
	}
	return &Watcher{
		o:              o,
		clk:            clk,
		log:            log,
		cache:          blobcache.New(o.Dir),
		report:         report,
		debounce:       deb,
		reconcile:      rec,
		throttleWindow: thr,
		lastReported:   map[string]time.Time{},
		manual:         make(chan struct{}, 1),
	}, nil
}

// Reload 换掉基线、manifest 与 secrets，并清空节流记忆。
//
// apply 成功、凭据轮换、pause/resume、ignore 变更后都要调它。
func (w *Watcher) Reload(st *state.State, m manifest.Manifest, sec *secrets.File) error {
	if st == nil {
		return fmt.Errorf("watcher: state 不能为 nil")
	}
	if sec == nil {
		sec = &secrets.File{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.st = cloneState(st)
	w.manifest = m
	w.sec = cloneSecrets(sec)
	// 基线变了，节流窗口作废——新基线上的同路径是新一轮漂移。
	w.lastReported = map[string]time.Time{}
	return nil
}

// Scan 对账一次。full 目前只影响调用方语义（定时对账 vs 事件触发），
// 扫描本身始终是全量的——partial 扫描省不了多少，还容易漏。
func (w *Watcher) Scan(full bool) ([]protocol.DriftItem, error) {
	_ = full
	w.mu.Lock()
	st := w.st
	m := w.manifest
	sec := w.sec
	home := w.o.ManagedHome
	w.mu.Unlock()

	if st == nil {
		return nil, fmt.Errorf("watcher: 尚未 Reload 基线")
	}

	exp, err := m.Expand(home)
	if err != nil {
		return nil, fmt.Errorf("watcher: 展开 manifest: %w", err)
	}

	seen := map[string]bool{}
	var items []protocol.DriftItem

	// 超限文件：Expand 放进 Skipped，这里转成 Truncated 漂移项。
	for _, sk := range exp.Skipped {
		if sk.Reason != protocol.SkipTooLarge {
			continue
		}
		if ignored(st.Ignored, sk.Rel) {
			continue
		}
		seen[sk.Rel] = true
		it := protocol.DriftItem{
			Path:      sk.Rel,
			Truncated: true,
		}
		if fs, ok := st.Files[sk.Rel]; ok {
			it.Kind = protocol.DriftModified
			it.BaseHash = fs.Rendered
			it.Mode = fs.Mode
		} else {
			it.Kind = protocol.DriftAdded
		}
		items = append(items, it)
	}

	for _, e := range exp.Files {
		if ignored(st.Ignored, e.Rel) {
			continue
		}
		seen[e.Rel] = true

		content, err := os.ReadFile(e.Abs)
		if err != nil {
			w.log.Warn("读受管文件失败", "path", e.Rel, "error", err)
			continue
		}

		// 二次超限保护：Expand 已拦过，但竞态下文件可能刚涨过上限。
		if len(content) > protocol.MaxFileSize {
			it := protocol.DriftItem{Path: e.Rel, Truncated: true, Mode: uint32(e.Mode.Perm())}
			if fs, ok := st.Files[e.Rel]; ok {
				it.Kind = protocol.DriftModified
				it.BaseHash = fs.Rendered
			} else {
				it.Kind = protocol.DriftAdded
			}
			items = append(items, it)
			continue
		}

		keys := e.Inc.Keys
		if fs, ok := st.Files[e.Rel]; ok && len(fs.Keys) > 0 {
			keys = fs.Keys
		}

		curHash := compareHash(content, keys)
		fs, inBase := st.Files[e.Rel]
		// 比对口径：优先用 Rendered（apply 后的真实基线）。
		// Rendered 为空时（survey / 状态丢失自愈）回退到 blob 渲染结果——
		// 磁盘从未被我们写过，但「与中台一致」仍然应该安静（spec §7.6）。
		// 填假 Rendered 会误判一致而漏报；真的一致则靠这条回退路径安静。
		baseHash := ""
		if inBase {
			baseHash = fs.Rendered
			if baseHash == "" && fs.Blob != "" {
				baseHash = expectedHash(w.cache, fs, sec, keys)
			}
		}
		if inBase && baseHash != "" && baseHash == curHash {
			continue // 与基线一致
		}

		it := protocol.DriftItem{
			Path: e.Rel,
			Mode: uint32(e.Mode.Perm()),
		}
		if inBase {
			it.Kind = protocol.DriftModified
			it.BaseHash = baseHash
		} else {
			it.Kind = protocol.DriftAdded
		}

		// 还原成占位符后再上报；Safe=false 时只报路径（spec §6.4）。
		var base []byte
		if inBase && fs.Blob != "" {
			if b, err := w.cache.Get(fs.Blob); err == nil {
				base = b
			}
		}
		creds, vars := mapsOf(sec)
		res := render.RestoreWithBase(content, base, creds, vars)
		it.RestorePartial = res.Partial
		if !res.Safe {
			it.Truncated = true
			// Content 故意留空
		} else {
			it.Content = res.Content
		}
		items = append(items, it)
	}

	// 基线里有、磁盘上已不存在 → deleted。
	for rel, fs := range st.Files {
		if seen[rel] || ignored(st.Ignored, rel) {
			continue
		}
		// 再确认一次：文件真的没了（而不是 Expand 漏掉）。
		abs := filepath.Join(home, filepath.FromSlash(rel))
		if _, err := os.Lstat(abs); err == nil {
			// 文件还在但不在 Expand 结果里——被 exclude / 恒排除 / 非受管了。
			// 不算 deleted，也不上报（范围外）。
			continue
		}
		items = append(items, protocol.DriftItem{
			Path:     rel,
			Kind:     protocol.DriftDeleted,
			BaseHash: fs.Rendered,
			Mode:     fs.Mode,
		})
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return items, nil
}

// CanonicalKeysHash 是 keys 模式的比对口径（spec §7.5）：
// 只取受管键子树，规范化（键排序、无空白）后取 hash。
//
// 用户或 Claude Code 改动其他键完全不算漂移——这正是 keys 模式存在的理由。
func CanonicalKeysHash(content []byte, keys []string) string {
	sub := map[string]any{}
	for _, k := range keys {
		v := gjson.GetBytes(content, k)
		if !v.Exists() {
			continue
		}
		var decoded any
		if err := json.Unmarshal([]byte(v.Raw), &decoded); err != nil {
			continue
		}
		sub[k] = decoded
	}
	// encoding/json 的 map 序列化按键排序，正是要的规范化。
	b, err := json.Marshal(sub)
	if err != nil {
		return ""
	}
	return blobcache.Hash(b)
}

func compareHash(content []byte, keys []string) string {
	if len(keys) > 0 {
		return CanonicalKeysHash(content, keys)
	}
	return blobcache.Hash(content)
}

// expectedHash 在 Rendered 为空时，用 blob 渲染结果推期望 hash。
// 渲染失败（缺凭据等）返回空串，调用方会保守地报 modified。
func expectedHash(cache *blobcache.Cache, fs state.FileState, sec *secrets.File, keys []string) string {
	raw, err := cache.Get(fs.Blob)
	if err != nil {
		return ""
	}
	look := func(r protocol.Ref) (string, bool) {
		if sec == nil {
			return "", false
		}
		return sec.Lookup(r)
	}
	rendered, err := render.Render(raw, look)
	if err != nil {
		return ""
	}
	return compareHash(rendered, keys)
}

func ignored(patterns []string, rel string) bool {
	for _, p := range patterns {
		if manifest.MatchGlob(p, rel) {
			return true
		}
	}
	return false
}

func mapsOf(sec *secrets.File) (creds, vars map[string]string) {
	if sec == nil {
		return nil, nil
	}
	return sec.Creds, sec.Vars
}

func cloneState(st *state.State) *state.State {
	c := *st
	c.Files = make(map[string]state.FileState, len(st.Files))
	for k, v := range st.Files {
		if v.Keys != nil {
			keys := make([]string, len(v.Keys))
			copy(keys, v.Keys)
			v.Keys = keys
		}
		c.Files[k] = v
	}
	if st.Ignored != nil {
		c.Ignored = append([]string(nil), st.Ignored...)
	}
	if st.Manifest != nil {
		c.Manifest = append(json.RawMessage(nil), st.Manifest...)
	}
	return &c
}

func cloneSecrets(sec *secrets.File) *secrets.File {
	out := &secrets.File{
		Creds:   copyMap(sec.Creds),
		Vars:    copyMap(sec.Vars),
		Machine: copyMap(sec.Machine),
	}
	return out
}

func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
