package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/nacos-group/nacos-cli/internal/client"
	"github.com/nacos-group/nacos-cli/internal/config"
	"github.com/nacos-group/nacos-cli/internal/help"
	"github.com/spf13/cobra"
	"golang.org/x/term"
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

var (
	configGetStrict     bool
	configGetTokenStdin bool
)

const maxStdinBearerTokenBytes = 64 * 1024

var getConfigCmd = &cobra.Command{
	Use:   "config-get [dataId] [group]",
	Short: "Get a specific configuration",
	Long:  help.ConfigGet.FormatForCLI("nacos-cli"),
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		dataID := args[0]
		group := args[1]

		nacosClient := mustNewNacosClient()
		if configGetTokenStdin {
			// The client now owns the in-memory token. Clear the package-level copy
			// before performing network I/O so a long-running caller retains less
			// sensitive state.
			token = ""
		}

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

// configGetUsesStrictExplicitConnection reports whether config-get must avoid
// profiles, NACOS_* environment variables, and endpoint defaults entirely.
func configGetUsesStrictExplicitConnection(cmd *cobra.Command) bool {
	return cmd != nil && cmd.Name() == getConfigCmd.Name() && configGetStrict
}

// prepareStrictConfigGet resolves the automation-only connection contract.
// Every connection field comes from an explicit flag, while the bearer token
// comes only from piped stdin and is never accepted through argv or a profile.
func prepareStrictConfigGet(cmd *cobra.Command) error {
	if configGetOutput.format != configGetOutputRaw && configGetOutput.format != configGetOutputJSON {
		return fmt.Errorf("config-get --strict requires --output raw or --output json")
	}
	for _, name := range []string{"host", "port", "scheme", "namespace", "auth-type"} {
		if !commandFlagChanged(cmd, name) {
			return fmt.Errorf("config-get --strict requires explicit --%s", name)
		}
	}
	for _, name := range []string{
		"config",
		"profile",
		"server",
		"token",
		"username",
		"password",
		"access-key",
		"secret-key",
		"security-token",
	} {
		if commandFlagChanged(cmd, name) {
			return fmt.Errorf("config-get --strict does not allow --%s", name)
		}
	}
	if !configGetTokenStdin {
		return fmt.Errorf("config-get --strict requires --token-stdin")
	}

	normalizedScheme := strings.ToLower(strings.TrimSpace(scheme))
	if normalizedScheme != "http" && normalizedScheme != "https" {
		return fmt.Errorf("config-get --strict requires --scheme http or --scheme https")
	}
	if port <= 0 || port > 65535 {
		return fmt.Errorf("config-get --strict requires --port between 1 and 65535")
	}

	normalizedHost := strings.TrimSpace(host)
	if normalizedHost == "" || strings.Contains(normalizedHost, "://") || strings.ContainsAny(normalizedHost, "/?#") {
		return fmt.Errorf("config-get --strict requires --host as a bare hostname or IP address")
	}
	normalizedHost = strings.TrimPrefix(strings.TrimSuffix(normalizedHost, "]"), "[")
	if strings.Contains(normalizedHost, ":") && net.ParseIP(normalizedHost) == nil {
		return fmt.Errorf("config-get --strict requires --host without an embedded port")
	}

	normalizedNamespace := strings.TrimSpace(namespace)
	if normalizedNamespace == "" {
		return fmt.Errorf("config-get --strict requires a non-empty --namespace; use public explicitly for the public namespace")
	}
	normalizedAuthType, err := config.NormalizeAuthType(authType)
	if err != nil {
		return err
	}
	if normalizedAuthType != client.AuthTypeToken {
		return fmt.Errorf("config-get --strict currently requires --auth-type token")
	}

	stdin := cmd.InOrStdin()
	if file, ok := stdin.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		return fmt.Errorf("config-get --token-stdin requires piped stdin and never prompts for a token")
	}
	stdinToken, err := readBearerToken(stdin)
	if err != nil {
		return err
	}

	host = normalizedHost
	scheme = normalizedScheme
	namespace = normalizedNamespace
	authType = normalizedAuthType
	serverAddr = net.JoinHostPort(normalizedHost, strconv.Itoa(port))
	token = stdinToken
	return nil
}

func commandFlagChanged(cmd *cobra.Command, name string) bool {
	if cmd == nil {
		return false
	}
	flag := cmd.Flags().Lookup(name)
	return flag != nil && flag.Changed
}

func readBearerToken(reader io.Reader) (string, error) {
	if reader == nil {
		return "", fmt.Errorf("config-get --token-stdin requires piped stdin")
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxStdinBearerTokenBytes+1))
	if err != nil {
		return "", fmt.Errorf("read bearer token from stdin: %w", err)
	}
	if len(data) > maxStdinBearerTokenBytes {
		return "", fmt.Errorf("bearer token from stdin exceeds %d bytes", maxStdinBearerTokenBytes)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("config-get --token-stdin received an empty token")
	}
	if strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return "", fmt.Errorf("config-get --token-stdin received whitespace inside the token")
	}
	return value, nil
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
	getConfigCmd.Flags().BoolVar(&configGetStrict, "strict", false, "Require a fully explicit connection and disable profiles, environment configuration, and defaults")
	getConfigCmd.Flags().BoolVar(&configGetTokenStdin, "token-stdin", false, "Read the bearer token from piped stdin (requires --strict)")
	rootCmd.AddCommand(getConfigCmd)
}
