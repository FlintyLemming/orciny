package overrides_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/overrides"
)

func TestSplitDescendsIntoObjects(t *testing.T) {
	pts, err := overrides.Split(
		[]byte(`{"env":{"A":"1","B":"2"},"n":1}`),
		[]byte(`{"env":{"A":"9","B":"2"},"n":1}`))
	require.NoError(t, err)
	require.Equal(t, []overrides.Point{
		{Selector: "env.A", BaseValue: `"1"`, MineValue: `"9"`},
	}, pts)
}

func TestSplitTreatsArrayAsLeaf(t *testing.T) {
	pts, err := overrides.Split(
		[]byte(`{"allow":["a","b"]}`),
		[]byte(`{"allow":["a","b","c"]}`))
	require.NoError(t, err)
	require.Len(t, pts, 1)
	require.Equal(t, "allow", pts[0].Selector)
	require.Equal(t, `["a","b","c"]`, pts[0].MineValue)
}

func TestSplitTypeMismatchIsLeaf(t *testing.T) {
	pts, err := overrides.Split([]byte(`{"x":{"a":1}}`), []byte(`{"x":"字符串"}`))
	require.NoError(t, err)
	require.Len(t, pts, 1)
	require.Equal(t, "x", pts[0].Selector)
}

func TestSplitMissingKeyOnEitherSide(t *testing.T) {
	pts, err := overrides.Split([]byte(`{"a":1}`), []byte(`{"a":1,"b":2}`))
	require.NoError(t, err)
	require.Equal(t, []overrides.Point{
		{Selector: "b", BaseValue: "", MineValue: "2"},
	}, pts, "空串 = 该键在这一侧不存在")

	pts, err = overrides.Split([]byte(`{"a":1,"b":2}`), []byte(`{"a":1}`))
	require.NoError(t, err)
	require.Equal(t, []overrides.Point{
		{Selector: "b", BaseValue: "2", MineValue: ""},
	}, pts)
}

// 带 . * ? \ 的键是真实存在的（hook matcher、MCP server 名）。
// 不转义会让覆盖层写到错误的位置——静默写坏配置（spec §4.1）。
func TestSplitEscapesSelectorSegments(t *testing.T) {
	pts, err := overrides.Split(
		[]byte(`{"hooks":{"a.b":1,"c*d":1,"e?f":1,"g\\h":1}}`),
		[]byte(`{"hooks":{"a.b":2,"c*d":2,"e?f":2,"g\\h":2}}`))
	require.NoError(t, err)
	got := make([]string, 0, len(pts))
	for _, p := range pts {
		got = append(got, p.Selector)
	}
	require.ElementsMatch(t, []string{
		`hooks.a\.b`, `hooks.c\*d`, `hooks.e\?f`, `hooks.g\\h`,
	}, got)
}

func TestSplitIgnoresReindentation(t *testing.T) {
	pts, err := overrides.Split(
		[]byte("{\n  \"a\": {\n    \"b\": 1\n  }\n}"),
		[]byte(`{"a":{"b":1}}`))
	require.NoError(t, err)
	require.Empty(t, pts, "只是重新缩进，不是差异点")
}

func TestSplitStableOrder(t *testing.T) {
	pts, err := overrides.Split([]byte(`{}`), []byte(`{"z":1,"a":1,"m":1}`))
	require.NoError(t, err)
	require.Equal(t, []string{"a", "m", "z"},
		[]string{pts[0].Selector, pts[1].Selector, pts[2].Selector})
}

func TestSplitRejectsNonObject(t *testing.T) {
	_, err := overrides.Split([]byte(`[1,2]`), []byte(`[1,2,3]`))
	require.ErrorIs(t, err, overrides.ErrNotJSONObject)
}

func TestIsJSONObject(t *testing.T) {
	require.True(t, overrides.IsJSONObject([]byte(`{"a":1}`)))
	require.True(t, overrides.IsJSONObject([]byte(" \n{}\t")))
	require.False(t, overrides.IsJSONObject([]byte(`[1]`)))
	require.False(t, overrides.IsJSONObject([]byte(`# 一份 Markdown`)))
	require.False(t, overrides.IsJSONObject(nil))
}

func TestCanonJSON(t *testing.T) {
	a, err := overrides.CanonJSON([]byte("{\n \"b\":1, \"a\":2}"))
	require.NoError(t, err)
	b, err := overrides.CanonJSON([]byte(`{"a":2,"b":1}`))
	require.NoError(t, err)
	require.Equal(t, a, b, "键序与缩进不该造成差异")

	_, err = overrides.CanonJSON([]byte(`{坏的`))
	require.Error(t, err)
}
