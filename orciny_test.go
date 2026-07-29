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
	require.Equal(t, "0.1.0", v.String())
}

func TestAppName(t *testing.T) {
	require.Equal(t, "orciny", orciny.AppName)
}

func TestMinAgentVersionNotAboveCurrent(t *testing.T) {
	cur := semver.MustParse(orciny.Version)
	require.False(t, orciny.MinAgentVersion.GT(cur),
		"门槛版本不能高于当前版本，否则自带的 agent 会被自己拒绝")
}
