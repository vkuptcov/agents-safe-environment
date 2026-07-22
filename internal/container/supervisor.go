package container

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/vkuptcov/agents-safe-environment/internal/session"
)

// sessionManager is the container-local lifecycle service supervised alongside dockerd.
type sessionManager interface {
	Serve(context.Context) error
}

// sessionManagerFactory constructs the manager after container bootstrap succeeds.
type sessionManagerFactory func(session.ManagerConfig) (sessionManager, error)

// Supervisor is the root Go workload of the agents-safe Sysbox container. It reconciles the invoking
// host identity inside the container, owns dockerd, and runs the session state machine.
type Supervisor struct {
	config Config
	paths  containerPaths
	log    *log.Logger

	commands     systemCommandRunner
	processes    daemonProcessStarter
	ping         func(context.Context, string) error
	newManager   sessionManagerFactory
	effectiveUID func() int
}

// NewSupervisor constructs the production container lifecycle. It does not
// modify accounts, files, or processes until Serve is called.
func NewSupervisor(config Config, logger *log.Logger) (*Supervisor, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Supervisor{
		config:       config,
		paths:        defaultContainerPaths(),
		log:          logger,
		commands:     execSystemCommandRunner{},
		processes:    execDaemonProcessStarter{},
		ping:         pingDockerDaemon,
		newManager:   defaultManagerFactory,
		effectiveUID: os.Geteuid,
	}, nil
}

// NewSupervisorFromEnvironment parses the Docker environment and constructs
// the production Go entrypoint.
func NewSupervisorFromEnvironment(logger *log.Logger) (*Supervisor, error) {
	config, err := ConfigFromEnvironment(os.LookupEnv)
	if err != nil {
		return nil, err
	}
	return NewSupervisor(config, logger)
}

// Serve performs privileged bootstrap, waits for the manager or dockerd to
// finish, and always attempts bounded dockerd shutdown before returning.
func (supervisor *Supervisor) Serve(ctx context.Context) error {
	if supervisor.effectiveUID() != 0 {
		return fmt.Errorf("agents-safe-session serve must run as root")
	}

	// Everything from here to the first lease is bounded together. The sidecar starts its
	// initial-lease timeout when Docker creates it, so an unbounded bootstrap could outlast that
	// timeout and leave the session permanently unleased. This deadline is the session's half of the
	// design's cold-start bound; the launcher bounds container creation as the other half.
	bootstrapContext, cancelBootstrap := context.WithTimeout(ctx, PreLeaseDeadline)
	defer cancelBootstrap()
	if err := mountContainerTmpfs(bootstrapContext, supervisor.config.TmpfsMounts, supervisor.commands); err != nil {
		return fmt.Errorf("mount container tmpfs: %w", err)
	}
	if err := reconcileContainerAccount(bootstrapContext, supervisor.config, supervisor.commands); err != nil {
		return fmt.Errorf("reconcile container account: %w", err)
	}
	if err := prepareContainerUserFilesystem(
		bootstrapContext, supervisor.config, supervisor.paths, supervisor.commands,
	); err != nil {
		return fmt.Errorf("prepare container user filesystem: %w", err)
	}

	// The listeners and the lease come before dockerd, which keeps that daemon's ready timeout out
	// of the cold-start budget. Neither needs it. The channel's own lifetime is the session's, so it
	// takes ctx rather than the bootstrap deadline.
	channel, err := startHostMCP(ctx, bootstrapContext, supervisor.config.HostMCP, supervisor.log)
	if err != nil {
		return fmt.Errorf("start host MCP forwarding: %w", err)
	}
	defer channel.close()
	// The deadline has done its job; the session's own lifetime governs everything after it.
	cancelBootstrap()

	daemon, err := startDockerDaemon(
		ctx,
		supervisor.config,
		supervisor.paths,
		supervisor.processes,
		supervisor.ping,
		supervisor.log,
	)
	if err != nil {
		return err
	}

	manager, err := supervisor.newManager(session.ManagerConfig{
		SocketPath:  supervisor.paths.sessionSocket,
		SocketUID:   supervisor.config.HostUID,
		SocketGID:   supervisor.config.HostGID,
		IdleTimeout: session.DefaultIdleTimeout,
		Log:         supervisor.log,
	})
	if err != nil {
		stopErr := daemon.Stop(supervisor.config.DockerShutdownTimeout)
		return errors.Join(fmt.Errorf("create session manager: %w", err), stopErr)
	}

	managerContext, cancelManager := context.WithCancel(ctx)
	defer cancelManager()
	managerDone := make(chan error, 1)
	go func() {
		managerDone <- manager.Serve(managerContext)
	}()

	select {
	case managerErr := <-managerDone:
		stopErr := daemon.Stop(supervisor.config.DockerShutdownTimeout)
		if managerErr != nil {
			return errors.Join(fmt.Errorf("session manager stopped: %w", managerErr), stopErr)
		}
		return stopErr
	case <-daemon.done:
		daemonErr := daemon.Err()
		cancelManager()
		<-managerDone
		return fmt.Errorf("dockerd exited while the session manager was active: %v%s",
			daemonErr, daemonDiagnosticSuffix(supervisor.paths.dockerdLog))
	case <-ctx.Done():
		cancelManager()
		managerErr := <-managerDone
		stopErr := daemon.Stop(supervisor.config.DockerShutdownTimeout)
		if stopErr != nil {
			return errors.Join(fmt.Errorf("stop dockerd after context cancellation: %w", stopErr), managerErr)
		}
		if managerErr != nil && !errors.Is(managerErr, context.Canceled) {
			return managerErr
		}
		return nil
	}
}

func defaultManagerFactory(config session.ManagerConfig) (sessionManager, error) {
	return session.NewManager(config)
}

func validateConfig(config Config) error {
	if config.HostUID < 0 || config.HostGID < 0 {
		return fmt.Errorf("invalid host identity %d:%d", config.HostUID, config.HostGID)
	}
	if !accountNamePattern.MatchString(config.HostUser) {
		return fmt.Errorf("host user %q is not a supported account name", config.HostUser)
	}
	if !accountNamePattern.MatchString(config.HostGroup) {
		return fmt.Errorf("host group %q is not a supported account name", config.HostGroup)
	}
	if config.HostHome == "/" {
		return fmt.Errorf("host home cannot be the filesystem root")
	}
	if err := validateAbsolutePath("host home", config.HostHome); err != nil {
		return err
	}
	if config.DockerReadyTimeout <= 0 {
		return fmt.Errorf("Docker ready timeout must be positive, got %s", config.DockerReadyTimeout)
	}
	if config.DockerShutdownTimeout <= 0 {
		return fmt.Errorf("Docker shutdown timeout must be positive, got %s", config.DockerShutdownTimeout)
	}
	if err := validateTmpfsMounts("tmpfs mounts", config.TmpfsMounts); err != nil {
		return err
	}
	return nil
}
