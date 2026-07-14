package launcher

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProjectContainerNameMatchesDesignExample(t *testing.T) {
	t.Parallel()
	key, err := ProjectKey(1000, "/home/alex/sources/example-project")
	require.NoError(t, err, "design example project key must be derivable")
	require.Equal(t, "aba8b4ca4ff345d5d0443c0c", key, "project key must match the documented SHA-256 example")
	name, err := ProjectContainerName(1000, "/home/alex/sources/example-project")
	require.NoError(t, err, "design example container name must be derivable")
	require.Equal(t, "codex-safe-aba8b4ca4ff345d5d0443c0c", name, "container name must use the documented project key")
}

func TestProjectContainerNameSeparatesUsersAndWorktrees(t *testing.T) {
	t.Parallel()
	first, err := ProjectContainerName(1000, "/project")
	require.NoError(t, err, "container name for the first user/project must be derivable")
	differentUser, err := ProjectContainerName(1001, "/project")
	require.NoError(t, err, "container name for the second user must be derivable")
	differentProject, err := ProjectContainerName(1000, "/project-feature")
	require.NoError(t, err, "container name for the second project must be derivable")
	require.NotEqual(t, first, differentUser, "different host UIDs must not share a container name")
	require.NotEqual(t, first, differentProject, "different project roots must not share a container name")
	require.NotEqual(t, differentUser, differentProject, "different UID and project inputs must remain distinct")
}
