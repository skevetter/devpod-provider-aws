package cmd

import (
	"context"

	"github.com/pkg/errors"
	"github.com/skevetter/devpod-provider-aws/pkg/aws"
	"github.com/skevetter/devpod/pkg/provider"
	"github.com/skevetter/log"
	"github.com/spf13/cobra"
)

// StartCmd holds the cmd flags
type StartCmd struct{}

// NewStartCmd defines a command
func NewStartCmd() *cobra.Command {
	cmd := &StartCmd{}
	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start an instance",
		RunE: func(_ *cobra.Command, args []string) error {
			awsProvider, err := aws.NewProvider(context.Background(), true, log.Default)
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

	return startCmd
}

// Run runs the command logic
func (cmd *StartCmd) Run(
	ctx context.Context,
	providerAws *aws.AwsProvider,
	machine *provider.Machine,
	logs log.Logger,
) error {
	instance, err := aws.GetDevpodStoppedInstance(
		ctx,
		providerAws.AwsConfig,
		providerAws.Config.MachineID,
	)
	if err != nil {
		return err
	}

	if instance.Status != "" {
		err = aws.Start(ctx, providerAws.AwsConfig, instance.InstanceID)
		if err != nil {
			return err
		}
	} else {
		return errors.Errorf("No stopped instance %s found", providerAws.Config.MachineID)
	}

	return nil
}
