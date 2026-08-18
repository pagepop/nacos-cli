package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/nacos-group/nacos-cli/internal/client"
	"github.com/spf13/cobra"
)

func TestConfigGetOutputFlagDefaultsToRaw(t *testing.T) {
	flag := getConfigCmd.Flags().Lookup("output")
	if flag == nil {
		t.Fatal("config-get should expose --output")
	}
	if flag.DefValue != string(configGetOutputRaw) {
		t.Fatalf("--output default = %q, want %q", flag.DefValue, configGetOutputRaw)
	}
}

func TestParseConfigGetOutput(t *testing.T) {
	tests := []struct {
		value   string
		want    configGetOutputFormat
		wantErr bool
	}{
		{value: "raw", want: configGetOutputRaw},
		{value: "pretty", want: configGetOutputPretty},
		{value: "json", want: configGetOutputJSON},
		{value: "", wantErr: true},
		{value: "RAW", wantErr: true},
		{value: "yaml", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got, err := parseConfigGetOutput(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseConfigGetOutput(%q) should fail", tt.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseConfigGetOutput(%q) error = %v", tt.value, err)
			}
			if got != tt.want {
				t.Fatalf("parseConfigGetOutput(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestRenderConfigGetRawPreservesContent(t *testing.T) {
	content := "line 1\nline 2"
	var output bytes.Buffer
	if err := renderConfigGet(&output, configGetOutputRaw, "application.yaml", "DEFAULT_GROUP", content); err != nil {
		t.Fatalf("renderConfigGet() error = %v", err)
	}
	if got := output.String(); got != content {
		t.Fatalf("raw output = %q, want %q", got, content)
	}
}

func TestRenderConfigGetRawEmptyContentWritesNothing(t *testing.T) {
	var output bytes.Buffer
	if err := renderConfigGet(&output, configGetOutputRaw, "empty.txt", "DEFAULT_GROUP", ""); err != nil {
		t.Fatalf("renderConfigGet() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("raw empty output = %q, want no bytes", output.String())
	}
}

func TestRenderConfigGetPretty(t *testing.T) {
	var output bytes.Buffer
	err := renderConfigGet(&output, configGetOutputPretty, "application.yaml", "DEFAULT_GROUP", "key: value")
	if err != nil {
		t.Fatalf("renderConfigGet() error = %v", err)
	}

	got := output.String()
	for _, expected := range []string{"Data ID: application.yaml", "Group: DEFAULT_GROUP", "key: value"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("pretty output missing %q:\n%s", expected, got)
		}
	}
}

func TestRenderConfigGetPrettyEmptyContent(t *testing.T) {
	var output bytes.Buffer
	if err := renderConfigGet(&output, configGetOutputPretty, "empty.txt", "DEFAULT_GROUP", ""); err != nil {
		t.Fatalf("renderConfigGet() error = %v", err)
	}
	if got := output.String(); got != "Configuration not found\n" {
		t.Fatalf("pretty empty output = %q, want %q", got, "Configuration not found\n")
	}
}

func TestRenderConfigGetJSONIncludesMetadata(t *testing.T) {
	var output bytes.Buffer
	err := renderConfigGet(&output, configGetOutputJSON, "application.yaml", "DEFAULT_GROUP", "key: value\n")
	if err != nil {
		t.Fatalf("renderConfigGet() error = %v", err)
	}

	var payload struct {
		DataID  string `json:"dataId"`
		Group   string `json:"group"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("JSON output is invalid: %v", err)
	}
	if payload.DataID != "application.yaml" || payload.Group != "DEFAULT_GROUP" {
		t.Fatalf("JSON identity = (%q, %q), want (application.yaml, DEFAULT_GROUP)", payload.DataID, payload.Group)
	}
	if payload.Content != "key: value\n" {
		t.Fatalf("JSON content = %q, want %q", payload.Content, "key: value\n")
	}
}

func TestRenderConfigGetJSONPreservesEmptyContent(t *testing.T) {
	var output bytes.Buffer
	if err := renderConfigGet(&output, configGetOutputJSON, "empty.txt", "DEFAULT_GROUP", ""); err != nil {
		t.Fatalf("renderConfigGet() error = %v", err)
	}

	var payload map[string]string
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("JSON output is invalid: %v", err)
	}
	if content, ok := payload["content"]; !ok || content != "" {
		t.Fatalf("JSON content = %q, present=%t; want present empty string", content, ok)
	}
}

func TestRenderConfigGetRejectsUnknownFormat(t *testing.T) {
	err := renderConfigGet(&bytes.Buffer{}, configGetOutputFormat("xml"), "data-id", "group", "content")
	if err == nil {
		t.Fatal("renderConfigGet() should reject an unknown format")
	}
}

func TestPrepareStrictConfigGet(t *testing.T) {
	resetRootConfigForTest(t)
	command := strictConfigGetTestCommand(t, strings.NewReader("temporary-token\n"))

	if err := prepareStrictConfigGet(command); err != nil {
		t.Fatalf("prepareStrictConfigGet() error = %v", err)
	}
	if serverAddr != "t-nacos.pagepop.cn:443" {
		t.Fatalf("serverAddr = %q, want t-nacos.pagepop.cn:443", serverAddr)
	}
	if scheme != "https" || namespace != "test-namespace" || authType != client.AuthTypeToken {
		t.Fatalf("strict connection = scheme:%q namespace:%q authType:%q", scheme, namespace, authType)
	}
	if token != "temporary-token" {
		t.Fatal("strict token was not read from stdin")
	}

	nacosClient := mustNewNacosClient()
	request, err := nacosClient.NewAuthedRequest("GET", nacosClient.BaseURL(), nil)
	if err != nil {
		t.Fatalf("NewAuthedRequest() error = %v", err)
	}
	if got := request.Header.Get("Authorization"); got != "Bearer temporary-token" {
		t.Fatalf("Authorization header = %q, want bearer token from stdin", got)
	}
}

func TestPrepareStrictConfigGetRejectsUnsafeSources(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*cobra.Command)
		want      string
	}{
		{
			name: "missing explicit namespace",
			configure: func(command *cobra.Command) {
				command.Flags().Lookup("namespace").Changed = false
			},
			want: "requires explicit --namespace",
		},
		{
			name: "argv token",
			configure: func(command *cobra.Command) {
				if err := command.Flags().Set("token", "argv-token"); err != nil {
					t.Fatal(err)
				}
			},
			want: "does not allow --token",
		},
		{
			name: "profile",
			configure: func(command *cobra.Command) {
				if err := command.Flags().Set("profile", "pagepop-agent"); err != nil {
					t.Fatal(err)
				}
			},
			want: "does not allow --profile",
		},
		{
			name: "non token auth",
			configure: func(command *cobra.Command) {
				authType = "nacos"
			},
			want: "requires --auth-type token",
		},
		{
			name: "pretty output",
			configure: func(command *cobra.Command) {
				configGetOutput.format = configGetOutputPretty
			},
			want: "requires --output raw or --output json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetRootConfigForTest(t)
			command := strictConfigGetTestCommand(t, strings.NewReader("temporary-token\n"))
			tt.configure(command)
			err := prepareStrictConfigGet(command)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("prepareStrictConfigGet() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestReadBearerToken(t *testing.T) {
	tests := []struct {
		name    string
		reader  io.Reader
		want    string
		wantErr string
	}{
		{name: "single line", reader: strings.NewReader("token-value\n"), want: "token-value"},
		{name: "empty", reader: strings.NewReader(" \n"), wantErr: "empty token"},
		{name: "embedded whitespace", reader: strings.NewReader("token value\n"), wantErr: "whitespace inside"},
		{name: "too large", reader: strings.NewReader(strings.Repeat("x", maxStdinBearerTokenBytes+1)), wantErr: "exceeds"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readBearerToken(tt.reader)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("readBearerToken() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("readBearerToken() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("readBearerToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

func strictConfigGetTestCommand(t *testing.T, stdin io.Reader) *cobra.Command {
	t.Helper()
	command := &cobra.Command{Use: "config-get"}
	for _, name := range []string{
		"host",
		"scheme",
		"namespace",
		"auth-type",
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
		command.Flags().String(name, "", "")
	}
	command.Flags().Int("port", 0, "")
	for name, value := range map[string]string{
		"host":      "t-nacos.pagepop.cn",
		"port":      "443",
		"scheme":    "https",
		"namespace": "test-namespace",
		"auth-type": "token",
	} {
		if err := command.Flags().Set(name, value); err != nil {
			t.Fatalf("set --%s: %v", name, err)
		}
	}
	host = "t-nacos.pagepop.cn"
	port = 443
	scheme = "https"
	namespace = "test-namespace"
	authType = "token"
	configGetStrict = true
	configGetTokenStdin = true
	command.SetIn(stdin)
	return command
}
