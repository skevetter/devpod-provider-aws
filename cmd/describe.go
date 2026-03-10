package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/skevetter/devpod-provider-aws/pkg/aws"
	"github.com/skevetter/log"
	"github.com/spf13/cobra"
)

// DescribeCmd holds the cmd flags.
type DescribeCmd struct{}

// NewDescribeCmd defines a command.
func NewDescribeCmd() *cobra.Command {
	cmd := &DescribeCmd{}
	return &cobra.Command{
		Use:   "describe",
		Short: "Retrieve description of the virtual machine",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			awsProvider, err := aws.NewProvider(cobraCmd.Context(), true, log.Default)
			if err != nil {
				return err
			}

			return cmd.Run(cobraCmd.Context(), awsProvider)
		},
	}
}

// Run runs the command logic.
func (cmd *DescribeCmd) Run(ctx context.Context, providerAws *aws.AwsProvider) error {
	json, err := aws.Describe(ctx, providerAws, providerAws.Config.MachineID)
	if err != nil {
		return err
	}

	_, err = fmt.Fprint(os.Stdout, json)
	return err
}
