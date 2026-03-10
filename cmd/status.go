package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/skevetter/devpod-provider-aws/pkg/aws"
	"github.com/skevetter/log"
	"github.com/spf13/cobra"
)

type InstanceStatus struct {
	NetworkInterfaces []InstanceStatusNetworkInterface `json:"networkInterfaces,omitempty"`
	Status            string                           `json:"status,omitempty"`
}

type InstanceStatusNetworkInterface struct {
	AccessConfigs []InstanceStatusAccessConfig `json:"accessConfigs,omitempty"`
}

type InstanceStatusAccessConfig struct {
	NatIP string `json:"natIP,omitempty"`
}

// StatusCmd holds the cmd flags
type StatusCmd struct{}

// NewStatusCmd defines a command
func NewStatusCmd() *cobra.Command {
	cmd := &StatusCmd{}
	return &cobra.Command{
		Use:   "status",
		Short: "Status an instance",
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
func (cmd *StatusCmd) Run(ctx context.Context, providerAws *aws.AwsProvider) error {
	status, err := aws.Status(ctx, providerAws, providerAws.Config.MachineID)
	if err != nil {
		return err
	}

	_, err = fmt.Fprint(os.Stdout, status)

	return err
}
