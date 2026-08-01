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
