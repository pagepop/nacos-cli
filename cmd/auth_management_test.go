package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nacos-group/nacos-cli/internal/config"
	"github.com/spf13/cobra"
)

func managementCommandTestRoot() *cobra.Command {
	root := &cobra.Command{Use: "nacos-cli", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("profile", "", "")
	root.PersistentFlags().String("config", "", "")
	root.PersistentPreRun = func(*cobra.Command, []string) { panic("legacy interactive loader must not run") }
	for _, name := range managementCommandNames {
		root.AddCommand(newManagementCommand(name))
	}
	return root
}

func runManagementTest(args ...string) (string, error) {
	root := managementCommandTestRoot()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs(args)
	err := root.Execute()
	return output.String(), err
}

func writeManagementTestProfile(t *testing.T, server string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "controlled.conf")
	content := "host: " + strings.TrimPrefix(server, "http://") + "\nscheme: http\nauthType: token\ntoken: controlled-token\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestManagementDryRunDoesNotContactServer(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(500) }))
	defer server.Close()
	profile := writeManagementTestProfile(t, server.URL)
	for _, args := range [][]string{
		{"role-bind", "--role", "ROLE_EXAMPLE", "--user", "LDAP_engineer7"},
		{"role-unbind", "--role", "ROLE_EXAMPLE", "--user", "LDAP_engineer7"},
		{"permission-add", "--role", "ROLE_EXAMPLE", "--resource", "online:DEFAULT_GROUP:config/example", "--action", "rw"},
		{"permission-remove", "--role", "ROLE_EXAMPLE", "--resource", "online:DEFAULT_GROUP:config/example", "--action", "rw"},
	} {
		args = append(args, "--config", profile, "--dry-run")
		output, err := runManagementTest(args...)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if json.Unmarshal([]byte(output), &result) != nil || result["dryRun"] != true || result["verified"] != false || strings.Contains(output, "controlled-token") {
			t.Fatalf("unsafe or invalid preview: %s", output)
		}
	}
	if requests != 0 {
		t.Fatalf("dry run contacted server %d times", requests)
	}
}

func TestManagementInvalidTargetsFailBeforeAuthentication(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
	defer server.Close()
	profile := writeManagementTestProfile(t, server.URL)
	for _, args := range [][]string{
		{"role-unbind", "--role", "ROLE_EXAMPLE"},
		{"role-bind", "--user", "LDAP_engineer7", "--role", " "},
		{"permission-add", "--role", "R", "--resource", "online:*:*", "--action", "admin"},
		{"user-list", "--page", "0"},
		{"role-list", "--all", "--page", "2"},
		{"permission-list", "--output", "invalid"},
	} {
		args = append(args, "--config", profile)
		if _, err := runManagementTest(args...); err == nil {
			t.Fatalf("expected invalid input failure: %v", args)
		}
	}
	if requests != 0 {
		t.Fatal("invalid input authenticated or made a request")
	}
}

func TestManagementMissingProfileFailsWithoutCreatingFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, args := range [][]string{{"user-list"}, {"user-list", "--profile", "missing"}} {
		if _, err := runManagementTest(args...); err == nil {
			t.Fatal("expected explicit profile failure")
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatal("query created or modified profile files")
	}
}

func TestManagementUserListOmitsPassword(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/nacos/v3/auth/user/list" {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"code":0,"data":{"totalCount":1,"pageItems":[{"username":"LDAP_engineer7","password":"secret-value"}]}}`)
	}))
	defer server.Close()
	profile := writeManagementTestProfile(t, server.URL)
	before, _ := os.ReadFile(profile)
	output, err := runManagementTest("user-list", "--config", profile, "--user", "LDAP_engineer7", "--all")
	if err != nil || !strings.Contains(output, "LDAP_engineer7") || strings.Contains(output, "password") || strings.Contains(output, "secret-value") {
		t.Fatalf("unsafe user output %s, err %v", output, err)
	}
	after, _ := os.ReadFile(profile)
	if !bytes.Equal(before, after) {
		t.Fatal("query rewrote profile")
	}
}

func TestManagementReusesEncryptedProfileAndV3Login(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	logins := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/nacos/v3/auth/user/login":
			logins++
			_ = r.ParseForm()
			if r.Form.Get("username") != "operator" || r.Form.Get("password") != "controlled-password" {
				t.Error("incorrect profile credentials")
			}
			fmt.Fprint(w, `{"code":0,"data":{"accessToken":"controlled-login-token","tokenTtl":3600}}`)
		case "/nacos/v3/auth/user/list":
			if r.Header.Get("Authorization") != "Bearer controlled-login-token" {
				t.Error("missing login token")
			}
			fmt.Fprint(w, `{"code":0,"data":{"totalCount":0,"pageItems":[]}}`)
		default:
			t.Errorf("unexpected legacy or alternate endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	path, err := config.GetProfileConfigPath("controlled")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Host: strings.TrimPrefix(server.URL, "http://"), Scheme: "http", AuthType: "nacos", Username: "operator", Password: "controlled-password", Token: "controlled-unused-token"}
	if err = cfg.SaveConfig(path); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if strings.Contains(string(before), "controlled-password") {
		t.Fatal("test profile was not encrypted")
	}
	output, err := runManagementTest("user-list", "--profile", "controlled", "--all")
	if err != nil || logins != 1 || strings.Contains(output, "controlled-login-token") {
		t.Fatalf("profile login failed: %v, output=%s", err, output)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("query rewrote encrypted profile")
	}
}
