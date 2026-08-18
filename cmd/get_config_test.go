package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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
