package cmd

import (
	"fmt"
	"runtime"

	"github.com/jkroepke/kube-webhook-certgen/core"
	"github.com/spf13/cobra"
)

var version = &cobra.Command{
	Use:   "version",
	Short: "Prints the CLI version information",
	Run:   versionCmdRun,
}

//nolint:forbidigo
func versionCmdRun(_ *cobra.Command, _ []string) {
	fmt.Printf("%s\n", core.Version)
	fmt.Printf("build %s\n", core.BuildTime)
	fmt.Printf("%s\n", runtime.Version())
}

func init() {
	rootCmd.AddCommand(version)
}
