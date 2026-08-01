package machines_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/machines"
	"github.com/FlintyLemming/orciny/protocol"
)

type stubConn struct{ fp string }

func (s stubConn) Fingerprint() string                      { return s.fp }
func (s stubConn) MachineID() string                        { return "m1" }
func (s stubConn) RemoteAddr() string                       { return "127.0.0.1:1" }
func (s stubConn) Send(protocol.Kind, any) error            { return nil }
func (s stubConn) SendAuthResult(bool, string, uint8) error { return nil }
func (s stubConn) Close(uint16, string) error               { return nil }

func TestNopRegistrySatisfiesRegistry(t *testing.T) {
	var r machines.Registry = machines.NopRegistry{}
	c := stubConn{fp: "fp"}

	require.NoError(t, r.Register(c))
	require.NoError(t, r.UpdateInfo(c, protocol.MachineInfo{Hostname: "box"}))
	r.Unregister(c) // 不 panic 即可
}
