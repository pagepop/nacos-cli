package cmd

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"
)

const capabilitiesSchemaVersion = 1

var automationCapabilities = []string{
	"auth.token-stdin.v1",
	"config-get.raw.v1",
	"config-get.strict-explicit.v1",
}

type capabilitiesDocument struct {
	SchemaVersion int      `json:"schemaVersion"`
	Capabilities  []string `json:"capabilities"`
}

var capabilitiesCmd = &cobra.Command{
	Use:   "capabilities",
	Short: "Print stable machine-readable CLI capabilities",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return renderCapabilities(cmd.OutOrStdout())
	},
}

func renderCapabilities(writer io.Writer) error {
	document := capabilitiesDocument{
		SchemaVersion: capabilitiesSchemaVersion,
		Capabilities:  automationCapabilities,
	}
	return json.NewEncoder(writer).Encode(document)
}

func init() {
	rootCmd.AddCommand(capabilitiesCmd)
}
