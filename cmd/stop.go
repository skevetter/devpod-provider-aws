package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/skevetter/devpod-provider-aws/pkg/aws"
	"github.com/skevetter/log"
	"github.com/spf13/cobra"
)

// StopCmd holds the cmd flags
type StopCmd struct{}

// NewStopCmd defines a command
func NewStopCmd() *cobra.Command {
	cmd := &StopCmd{}
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop an instance",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			awsProvider, err := aws.NewProvider(cobraCmd.Context(), false, log.Default)
			if err != nil {
				return err
			}

			return cmd.Run(cobraCmd.Context(), awsProvider)
		},
	}
}

// Run runs the command logic
func (cmd *StopCmd) Run(ctx context.Context, providerAws *aws.AwsProvider) error {
	instances, err := aws.GetDevpodRunningInstance(
		ctx,
		providerAws.AwsConfig,
		providerAws.Config.MachineID,
	)
	if err != nil {
		if errors.Is(err, aws.ErrInstanceNotFound) {
			return fmt.Errorf("no running instance found")
		}
		return err
	}

	return aws.Stop(ctx, providerAws, instances.InstanceID)
}
