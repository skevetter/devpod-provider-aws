package cmd

import (
	"context"

	"github.com/skevetter/devpod-provider-aws/pkg/aws"
	"github.com/skevetter/devpod-provider-aws/pkg/options"
	"github.com/skevetter/log"
	"github.com/spf13/cobra"
)

// InitCmd holds the cmd flags
type InitCmd struct{}

// NewInitCmd defines a init
func NewInitCmd() *cobra.Command {
	cmd := &InitCmd{}
	return &cobra.Command{
		Use:   "init",
		Short: "Init account",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
}

// Run runs the init logic
func (cmd *InitCmd) Run(ctx context.Context) error {
	config, err := options.FromEnv(true, false)
	if err != nil {
		return err
	}

	_, err = aws.NewAWSConfig(ctx, log.Default, config)
	if err != nil {
		return err
	}

	return nil
}
