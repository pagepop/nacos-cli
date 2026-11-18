package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/nacos-group/nacos-cli/internal/client"
	"github.com/nacos-group/nacos-cli/internal/config"
	"github.com/spf13/cobra"
)

var managementCommandNames = []string{
	"user-list", "role-list", "role-bind", "role-unbind",
	"permission-list", "permission-add", "permission-remove",
}

type managementOptions struct {
	user     string
	role     string
	resource string
	action   string
	search   string
	page     int
	size     int
	all      bool
	dryRun   bool
	output   string
}

type managementChange struct {
	Operation  string     `json:"operation"`
	Endpoint   string     `json:"endpoint"`
	Method     string     `json:"method"`
	Parameters url.Values `json:"parameters"`
	DryRun     bool       `json:"dryRun"`
	Changed    bool       `json:"changed"`
	Verified   bool       `json:"verified"`
	Status     string     `json:"status"`
}

func newManagementCommand(name string) *cobra.Command {
	options := &managementOptions{}
	list := strings.HasSuffix(name, "-list")
	description := map[string]string{
		"user-list":         "List Nacos users without password fields",
		"role-list":         "List Nacos user-role bindings",
		"role-bind":         "Bind a role to one exact Nacos username",
		"role-unbind":       "Unbind a role from one exact Nacos username",
		"permission-list":   "List resource permissions assigned to roles",
		"permission-add":    "Add a role permission and verify the stored record",
		"permission-remove": "Remove an exact role permission and verify its absence",
	}
	command := &cobra.Command{
		Use: name, Short: description[name], Args: cobra.NoArgs,
		// Bypass the root's interactive/default-profile loader. Management commands
		// only read an explicitly selected existing profile and never rewrite it.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE:              func(cmd *cobra.Command, _ []string) error { return runManagementCommand(cmd, name, options) },
	}
	command.Flags().StringVar(&options.output, "output", "json", "Output format: json or pretty (indented JSON)")
	if name == "user-list" || strings.HasPrefix(name, "role-") {
		command.Flags().StringVar(&options.user, "user", "", "Target Nacos username; login credentials come from the selected profile")
	}
	if name != "user-list" {
		command.Flags().StringVar(&options.role, "role", "", "Exact role name")
	}
	if list {
		command.Flags().IntVar(&options.page, "page", 1, "Page number")
		command.Flags().IntVar(&options.size, "size", 100, "Page size")
		command.Flags().BoolVar(&options.all, "all", false, "Read all pages and reject incomplete or changing pagination")
		command.Flags().StringVar(&options.search, "search", "accurate", "Search mode: accurate or blur")
	} else {
		_ = command.MarkFlagRequired("role")
		command.Flags().BoolVar(&options.dryRun, "dry-run", false, "Print the target and request without authenticating or sending HTTP requests")
		if strings.HasPrefix(name, "role-") {
			_ = command.MarkFlagRequired("user")
		} else {
			command.Flags().StringVar(&options.resource, "resource", "", "Exact Nacos permission resource")
			command.Flags().StringVar(&options.action, "action", "", "Permission action: r, w or rw; w includes deletion")
			_ = command.MarkFlagRequired("resource")
			_ = command.MarkFlagRequired("action")
		}
	}
	return command
}

func validateManagementOptions(name string, options *managementOptions) error {
	if options.output != "json" && options.output != "pretty" {
		return fmt.Errorf("--output must be json or pretty")
	}
	for _, value := range []string{options.user, options.role, options.resource} {
		if value != strings.TrimSpace(value) || strings.ContainsFunc(value, unicode.IsControl) {
			return fmt.Errorf("target fields must not contain control characters or surrounding whitespace")
		}
	}
	if strings.HasSuffix(name, "-list") {
		if options.page < 1 || options.size < 1 || (options.all && options.page != 1) {
			return fmt.Errorf("page and size must be positive; --all requires page 1")
		}
		if options.search != "accurate" && options.search != "blur" {
			return fmt.Errorf("--search must be accurate or blur")
		}
		return nil
	}
	if options.role == "" {
		return fmt.Errorf("--role is required")
	}
	if strings.HasPrefix(name, "role-") {
		if options.user == "" {
			return fmt.Errorf("--user must be a full non-empty target username")
		}
	} else if options.resource == "" || (options.action != "r" && options.action != "w" && options.action != "rw") {
		return fmt.Errorf("--resource and --action (r, w or rw) are required")
	}
	return nil
}

