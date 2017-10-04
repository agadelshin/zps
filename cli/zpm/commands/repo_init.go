package commands

import (
	"errors"

	"github.com/solvent-io/zps/cli"
	"github.com/solvent-io/zps/zpm"
	"github.com/spf13/cobra"
)

type ZpmRepoInitCommand struct {
	*cobra.Command
	*cli.Ui
}

func NewZpmRepoInitCommand() *ZpmRepoInitCommand {
	cmd := &ZpmRepoInitCommand{}
	cmd.Command = &cobra.Command{}
	cmd.Ui = cli.NewUi()
	cmd.Use = "init"
	cmd.Short = "Initialize a ZPM repository"
	cmd.Long = "Initialize a ZPM repository"
	cmd.PreRunE = cmd.setup
	cmd.RunE = cmd.run

	return cmd
}

func (z *ZpmRepoInitCommand) setup(cmd *cobra.Command, args []string) error {
	color, err := cmd.Flags().GetBool("no-color")

	z.NoColor(color)

	return err
}

func (z *ZpmRepoInitCommand) run(cmd *cobra.Command, args []string) error {
	root, _ := cmd.Flags().GetString("root")
	image, _ := cmd.Flags().GetString("image")

	if cmd.Flags().Arg(0) == "" {
		return errors.New("Repo name required")
	}

	// Load manager
	mgr, err := zpm.NewManager(root, image)
	if err != nil {
		z.Fatal(err.Error())
	}

	err = mgr.RepoInit(cmd.Flags().Arg(0))
	if err != nil {
		z.Fatal(err.Error())
	}

	return nil
}
