package secrets_test

import (
	"os"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestSaveLoadRoundTripAndPerm(t *testing.T) {
	dir := t.TempDir()
	want := &secrets.File{
		Vars:     map[string]string{"ws": "main"},
		Machine:  map[string]string{"hostname": "mac", "os": "darwin"},
		Provider: map[string]string{"claude.base_url": "https://example.test"},
	}
	require.NoError(t, secrets.Save(dir, want))

	info, err := os.Stat(secrets.Path(dir))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "秘密缓存必须 0600")

	got, err := secrets.Load(dir)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// 首次运行时文件还不存在，这不是错误。
func TestLoadMissingReturnsEmpty(t *testing.T) {
	f, err := secrets.Load(t.TempDir())
	require.NoError(t, err)
	require.Empty(t, f.Vars)
	require.Empty(t, f.Machine)
}

func TestLookup(t *testing.T) {
	f := &secrets.File{
		Vars:     map[string]string{"ws": "main"},
		Machine:  map[string]string{"hostname": "mac"},
		Provider: map[string]string{"claude.auth_token": "sk-value"},
	}
	v, ok := f.Lookup(protocol.Ref{Kind: protocol.RefProvider, Name: "claude.auth_token"})
	require.True(t, ok)
	require.Equal(t, "sk-value", v)

	v, ok = f.Lookup(protocol.Ref{Kind: protocol.RefVar, Name: "ws"})
	require.True(t, ok)
	require.Equal(t, "main", v)

	v, ok = f.Lookup(protocol.Ref{Kind: protocol.RefMachine, Name: "hostname"})
	require.True(t, ok)
	require.Equal(t, "mac", v)

	_, ok = f.Lookup(protocol.Ref{Kind: protocol.RefVar, Name: "gone"})
	require.False(t, ok)
}

// 轮换检测靠它：拉回来的快照 revision 没变但 secrets 变了，
// 就只重渲染受影响的文件（spec §5.1）。
func TestEqual(t *testing.T) {
	a := &secrets.File{
		Provider: map[string]string{"claude.auth_token": "v"},
		Vars:     map[string]string{"w": "1"},
	}
	b := &secrets.File{
		Provider: map[string]string{"claude.auth_token": "v"},
		Vars:     map[string]string{"w": "1"},
	}
	require.True(t, a.Equal(b))

	c := &secrets.File{
		Provider: map[string]string{"claude.auth_token": "v2"},
		Vars:     map[string]string{"w": "1"},
	}
	require.False(t, a.Equal(c))

	require.False(t, a.Equal(nil))
	require.True(t, (*secrets.File)(nil).Equal(nil))
}

func TestLookupProvider(t *testing.T) {
	f := &secrets.File{
		Provider: map[string]string{
			"claude.base_url": "https://open.bigmodel.cn/api/anthropic",
			"claude.model":    "glm-5.1",
		},
	}
	v, ok := f.Lookup(protocol.Ref{Kind: protocol.RefProvider, Name: "claude.base_url"})
	require.True(t, ok)
	require.Equal(t, "https://open.bigmodel.cn/api/anthropic", v)

	// 没下发的键必须返回 false——渲染要因此整体失败，
	// 而不是把空串写进 settings.json（M1 spec §6.1）。
	_, ok = f.Lookup(protocol.Ref{Kind: protocol.RefProvider, Name: "claude.model_opus"})
	require.False(t, ok)
}

func TestSaveLoadKeepsProvider(t *testing.T) {
	dir := t.TempDir()
	want := &secrets.File{
		Vars:    map[string]string{"ws": "main"},
		Machine: map[string]string{"hostname": "mac"},
		Provider: map[string]string{
			"claude.base_url": "https://example.test", "claude.model": "glm-5.1",
		},
	}
	require.NoError(t, secrets.Save(dir, want))
	got, err := secrets.Load(dir)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestLoadMissingReturnsEmptyProvider(t *testing.T) {
	f, err := secrets.Load(t.TempDir())
	require.NoError(t, err)
	require.Empty(t, f.Provider)
	require.NotNil(t, f.Provider)
}

func TestEqualComparesProvider(t *testing.T) {
	a := &secrets.File{Provider: map[string]string{"model": "glm-5.1"}}
	b := &secrets.File{Provider: map[string]string{"model": "glm-4.7"}}
	require.False(t, a.Equal(b))
	require.True(t, a.Equal(&secrets.File{Provider: map[string]string{"model": "glm-5.1"}}))
}

func TestFileHasNoCredsField(t *testing.T) {
	typ := reflect.TypeOf(secrets.File{})
	_, ok := typ.FieldByName("Creds")
	require.False(t, ok, "Creds 字段随 {{cred.*}} 一起删除")
}

// 老 agent 写下的 secrets.json 里多一个 creds 键，读回来忽略即可，不能报错。
func TestLoadIgnoresLegacyCredsKey(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(secrets.Path(dir), []byte(`{
		"creds":{"zhipu":"sk-old"},
		"vars":{"workspace":"main"},
		"provider":{"claude.base_url":"https://x.example"}
	}`), 0o600))

	f, err := secrets.Load(dir)
	require.NoError(t, err)
	require.Equal(t, "main", f.Vars["workspace"])
	require.Equal(t, "https://x.example", f.Provider["claude.base_url"])
}

func TestLookupResolvesEndpointQualifiedProviderKeys(t *testing.T) {
	f := &secrets.File{Provider: map[string]string{
		"claude.auth_token": "sk-claude-abcdef123456",
		"openai.api_key":    "sk-openai-zyxwvu654321",
	}}
	v, ok := f.Lookup(protocol.Ref{Kind: protocol.RefProvider, Name: "openai.api_key"})
	require.True(t, ok)
	require.Equal(t, "sk-openai-zyxwvu654321", v)
}

// 保存出来的 JSON 里不许再有 creds 键。
func TestSaveWritesNoCredsKey(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, secrets.Save(dir, &secrets.File{
		Vars: map[string]string{"workspace": "main"},
	}))
	b, err := os.ReadFile(secrets.Path(dir))
	require.NoError(t, err)
	require.NotContains(t, string(b), `"creds"`)
}
