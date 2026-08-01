package watcher

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

// Run 是 watcher 的主循环：fsnotify 事件与对账 ticker 在同一个 select 里，
// 一条 goroutine 管到底。
//
// 三个时长全部走注入的时钟：去抖 2 秒、对账 5 分钟、节流 30 秒。真睡的话
// 这个包的测试要跑几分钟（spec §10.1）。
func (w *Watcher) Run(ctx context.Context) error {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("watcher: 建立文件监视: %w", err)
	}
	defer fsw.Close()

	if err := w.addWatches(fsw); err != nil {
		// 监视建不起来不是致命的：定时对账仍然兜得住，
		// 只是发现漂移会慢到 5 分钟以内。
		w.log.Warn("建立文件监视失败，退化为仅定时对账", "error", err)
	}

	ticker := w.clk.NewTicker(w.reconcile)
	defer ticker.Stop()

	var debounce clock.Timer
	var debounceC <-chan time.Time

	armDebounce := func() {
		if debounce != nil {
			debounce.Stop()
		}
		debounce = w.clk.NewTimer(w.debounce)
		debounceC = debounce.C()
	}

	for {
		select {
		case <-ctx.Done():
			if debounce != nil {
				debounce.Stop()
			}
			return nil

		case ev, ok := <-fsw.Events:
			if !ok {
				return nil
			}
			if !w.interested(ev.Name) {
				continue // 噪音在内存里丢掉，不产生 IO
			}
			// 去抖：编辑器保存往往触发多个事件（spec §8.1）
			armDebounce()

		case err, ok := <-fsw.Errors:
			if !ok {
				return nil
			}
			w.log.Warn("文件监视报错", "error", err)

		case <-debounceC:
			debounceC = nil
			w.reportScan(false)

		case <-ticker.C():
			// 定时全量对账兜底 fsnotify 漏事件：网络文件系统、
			// 容器 bind mount、inotify 队列溢出（spec §8.1）
			w.reportScan(true)

		case <-w.manual:
			// Notify 注入的事件（测试与 CLI 的 drift 子命令）
			armDebounce()
		}
	}
}

// Notify 注入一次「路径可能变了」的信号，供测试与 CLI 使用。
// 非阻塞：已有待处理信号时直接丢掉，去抖会把它们合并。
func (w *Watcher) Notify(path string) {
	_ = path // 路径信息目前只用于触发全量扫描；保留参数方便将来做定向扫描
	select {
	case w.manual <- struct{}{}:
	default:
	}
}

// interested 报告某个路径是否值得触发一次扫描。
//
// .claude.json 在 HOME 根下，因此必须监视整个 HOME 目录——但那里什么都有。
// 只对这一个文件名响应，其余事件在内存里丢掉，不产生任何 IO（spec §8.1）。
func (w *Watcher) interested(abs string) bool {
	rel, err := filepath.Rel(w.o.ManagedHome, abs)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "../") || rel == ".." {
		return false
	}
	if !strings.Contains(rel, "/") {
		// HOME 根下：只认 .claude.json
		return rel == ".claude.json"
	}
	if manifest.IsAlwaysExcluded(rel) {
		return false
	}
	// 目录事件（新建子目录）也值得响应——tree 模式要靠它发现新增。
	return true
}

// addWatches 把 manifest 展开后会碰到的目录挂上 fsnotify。
//
// .claude.json 在 HOME 根，所以 HOME 本身必须监视；其余只监视
// include 覆盖到的目录，避免把整个家目录的噪音都接进来。
func (w *Watcher) addWatches(fsw *fsnotify.Watcher) error {
	w.mu.Lock()
	m := w.manifest
	home := w.o.ManagedHome
	w.mu.Unlock()

	// HOME 根：给 .claude.json 用。
	if err := fsw.Add(home); err != nil {
		return fmt.Errorf("监视 %s: %w", home, err)
	}

	seen := map[string]bool{home: true}
	addDir := func(dir string) {
		if seen[dir] {
			return
		}
		seen[dir] = true
		// 目录尚不存在时 Add 会失败；忽略，定时对账仍兜得住，
		// 且父目录的 create 事件会间接触发下一次扫描。
		_ = fsw.Add(dir)
	}

	for _, inc := range m.Include {
		switch inc.Mode {
		case manifest.ModeFile, manifest.ModeKeys:
			dir := filepath.Join(home, filepath.FromSlash(filepath.Dir(inc.Path)))
			addDir(dir)
		case manifest.ModeTree:
			base := strings.TrimSuffix(inc.Path, "/**")
			dir := filepath.Join(home, filepath.FromSlash(base))
			addDir(dir)
			// 已有子目录也挂上，否则深层改动靠不到。
			_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d == nil || !d.IsDir() {
					return nil
				}
				addDir(p)
				return nil
			})
		}
	}
	return nil
}

func (w *Watcher) reportScan(full bool) {
	w.mu.Lock()
	paused := w.st != nil && w.st.Paused
	w.mu.Unlock()
	if paused {
		return
	}

	items, err := w.Scan(full)
	if err != nil {
		w.log.Warn("对账失败", "error", err)
		return
	}
	items = w.throttle(items, full)
	if len(items) == 0 {
		return // 没漂移就不发空消息：每 5 分钟一条纯噪音
	}
	if err := w.report(items, full); err != nil {
		w.log.Warn("上报漂移失败", "error", err)
		return
	}
	w.markReported(items)
}

// throttle 滤掉 30 秒内已上报过的路径。
//
// 全量对账（full）不受节流影响：它是兜底路径，且 hub 侧靠部分唯一索引
// 做去重——同一路径重复检测到就更新那条，不新建（spec §4.1）。
func (w *Watcher) throttle(items []protocol.DriftItem, full bool) []protocol.DriftItem {
	if full {
		return items
	}
	now := w.clk.Now()
	out := make([]protocol.DriftItem, 0, len(items))
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, it := range items {
		if last, ok := w.lastReported[it.Path]; ok && now.Sub(last) < w.throttleWindow {
			continue
		}
		out = append(out, it)
	}
	return out
}

func (w *Watcher) markReported(items []protocol.DriftItem) {
	now := w.clk.Now()
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, it := range items {
		w.lastReported[it.Path] = now
	}
}
