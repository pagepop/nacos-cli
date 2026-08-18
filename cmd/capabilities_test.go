package cmd

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestRenderCapabilities(t *testing.T) {
	var output bytes.Buffer
	if err := renderCapabilities(&output); err != nil {
		t.Fatalf("renderCapabilities() error = %v", err)
	}

	var document capabilitiesDocument
	if err := json.Unmarshal(output.Bytes(), &document); err != nil {
		t.Fatalf("capabilities output is invalid JSON: %v", err)
	}
	if document.SchemaVersion != 1 {
		t.Fatalf("schemaVersion = %d, want 1", document.SchemaVersion)
	}
	want := []string{
		"auth.token-stdin.v1",
		"config-get.raw.v1",
		"config-get.strict-explicit.v1",
	}
	if !reflect.DeepEqual(document.Capabilities, want) {
		t.Fatalf("capabilities = %v, want %v", document.Capabilities, want)
	}
}
