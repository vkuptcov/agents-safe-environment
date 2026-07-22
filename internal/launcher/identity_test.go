package launcher

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProjectContainerNameMatchesDesignExample(t *testing.T) {
	t.Parallel()
	key := ProjectKey(1000, "/home/alex/sources/example-project")
	require.Equal(t, "aba8b4ca4ff345d5d0443c0c", key, "project key must match the documented SHA-256 example")
	name := ProjectContainerName(1000, "/home/alex/sources/example-project")
	require.Equal(t, "agents-safe-aba8b4ca4ff345d5d0443c0c", name, "container name must use the documented project key")
}

func TestProjectContainerNameSeparatesUsersAndWorktrees(t *testing.T) {
	t.Parallel()
	first := ProjectContainerName(1000, "/project")
	differentUser := ProjectContainerName(1001, "/project")
	differentProject := ProjectContainerName(1000, "/project-feature")
	require.NotEqual(t, first, differentUser, "different host UIDs must not share a container name")
	require.NotEqual(t, first, differentProject, "different project roots must not share a container name")
	require.NotEqual(t, differentUser, differentProject, "different UID and project inputs must remain distinct")
}
