package cmd

import (
	"context"
	"errors"

	"github.com/skevetter/devpod-provider-aws/pkg/aws"
	"github.com/skevetter/log"
	"github.com/spf13/cobra"
)

// DeleteCmd holds the cmd flags
type DeleteCmd struct{}

// NewDeleteCmd defines a command
func NewDeleteCmd() *cobra.Command {
	cmd := &DeleteCmd{}
	return &cobra.Command{
		Use:   "delete",
		Short: "Delete an instance",
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
func (cmd *DeleteCmd) Run(ctx context.Context, providerAws *aws.AwsProvider) error {
	instance, err := aws.GetDevpodInstance(
		ctx,
		providerAws.AwsConfig,
		providerAws.Config.MachineID,
	)
	if err != nil {
		if errors.Is(err, aws.ErrInstanceNotFound) {
			return nil
		}
		return err
	}

	return aws.Delete(ctx, providerAws, instance)
}
