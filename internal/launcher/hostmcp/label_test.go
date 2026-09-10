package hostmcp_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/hostmcp"
)

func TestParseLabelRestoresARecordedEndpointSet(t *testing.T) {
	set, err := hostmcp.ParseLabel("127.0.0.1:64342,localhost:8080")
	require.NoError(t, err, "a recorded label must parse")
	require.Equal(t, "127.0.0.1:64342,localhost:8080", set.Label(), "the label round-trips in recorded order")
	require.Equal(t, []string{"127.0.0.1:8080", "[::1]:8080"}, set.Endpoints[1].Listen(),
		"localhost keeps both loopback listeners after restoration")
}

func TestParseLabelTreatsAbsentAsEmpty(t *testing.T) {
	set, err := hostmcp.ParseLabel(hostmcp.AbsentLabel)
	require.NoError(t, err)
	require.True(t, set.Empty(), "the absent marker forwards nothing")
	set, err = hostmcp.ParseLabel("")
	require.NoError(t, err)
	require.True(t, set.Empty(), "a missing label forwards nothing")
}

func TestParseLabelRejectsMalformedEntries(t *testing.T) {
	for _, label := range []string{"no-port", "127.0.0.1:notaport", "127.0.0.1:0", "example.com:80", ",127.0.0.1:1"} {
		_, err := hostmcp.ParseLabel(label)
		require.Error(t, err, "label %q must be rejected", label)
	}
}
