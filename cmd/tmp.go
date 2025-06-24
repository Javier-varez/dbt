package cmd

import (
	"log"

	"github.com/daedaleanai/dbt/v3/util"
	"github.com/daedaleanai/dbt/v3/workspace"

	"github.com/daedaleanai/cobra"
)

var tmpCmd = &cobra.Command{
	Use: "tmp",
	Run: func(cmd *cobra.Command, args []string) {
		runTmp(args, modeBuild, nil)
	},
}

func init() {
	rootCmd.AddCommand(tmpCmd)
}

func runTmp(args []string, mode mode, modeArgs []string) {
	workspaceRoot := util.GetWorkspaceRoot()
	_, err := workspace.OpenWorkspace(workspaceRoot)
	if err != nil {
		log.Fatal("Error opening workspace", err)
	}

}
