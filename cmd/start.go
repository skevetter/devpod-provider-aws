package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/skevetter/devpod-provider-aws/pkg/aws"
	"github.com/skevetter/log"
	"github.com/spf13/cobra"
)

// StartCmd holds the cmd flags
type StartCmd struct{}

// NewStartCmd defines a command
func NewStartCmd() *cobra.Command {
	cmd := &StartCmd{}
	return &cobra.Command{
		Use:   "start",
		Short: "Start an instance",
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
func (cmd *StartCmd) Run(ctx context.Context, providerAws *aws.AwsProvider) error {
	instance, err := aws.GetDevpodStoppedInstance(
		ctx,
		providerAws.AwsConfig,
		providerAws.Config.MachineID,
	)
	if err != nil {
		if errors.Is(err, aws.ErrInstanceNotFound) {
			return fmt.Errorf("no stopped instance %s found", providerAws.Config.MachineID)
		}
		return err
	}

	return aws.Start(ctx, providerAws, instance.InstanceID)
}
