package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// AuthUser deliberately excludes password fields returned by the server.
type AuthUser struct {
	Username string `json:"username"`
}
type AuthRole struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}
type AuthPermission struct {
	Role     string `json:"role"`
	Resource string `json:"resource"`
	Action   string `json:"action"`
}

func (v AuthUser) valid() bool { return validAuthValue(v.Username) }
func (v AuthRole) valid() bool { return validAuthValue(v.Username) && validAuthValue(v.Role) }
func (v AuthPermission) valid() bool {
	return validAuthValue(v.Role) && validAuthValue(v.Resource) && validAuthAction(v.Action)
}

type authRecord interface{ valid() bool }
type AuthPage[T authRecord] struct {
	TotalCount int  `json:"totalCount"`
	PageNumber int  `json:"pageNumber"`
	PageItems  []T  `json:"pageItems"`
	Complete   bool `json:"complete"`
}
type AuthListOptions struct {
	Username string
	Role     string
	Search   string
	Page     int
	Size     int
	All      bool
}

// NewManagementClient reuses profile credentials and the shared HTTP client,
// but keeps v3 auth management non-interactive and free of legacy API fallback.
// Authentication is deferred until the first request so dry runs stay offline.
func NewManagementClient(serverAddr, scheme, authType, username, password, token string) (*NacosClient, error) {
	endpoint, err := url.Parse(scheme + "://" + serverAddr)
	if err != nil || (scheme != "http" && scheme != "https") || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, fmt.Errorf("auth management requires an explicit valid HTTP(S) server address")
	}
	if authType != AuthTypeNacos && authType != AuthTypeToken {
		return nil, fmt.Errorf("auth management supports only nacos or token authentication")
	}
	if (authType == AuthTypeNacos && (username == "" || password == "")) || (authType == AuthTypeToken && token == "") {
		return nil, fmt.Errorf("auth management credentials are incomplete")
	}
	c := &NacosClient{ServerAddr: serverAddr, Scheme: scheme, AuthType: authType, Username: username, Password: password}
	if authType == AuthTypeToken {
		c.AccessToken = token
	}
	return c, nil
}

func validAuthValue(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && !strings.ContainsFunc(value, unicode.IsControl)
}
func validAuthAction(action string) bool { return action == "r" || action == "w" || action == "rw" }

func (c *NacosClient) managementHTTP(req *http.Request) ([]byte, error) {
	// Redirects must not forward credentials or repeat a mutation to another URL.
	transport := *c.HTTPClient()
	transport.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := transport.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Nacos auth management request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, ParseHTTPError(response.StatusCode, nil, "Nacos auth management")
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read Nacos auth management response failed")
	}
	return body, nil
}

func (c *NacosClient) managementLogin() error {
	form := url.Values{"username": {c.Username}, "password": {c.Password}}
	req, err := http.NewRequest(http.MethodPost, c.BaseURL()+"/nacos/v3/auth/user/login", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("invalid Nacos authentication endpoint")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	body, err := c.managementHTTP(req)
	if err != nil {
		return fmt.Errorf("Nacos management login: %w", err)
	}
	var envelope struct {
		Code *int `json:"code"`
	}
	if json.Unmarshal(body, &envelope) != nil || (envelope.Code != nil && *envelope.Code != 0) || !c.applyLoginResponse(body) {
		return fmt.Errorf("Nacos management login returned an invalid token response")
	}
	return nil
}

func (c *NacosClient) managementRequest(method, path string, params url.Values) (json.RawMessage, error) {
	if c.AuthType != AuthTypeNacos && c.AuthType != AuthTypeToken {
		return nil, fmt.Errorf("unsupported auth management authentication")
	}
	if c.AuthType == AuthTypeNacos && (c.AccessToken == "" || (!c.TokenExpireAt.IsZero() && time.Now().After(c.TokenExpireAt))) {
		if err := c.managementLogin(); err != nil {
			return nil, err
		}
	}
	endpoint := c.BaseURL() + "/nacos/v3/auth/" + path
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader(params.Encode())
	} else {
		endpoint += "?" + params.Encode()
	}
	req, err := c.NewAuthedRequest(method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("invalid auth management request")
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := c.managementHTTP(req)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(response, &envelope) != nil || envelope.Code == nil {
		return nil, fmt.Errorf("invalid Nacos auth management response envelope")
	}
	if *envelope.Code != 0 {
		return nil, fmt.Errorf("Nacos auth management returned code %d", *envelope.Code)
	}
	return envelope.Data, nil
}

func listAuthRecords[T authRecord](c *NacosClient, path string, options AuthListOptions) (*AuthPage[T], error) {
	if options.Page < 1 || options.Size < 1 || (options.All && options.Page != 1) {
		return nil, fmt.Errorf("page and size must be positive; --all requires page 1")
	}
	search := options.Search
	if search == "" {
		search = "accurate"
	}
	if search != "accurate" && search != "blur" {
		return nil, fmt.Errorf("search must be accurate or blur")
	}
	result := &AuthPage[T]{PageNumber: options.Page, PageItems: []T{}}
	seen := map[string]bool{}
	for page := options.Page; ; page++ {
		params := url.Values{"pageNo": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(options.Size)}, "search": {search}}
		if path == "user/list" || path == "role/list" {
			params.Set("username", options.Username)
		}
		if path != "user/list" {
			params.Set("role", options.Role)
		}
		data, err := c.managementRequest(http.MethodGet, path, params)
		if err != nil {
			return nil, err
		}
		var batch struct {
			TotalCount *int `json:"totalCount"`
			PageItems  []T  `json:"pageItems"`
		}
		if json.Unmarshal(data, &batch) != nil || batch.TotalCount == nil || *batch.TotalCount < 0 || batch.PageItems == nil || len(batch.PageItems) > options.Size || len(batch.PageItems) > *batch.TotalCount {
			return nil, fmt.Errorf("invalid Nacos auth management pagination")
		}
		if page == options.Page {
			result.TotalCount = *batch.TotalCount
		} else if result.TotalCount != *batch.TotalCount {
			return nil, fmt.Errorf("auth management pagination changed; query again")
		}
		for _, item := range batch.PageItems {
			key, _ := json.Marshal(item)
			if !item.valid() || seen[string(key)] {
				return nil, fmt.Errorf("invalid or repeated auth management record")
			}
			seen[string(key)] = true
			result.PageItems = append(result.PageItems, item)
		}
		if len(result.PageItems) > result.TotalCount {
			return nil, fmt.Errorf("auth management pagination exceeded totalCount")
		}
		result.Complete = options.Page == 1 && len(result.PageItems) == result.TotalCount
		if result.Complete || !options.All {
			return result, nil
		}
		if len(batch.PageItems) == 0 {
			return nil, fmt.Errorf("auth management pagination is incomplete")
		}
	}
}

