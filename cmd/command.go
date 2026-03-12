package cmd

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/skevetter/devpod-provider-aws/pkg/aws"
	"github.com/skevetter/devpod-provider-aws/pkg/options"
	"github.com/skevetter/devpod/pkg/ssh"
	"github.com/skevetter/log"
	"github.com/spf13/cobra"
	gossh "golang.org/x/crypto/ssh"
)

// CommandCmd holds the cmd flags
type CommandCmd struct{}

// NewCommandCmd defines a command
func NewCommandCmd() *cobra.Command {
	cmd := &CommandCmd{}
	return &cobra.Command{
		Use:   "command",
		Short: "Command an instance",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			awsProvider, err := aws.NewProvider(cobraCmd.Context(), true, log.Default)
			if err != nil {
				return err
			}

			return cmd.Run(cobraCmd.Context(), awsProvider)
		},
	}
}

// Run runs the command logic
func (cmd *CommandCmd) Run(ctx context.Context, providerAws *aws.AwsProvider) error {
	command := os.Getenv("COMMAND")
	if command == "" {
		return fmt.Errorf("command environment variable is missing")
	}

	privateKey, err := ssh.GetPrivateKeyRawBase(providerAws.Config.MachineFolder)
	if err != nil {
		return fmt.Errorf("load private key: %w", err)
	}

	instance, err := aws.GetDevpodRunningInstance(
		ctx,
		providerAws.AwsConfig,
		providerAws.Config.MachineID,
	)
	if err != nil {
		return err
	}

	strategy := cmd.selectStrategy(providerAws.Config)
	defer func() { _ = strategy.Close() }()

	client, err := strategy.Connect(ctx, &instance, privateKey)
	if err != nil {
		return err
	}

	return ssh.Run(ctx, ssh.RunOptions{
		Client:  client,
		Command: command,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	})
}

// ConnectionStrategy defines how to connect to an EC2 instance
type ConnectionStrategy interface {
	Connect(ctx context.Context, instance *aws.Machine, privateKey []byte) (*gossh.Client, error)
	Close() error
	Name() string
}

// baseTunnelStrategy provides common tunnel + SSH client management
type baseTunnelStrategy struct {
	tunnel *TunnelManager
	client *gossh.Client
	name   string
}

func (s *baseTunnelStrategy) Close() error {
	if s.client != nil {
		_ = s.client.Close()
	}
	if s.tunnel != nil {
		return s.tunnel.Close()
	}
	return nil
}

func (s *baseTunnelStrategy) Name() string {
	return s.name
}

// DirectSSHStrategy connects via direct SSH
type DirectSSHStrategy struct {
	client *gossh.Client
}

func (s *DirectSSHStrategy) Connect(
	ctx context.Context,
	instance *aws.Machine,
	privateKey []byte,
) (*gossh.Client, error) {
	host := instance.Host()
	client, err := ssh.NewSSHClient("devpod", host+":22", privateKey)
	if err != nil {
		return nil, fmt.Errorf("direct ssh to %s: %w", host, err)
	}
	s.client = client
	return client, nil
}

func (s *DirectSSHStrategy) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

func (s *DirectSSHStrategy) Name() string {
	return "direct-ssh"
}

// InstanceConnectStrategy connects via EC2 Instance Connect
type InstanceConnectStrategy struct {
	baseTunnelStrategy
	endpointID string
}

func (s *InstanceConnectStrategy) Connect(
	ctx context.Context,
	instance *aws.Machine,
	privateKey []byte,
) (*gossh.Client, error) {
	s.name = "instance-connect"

	port, err := findAvailablePort()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.name, err)
	}

	args := []string{
		"ec2-instance-connect",
		"open-tunnel",
		"--instance-id",
		instance.InstanceID,
		"--local-port",
		strconv.Itoa(port),
	}
	if s.endpointID != "" {
		args = append(args, "--instance-connect-endpoint-id", s.endpointID)
	}

	s.tunnel = &TunnelManager{port: port}
	if err := s.tunnel.Start(ctx, args); err != nil {
		return nil, fmt.Errorf("%s: %w", s.name, err)
	}

	client, err := ssh.NewSSHClient("devpod", s.tunnel.Address(), privateKey)
	if err != nil {
		_ = s.tunnel.Close()
		return nil, fmt.Errorf("%s: ssh connect: %w", s.name, err)
	}
	s.client = client
	return client, nil
}

// SessionManagerStrategy connects via AWS Session Manager
type SessionManagerStrategy struct {
	baseTunnelStrategy
}

func (s *SessionManagerStrategy) Connect(
	ctx context.Context,
	instance *aws.Machine,
	privateKey []byte,
) (*gossh.Client, error) {
	s.name = "session-manager"

	port, err := findAvailablePort()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.name, err)
	}

	args, err := aws.CommandArgsSSMTunneling(instance.InstanceID, port)
	if err != nil {
		return nil, fmt.Errorf("%s: build args: %w", s.name, err)
	}

	s.tunnel = &TunnelManager{port: port}
	if err := s.tunnel.Start(ctx, args); err != nil {
		return nil, fmt.Errorf("%s: %w", s.name, err)
	}

	client, err := ssh.NewSSHClient("devpod", s.tunnel.Address(), privateKey)
	if err != nil {
		_ = s.tunnel.Close()
		return nil, fmt.Errorf("%s: ssh connect: %w", s.name, err)
	}
	s.client = client
	return client, nil
}

// TunnelManager manages AWS CLI tunnel processes
type TunnelManager struct {
	cmd    *exec.Cmd
	port   int
	cancel context.CancelFunc
}

func (t *TunnelManager) Start(ctx context.Context, args []string) error {
	cancelCtx, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	t.cmd = exec.CommandContext(cancelCtx, "aws", args...)
	if err := t.cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start tunnel: %w", err)
	}

	timeoutCtx, cancelFn := context.WithTimeout(ctx, 30*time.Second)
	defer cancelFn()

	if err := waitForPort(timeoutCtx, t.Address()); err != nil {
		_ = t.Close()
		return fmt.Errorf("tunnel port not ready: %w", err)
	}
	return nil
}

func (t *TunnelManager) Address() string {
	return fmt.Sprintf("localhost:%d", t.port)
}

func (t *TunnelManager) Close() error {
	if t.cancel != nil {
		t.cancel()
	}
	if t.cmd != nil && t.cmd.Process != nil {
		return t.cmd.Process.Kill()
	}
	return nil
}

// selectStrategy chooses the appropriate connection strategy based on config
func (cmd *CommandCmd) selectStrategy(config *options.Options) ConnectionStrategy {
	if config.UseInstanceConnectEndpoint {
		return &InstanceConnectStrategy{endpointID: config.InstanceConnectEndpointID}
	}
	if config.UseSessionManager {
		return &SessionManagerStrategy{}
	}
	return &DirectSSHStrategy{}
}

func waitForPort(ctx context.Context, addr string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for port %s", addr)
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				return nil
			}
		}
	}
}

func findAvailablePort() (int, error) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, fmt.Errorf("find available port: %w", err)
	}
	defer func() { _ = l.Close() }()

	return l.Addr().(*net.TCPAddr).Port, nil
}
