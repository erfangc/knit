package common

import (
	"fmt"
	"github.com/erfangc/knit/cmd"
	"github.com/spf13/cobra"
	"os"
)

var rootCmd = &cobra.Command{
	Use:   "knit",
	Short: "knit bootstraps a Kubernetes cluster that has already been provisioned by Rancher",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(cmd.EKSCmd)
}

func main() {
	err := rootCmd.Execute()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