func (c *NacosClient) ListUsers(options AuthListOptions) (*AuthPage[AuthUser], error) {
	return listAuthRecords[AuthUser](c, "user/list", options)
}
func (c *NacosClient) ListRoles(options AuthListOptions) (*AuthPage[AuthRole], error) {
	return listAuthRecords[AuthRole](c, "role/list", options)
}
func (c *NacosClient) ListPermissions(options AuthListOptions) (*AuthPage[AuthPermission], error) {
	return listAuthRecords[AuthPermission](c, "permission/list", options)
}

func (c *NacosClient) BindRole(role, username string) (bool, error) {
	return c.changeRole(role, username, true)
}
func (c *NacosClient) UnbindRole(role, username string) (bool, error) {
	return c.changeRole(role, username, false)
}
func (c *NacosClient) AddPermission(role, resource, action string) (bool, error) {
	return c.changePermission(role, resource, action, true)
}
func (c *NacosClient) RemovePermission(role, resource, action string) (bool, error) {
	return c.changePermission(role, resource, action, false)
}

func (c *NacosClient) changeRole(role, username string, add bool) (bool, error) {
	if !validAuthValue(role) || !validAuthValue(username) {
		return false, fmt.Errorf("role and full target username are required")
	}
	params := url.Values{"role": {role}, "username": {username}}
	present := func() (bool, error) {
		rows, err := c.ListRoles(AuthListOptions{Username: username, Role: role, Page: 1, Size: 100, All: true})
		if err != nil {
			return false, err
		}
		for _, row := range rows.PageItems {
			if row.Role != role || row.Username != username {
				return false, fmt.Errorf("role lookup returned a different target")
			}
		}
		return len(rows.PageItems) > 0, nil
	}
	return c.changeAuthRecord("role", params, add, present)
}

func (c *NacosClient) changePermission(role, resource, action string, add bool) (bool, error) {
	if !validAuthValue(role) || !validAuthValue(resource) || !validAuthAction(action) {
		return false, fmt.Errorf("role, resource and action (r, w or rw) are required")
	}
	params := url.Values{"role": {role}, "resource": {resource}, "action": {action}}
	present := func() (bool, error) {
		rows, err := c.ListPermissions(AuthListOptions{Role: role, Page: 1, Size: 100, All: true})
		if err != nil {
			return false, err
		}
		found := false
		for _, row := range rows.PageItems {
			if row.Role != role {
				return false, fmt.Errorf("permission lookup returned a different role")
			}
			if row.Resource == resource && row.Action == action {
				found = true
			}
		}
		return found, nil
	}
	return c.changeAuthRecord("permission", params, add, present)
}

func (c *NacosClient) changeAuthRecord(path string, params url.Values, add bool, present func() (bool, error)) (bool, error) {
	before, err := present()
	if err != nil {
		return false, err
	}
	if before == add {
		return false, nil
	}
	method := http.MethodDelete
	if add {
		method = http.MethodPost
	}
	if _, err := c.managementRequest(method, path, params); err != nil {
		return false, fmt.Errorf("auth management write result is unconfirmed; inspect current records before retrying: %w", err)
	}
	after, err := present()
	if err != nil {
		return true, fmt.Errorf("write accepted but readback failed; inspect current records before retrying: %w", err)
	}
	if after != add {
		return true, fmt.Errorf("write accepted but readback did not match the requested state")
	}
	return true, nil
}
