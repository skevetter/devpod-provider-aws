package cmd

import (
	"context"

	"github.com/skevetter/devpod-provider-aws/pkg/aws"
	"github.com/skevetter/log"
	"github.com/spf13/cobra"
)

// CreateCmd holds the cmd flags
type CreateCmd struct{}

// NewCreateCmd defines a command
func NewCreateCmd() *cobra.Command {
	cmd := &CreateCmd{}
	return &cobra.Command{
		Use:   "create",
		Short: "Create an instance",
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
func (cmd *CreateCmd) Run(ctx context.Context, providerAws *aws.AwsProvider) error {
	_, err := aws.GetDevpodSecurityGroups(ctx, providerAws)
	if err != nil {
		return err
	}

	_, err = aws.Create(ctx, providerAws.AwsConfig, providerAws)
	if err != nil {
		return err
	}

	return nil
}
