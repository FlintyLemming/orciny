package orciny_test

import (
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny"
)

func TestVersionIsValidSemver(t *testing.T) {
	v, err := semver.Parse(orciny.Version)
	require.NoError(t, err, "Version 必须是合法 semver")
	// 断言的是「规范形式」而不是某个字面量：Version 会随里程碑上抬，
	// 而且 goreleaser 会用 -ldflags 注入构建版本。
	require.Equal(t, orciny.Version, v.String())
}

func TestAppName(t *testing.T) {
	require.Equal(t, "orciny", orciny.AppName)
}

func TestMinAgentVersionNotAboveCurrent(t *testing.T) {
	cur := semver.MustParse(orciny.Version)
	require.False(t, orciny.MinAgentVersion.GT(cur),
		"门槛版本不能高于当前版本，否则自带的 agent 会被自己拒绝")
}

func TestMinAgentVersionStaysAtM1(t *testing.T) {
	require.Equal(t, "0.1.0", orciny.MinAgentVersion.String(),
		"M1.5 的版本门槛是定向的，不许抬全局握手门槛（spec §10）")
}

func TestMinProviderAgentVersion(t *testing.T) {
	require.Equal(t, "0.2.0", orciny.MinProviderAgentVersion.String())
	require.False(t, orciny.MinProviderAgentVersion.LT(orciny.MinAgentVersion),
		"定向门槛不该低于全局门槛")
	require.False(t, orciny.MinProviderAgentVersion.GT(semver.MustParse(orciny.Version)),
		"定向门槛不能高于当前版本，否则自带的 agent 会被自己拒绝")
}