func loadManagementClient(cmd *cobra.Command) (*client.NacosClient, error) {
	profile, _ := cmd.Flags().GetString("profile")
	path, _ := cmd.Flags().GetString("config")
	if (profile == "") == (path == "") {
		return nil, fmt.Errorf("auth management requires exactly one explicit --profile or --config")
	}
	for _, flag := range []string{"host", "port", "server", "scheme", "namespace", "auth-type", "username", "password", "access-key", "secret-key", "security-token", "token"} {
		if cmd.Flags().Changed(flag) {
			return nil, fmt.Errorf("auth management reads the selected profile; connection override --%s is not supported", flag)
		}
	}
	if path == "" {
		var err error
		path, err = config.GetProfileConfigPath(profile)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve the selected management profile")
		}
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		return nil, fmt.Errorf("cannot load the selected management profile; check its existence, format and encrypted credentials")
	}
	if missing := cfg.GetMissingFields(); len(missing) > 0 {
		return nil, fmt.Errorf("management profile is incomplete (missing: %s)", strings.Join(missing, ", "))
	}
	return client.NewManagementClient(cfg.GetServerAddr(), cfg.GetScheme(), cfg.AuthType, cfg.Username, cfg.Password, cfg.Token)
}

func runManagementCommand(cmd *cobra.Command, name string, options *managementOptions) error {
	if err := validateManagementOptions(name, options); err != nil {
		return err
	}
	c, err := loadManagementClient(cmd)
	if err != nil {
		return err
	}
	var result any
	query := client.AuthListOptions{Username: options.user, Role: options.role, Search: options.search, Page: options.page, Size: options.size, All: options.all}
	switch name {
	case "user-list":
		result, err = c.ListUsers(query)
	case "role-list":
		result, err = c.ListRoles(query)
	case "permission-list":
		result, err = c.ListPermissions(query)
	default:
		result, err = runManagementChange(c, name, options)
	}
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(cmd.OutOrStdout())
	if options.output == "pretty" {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(result)
}

func runManagementChange(c *client.NacosClient, name string, options *managementOptions) (*managementChange, error) {
	add := name == "role-bind" || name == "permission-add"
	method := http.MethodDelete
	if add {
		method = http.MethodPost
	}
	collection := "permission"
	params := url.Values{"role": {options.role}}
	if strings.HasPrefix(name, "role-") {
		collection = "role"
		params.Set("username", options.user)
	} else {
		params.Set("resource", options.resource)
		params.Set("action", options.action)
	}
	result := &managementChange{Operation: name, Endpoint: c.BaseURL() + "/nacos/v3/auth/" + collection, Method: method, Parameters: params, DryRun: options.dryRun, Status: "planned"}
	if options.dryRun {
		return result, nil
	}
	var err error
	switch name {
	case "role-bind":
		result.Changed, err = c.BindRole(options.role, options.user)
	case "role-unbind":
		result.Changed, err = c.UnbindRole(options.role, options.user)
	case "permission-add":
		result.Changed, err = c.AddPermission(options.role, options.resource, options.action)
	case "permission-remove":
		result.Changed, err = c.RemovePermission(options.role, options.resource, options.action)
	}
	if err != nil {
		return nil, err
	}
	result.Verified = true
	result.Status = "changed"
	if !result.Changed {
		result.Status = "already_absent"
		if add {
			result.Status = "already_present"
		}
	}
	return result, nil
}

func init() {
	for _, name := range managementCommandNames {
		rootCmd.AddCommand(newManagementCommand(name))
	}
}
