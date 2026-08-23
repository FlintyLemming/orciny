package syncer_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/agent/internal/syncer"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

type outbox struct {
	mu   sync.Mutex
	msgs []struct {
		kind    protocol.Kind
		payload any
	}
}

func (o *outbox) send(kind protocol.Kind, payload any) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.msgs = append(o.msgs, struct {
		kind    protocol.Kind
		payload any
	}{kind, payload})
	return nil
}

func (o *outbox) of(kind protocol.Kind) []any {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []any
	for _, m := range o.msgs {
		if m.kind == kind {
			out = append(out, m.payload)
		}
	}
	return out
}

type rig struct {
	dir  string
	home string
	out  *outbox
	s    *syncer.Syncer
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{dir: t.TempDir(), home: t.TempDir(), out: &outbox{}}
	s, err := syncer.New(syncer.Deps{
		Dir:         r.dir,
		ManagedHome: r.home,
		Clock:       clock.NewFake(time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)),
		Send:        r.out.send,
		MachineName: "主力",
	})
	require.NoError(t, err)
	r.s = s
	return r
}

func envelope(t *testing.T, kind protocol.Kind, payload any) protocol.Envelope {
	t.Helper()
	b, err := protocol.Encode(kind, nil, payload)
	require.NoError(t, err)
	env, err := protocol.Decode(b)
	require.NoError(t, err)
	return env
}

func snapshotOf(t *testing.T, files map[string]string) (protocol.ConfigSnapshot, map[string][]byte) {
	t.Helper()
	mj, err := manifest.Default().JSON()
	require.NoError(t, err)
	snap := protocol.ConfigSnapshot{
		ConfigSetID: "set1", RevisionID: "rev1", Seq: 1, Manifest: mj,
	}
	blobs := map[string][]byte{}
	for rel, content := range files {
		h := blobcache.Hash([]byte(content))
		snap.Files = append(snap.Files, protocol.FileEntry{
			Path: rel, Hash: h, Size: uint32(len(content)), Mode: 0o644,
		})
		blobs[h] = []byte(content)
	}
	snap.Checksum = protocol.Checksum(snap.Files)
	return snap, blobs
}

func TestNotifyTriggersPull(t *testing.T) {
	r := newRig(t)
	r.s.Handle(envelope(t, protocol.KindConfigNotify,
		protocol.ConfigNotify{ConfigSetID: "set1", RevisionID: "rev1"}))

	pulls := r.out.of(protocol.KindConfigPull)
	require.Len(t, pulls, 1)
}

// 本地什么都没有 → 先要 blob，不能直接 apply。
func TestSnapshotRequestsMissingBlobs(t *testing.T) {
	r := newRig(t)
	snap, _ := snapshotOf(t, map[string]string{".claude/CLAUDE.md": "内容"})
	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))

	reqs := r.out.of(protocol.KindBlobRequest)
	require.Len(t, reqs, 1)
	require.Equal(t, []string{snap.Files[0].Hash}, reqs[0].(protocol.BlobRequest).Hashes)
	require.Empty(t, r.out.of(protocol.KindApplyAck), "内容没齐不能 apply")
	require.NoFileExists(t, filepath.Join(r.home, ".claude/CLAUDE.md"))
}

func TestBlobDataCompletesApply(t *testing.T) {
	r := newRig(t)
	snap, blobs := snapshotOf(t, map[string]string{".claude/CLAUDE.md": "内容"})
	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))

	for h, content := range blobs {
		r.s.Handle(envelope(t, protocol.KindBlobData, protocol.BlobData{Hash: h, Content: content}))
	}

	acks := r.out.of(protocol.KindApplyAck)
	require.Len(t, acks, 1)
	require.True(t, acks[0].(protocol.ApplyAck).OK)

	got, err := os.ReadFile(filepath.Join(r.home, ".claude/CLAUDE.md"))
	require.NoError(t, err)
	require.Equal(t, "内容", string(got))

	st, err := state.Load(r.dir)
	require.NoError(t, err)
	require.Equal(t, "rev1", st.Revision)
	require.Equal(t, snap.Checksum, st.Checksum)
}

// 缓存命中就不再向 hub 要内容。
func TestSnapshotUsesCacheWhenAvailable(t *testing.T) {
	r := newRig(t)
	snap, blobs := snapshotOf(t, map[string]string{".claude/CLAUDE.md": "内容"})
	cache := blobcache.New(r.dir)
	for _, content := range blobs {
		_, err := cache.Put(content)
		require.NoError(t, err)
	}

	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))
	require.Empty(t, r.out.of(protocol.KindBlobRequest), "本地已有就不该再要")
	require.Len(t, r.out.of(protocol.KindApplyAck), 1)
}

