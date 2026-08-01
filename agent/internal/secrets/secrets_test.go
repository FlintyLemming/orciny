package secrets_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestSaveLoadRoundTripAndPerm(t *testing.T) {
	dir := t.TempDir()
	want := &secrets.File{
		Creds:   map[string]string{"k": "sk-value-1234"},
		Vars:    map[string]string{"ws": "main"},
		Machine: map[string]string{"hostname": "mac", "os": "darwin"},
	}
	require.NoError(t, secrets.Save(dir, want))

	info, err := os.Stat(secrets.Path(dir))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "凭据缓存必须 0600")

	got, err := secrets.Load(dir)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// 首次运行时文件还不存在，这不是错误。
func TestLoadMissingReturnsEmpty(t *testing.T) {
	f, err := secrets.Load(t.TempDir())
	require.NoError(t, err)
	require.Empty(t, f.Creds)
	require.Empty(t, f.Vars)
	require.Empty(t, f.Machine)
}

func TestLookup(t *testing.T) {
	f := &secrets.File{
		Creds:   map[string]string{"k": "sk-value"},
		Vars:    map[string]string{"ws": "main"},
		Machine: map[string]string{"hostname": "mac"},
	}
	v, ok := f.Lookup(protocol.Ref{Kind: protocol.RefCred, Name: "k"})
	require.True(t, ok)
	require.Equal(t, "sk-value", v)

	v, ok = f.Lookup(protocol.Ref{Kind: protocol.RefVar, Name: "ws"})
	require.True(t, ok)
	require.Equal(t, "main", v)

	v, ok = f.Lookup(protocol.Ref{Kind: protocol.RefMachine, Name: "hostname"})
	require.True(t, ok)
	require.Equal(t, "mac", v)

	_, ok = f.Lookup(protocol.Ref{Kind: protocol.RefCred, Name: "gone"})
	require.False(t, ok)
}

// 轮换检测靠它：拉回来的快照 revision 没变但 secrets 变了，
// 就只重渲染受影响的文件（spec §5.1）。
func TestEqual(t *testing.T) {
	a := &secrets.File{Creds: map[string]string{"k": "v"}, Vars: map[string]string{"w": "1"}}
	b := &secrets.File{Creds: map[string]string{"k": "v"}, Vars: map[string]string{"w": "1"}}
	require.True(t, a.Equal(b))

	c := &secrets.File{Creds: map[string]string{"k": "v2"}, Vars: map[string]string{"w": "1"}}
	require.False(t, a.Equal(c))

	require.False(t, a.Equal(nil))
	require.True(t, (*secrets.File)(nil).Equal(nil))
}
