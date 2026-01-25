package cmd

import (
	"context"

	"github.com/pkg/errors"
	"github.com/skevetter/devpod-provider-aws/pkg/aws"
	"github.com/skevetter/devpod/pkg/provider"
	"github.com/skevetter/log"
	"github.com/spf13/cobra"
)

// StopCmd holds the cmd flags
type StopCmd struct{}

// NewStopCmd defines a command
func NewStopCmd() *cobra.Command {
	cmd := &StopCmd{}
	stopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop an instance",
		RunE: func(_ *cobra.Command, args []string) error {
			awsProvider, err := aws.NewProvider(context.Background(), false, log.Default)
			if err != nil {
				return err
			}

			return cmd.Run(
				context.Background(),
				awsProvider,
				getMachineProviderFromEnv(),
				log.Default,
			)
		},
	}

	return stopCmd
}

// Run runs the command logic
func (cmd *StopCmd) Run(
	ctx context.Context,
	providerAws *aws.AwsProvider,
	machine *provider.Machine,
	logs log.Logger,
) error {
	instances, err := aws.GetDevpodRunningInstance(
		ctx,
		providerAws.AwsConfig,
		providerAws.Config.MachineID,
	)
	if err != nil {
		return err
	}

	if instances.Status != "" {
		err = aws.Stop(ctx, providerAws, instances.InstanceID)
		if err != nil {
			return err
		}
	} else {
		return errors.Errorf("No running instance found")
	}

	return nil
}
