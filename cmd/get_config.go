package cmd

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/nacos-group/nacos-cli/internal/help"
	"github.com/spf13/cobra"
)

type configGetOutputFormat string

const (
	configGetOutputRaw    configGetOutputFormat = "raw"
	configGetOutputPretty configGetOutputFormat = "pretty"
	configGetOutputJSON   configGetOutputFormat = "json"
)

type configGetOutputValue struct {
	format configGetOutputFormat
}

func (v *configGetOutputValue) Set(value string) error {
	format, err := parseConfigGetOutput(value)
	if err != nil {
		return err
	}
	v.format = format
	return nil
}

func (v *configGetOutputValue) String() string {
	return string(v.format)
}

func (v *configGetOutputValue) Type() string {
	return "output-format"
}

var configGetOutput = configGetOutputValue{format: configGetOutputRaw}

var getConfigCmd = &cobra.Command{
	Use:   "config-get [dataId] [group]",
	Short: "Get a specific configuration",
	Long:  help.ConfigGet.FormatForCLI("nacos-cli"),
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		dataID := args[0]
		group := args[1]

		nacosClient := mustNewNacosClient()

		if configGetOutput.format == configGetOutputPretty {
			fmt.Fprintf(cmd.OutOrStdout(), "Fetching config: %s (%s)...\n\n", dataID, group)
		}

		content, err := nacosClient.GetConfig(dataID, group)
		checkError(err)

		if verbose {
			fmt.Fprintf(
				cmd.ErrOrStderr(),
				"[info] config-get fetched dataId=%s group=%s output=%s bytes=%d\n",
				dataID,
				group,
				configGetOutput.format,
				len(content),
			)
		}

		checkError(renderConfigGet(cmd.OutOrStdout(), configGetOutput.format, dataID, group, content))
	},
}

func parseConfigGetOutput(value string) (configGetOutputFormat, error) {
	switch configGetOutputFormat(value) {
	case configGetOutputRaw, configGetOutputPretty, configGetOutputJSON:
		return configGetOutputFormat(value), nil
	default:
		return "", fmt.Errorf("unsupported --output value %q: expected raw, pretty, or json", value)
	}
}

// renderConfigGet writes only the selected representation to stdout.
// Operational diagnostics belong on stderr so raw output remains byte-exact.
func renderConfigGet(writer io.Writer, format configGetOutputFormat, dataID, group, content string) error {
	switch format {
	case configGetOutputRaw:
		_, err := io.WriteString(writer, content)
		return err
	case configGetOutputPretty:
		if content == "" {
			_, err := fmt.Fprintln(writer, "Configuration not found")
			return err
		}
		if _, err := fmt.Fprintln(writer, "═══════════════════════════════════════"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(writer, "Data ID: %s\n", dataID); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(writer, "Group: %s\n", group); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(writer, "═══════════════════════════════════════"); err != nil {
			return err
		}
		_, err := fmt.Fprintln(writer, content)
		return err
	case configGetOutputJSON:
		payload := struct {
			DataID  string `json:"dataId"`
			Group   string `json:"group"`
			Content string `json:"content"`
		}{
			DataID:  dataID,
			Group:   group,
			Content: content,
		}
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(payload)
	default:
		return fmt.Errorf("unsupported config-get output format %q", format)
	}
}

func init() {
	getConfigCmd.Flags().Var(&configGetOutput, "output", "Output format: raw, pretty, or json")
	rootCmd.AddCommand(getConfigCmd)
}