// 秘密与变量随快照落进 secrets.json，供离线自愈与还原使用（spec §6.2）。
func TestSecretsArePersisted(t *testing.T) {
	r := newRig(t)
	snap, blobs := snapshotOf(t, map[string]string{
		".claude/settings.json": `{"K":"{{provider.claude.auth_token}}"}`,
	})
	snap.Provider = map[string]string{"claude.auth_token": "sk-real-1234"}
	snap.Variables = map[string]string{"ws": "main"}

	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))
	for h, content := range blobs {
		r.s.Handle(envelope(t, protocol.KindBlobData, protocol.BlobData{Hash: h, Content: content}))
	}

	sec, err := secrets.Load(r.dir)
	require.NoError(t, err)
	require.Equal(t, "sk-real-1234", sec.Provider["claude.auth_token"])
	require.Equal(t, "main", sec.Vars["ws"])
	require.Equal(t, "主力", sec.Machine["name"])
	require.NotEmpty(t, sec.Machine["hostname"])
	require.NotEmpty(t, sec.Machine["os"])
	require.NotEmpty(t, sec.Machine["arch"])

	info, err := os.Stat(secrets.Path(r.dir))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// hub 说找不到 blob → 中止本次 apply，不写任何文件（spec §5.2）。
func TestMissingBlobAbortsApply(t *testing.T) {
	r := newRig(t)
	snap, _ := snapshotOf(t, map[string]string{".claude/CLAUDE.md": "内容"})
	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))
	r.s.Handle(envelope(t, protocol.KindBlobData,
		protocol.BlobData{Hash: snap.Files[0].Hash, Missing: true}))

	acks := r.out.of(protocol.KindApplyAck)
	require.Len(t, acks, 1)
	ack := acks[0].(protocol.ApplyAck)
	require.False(t, ack.OK)
	require.Contains(t, ack.Error, "内容")
	require.NoFileExists(t, filepath.Join(r.home, ".claude/CLAUDE.md"))
}

// survey 模式不写盘（spec §7.6）。落盘的部分在子计划 15 补全对账上报。
func TestSurveyModeDoesNotWrite(t *testing.T) {
	r := newRig(t)
	snap, blobs := snapshotOf(t, map[string]string{".claude/CLAUDE.md": "内容"})
	snap.Mode = protocol.ModeSurvey
	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))
	for h, content := range blobs {
		r.s.Handle(envelope(t, protocol.KindBlobData, protocol.BlobData{Hash: h, Content: content}))
	}
	require.NoFileExists(t, filepath.Join(r.home, ".claude/CLAUDE.md"))
	require.Empty(t, r.out.of(protocol.KindApplyAck), "survey 不产生 apply 回执")
}

// 重复下发同一版本：第二次零写入（spec §7.3 的幂等）。
func TestRepeatedSnapshotIsIdempotent(t *testing.T) {
	r := newRig(t)
	snap, blobs := snapshotOf(t, map[string]string{".claude/CLAUDE.md": "内容"})
	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))
	for h, content := range blobs {
		r.s.Handle(envelope(t, protocol.KindBlobData, protocol.BlobData{Hash: h, Content: content}))
	}
	first, err := os.Stat(filepath.Join(r.home, ".claude/CLAUDE.md"))
	require.NoError(t, err)

	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))
	acks := r.out.of(protocol.KindApplyAck)
	require.Len(t, acks, 2)
	require.True(t, acks[1].(protocol.ApplyAck).OK)

	after, err := os.Stat(filepath.Join(r.home, ".claude/CLAUDE.md"))
	require.NoError(t, err)
	require.Equal(t, first.ModTime(), after.ModTime(), "第二次不该碰文件")
}

func writeManaged(t *testing.T, r *rig, rel, content string) {
	t.Helper()
	p := filepath.Join(r.home, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

// 快照带来的 provider 值必须落进 secrets.json，否则渲染取不到值、
// 整份 apply 失败（M1 spec §6.1）。
func TestSnapshotProviderLandsInSecrets(t *testing.T) {
	r := newRig(t)
	snap, blobs := snapshotOf(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}",` +
			`"ANTHROPIC_MODEL":"{{provider.claude.model}}"}}`,
	})
	snap.Provider = map[string]string{
		"claude.base_url": "https://open.bigmodel.cn/api/anthropic",
		"claude.model":    "glm-5.1",
	}

	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))
	for h, content := range blobs {
		r.s.Handle(envelope(t, protocol.KindBlobData, protocol.BlobData{Hash: h, Content: content}))
	}

	acks := r.out.of(protocol.KindApplyAck)
	require.Len(t, acks, 1)
	ack, ok := acks[0].(protocol.ApplyAck)
	require.True(t, ok)
	require.True(t, ack.OK, "apply 必须成功：%s", ack.Error)

	sec, err := secrets.Load(r.dir)
	require.NoError(t, err)
	require.Equal(t, snap.Provider, sec.Provider)

	got, err := os.ReadFile(filepath.Join(r.home, ".claude", "settings.json"))
	require.NoError(t, err)
	require.Contains(t, string(got), "https://open.bigmodel.cn/api/anthropic")
	require.Contains(t, string(got), "glm-5.1")
	// 渲染绝不能把字面占位符落给 Claude Code。
	require.NotContains(t, string(got), "{{provider.")
}
