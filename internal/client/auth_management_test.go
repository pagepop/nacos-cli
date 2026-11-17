package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func managementTestClient(t *testing.T, handler http.HandlerFunc) *NacosClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c, err := NewManagementClient(strings.TrimPrefix(server.URL, "http://"), "http", AuthTypeToken, "", "", "controlled-token")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestManagementUsersArePaginatedAndSanitized(t *testing.T) {
	requests := 0
	c := managementTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/nacos/v3/auth/user/list" || r.Header.Get("Authorization") != "Bearer controlled-token" {
			t.Errorf("unexpected request %s or authentication", r.URL.Path)
		}
		if r.URL.Query().Get("username") != "LDAP_engineer7" || r.URL.Query().Get("search") != "accurate" {
			t.Errorf("incorrect filters: %s", r.URL.RawQuery)
		}
		fmt.Fprintf(w, `{"code":0,"data":{"totalCount":2,"pageNumber":%s,"pageItems":[{"username":"engineer%s","password":"must-never-be-returned"}]}}`, r.URL.Query().Get("pageNo"), r.URL.Query().Get("pageNo"))
	})
	result, err := c.ListUsers(AuthListOptions{Username: "LDAP_engineer7", Page: 1, Size: 1, All: true})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(result)
	if requests != 2 || !result.Complete || len(result.PageItems) != 2 || strings.Contains(string(body), "password") || strings.Contains(string(body), "must-never") {
		t.Fatalf("unexpected sanitized result: %s, requests=%d", body, requests)
	}
}

func TestManagementRejectsFailedAndIncompleteResponses(t *testing.T) {
	for _, body := range []string{
		`{"code":403,"message":"secret-body"}`,
		`{"data":{"totalCount":0,"pageItems":[]}}`,
		`{"code":0,"data":{"totalCount":0}}`,
		`{"code":0,"data":{"totalCount":1,"pageItems":[]}}`,
		`{"code":0,"data":{"totalCount":1,"pageItems":[{"role":"R"}]}}`,
	} {
		t.Run(body, func(t *testing.T) {
			c := managementTestClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
			_, err := c.ListRoles(AuthListOptions{Page: 1, Size: 100, All: true})
			if err == nil || strings.Contains(err.Error(), "secret-body") {
				t.Fatalf("expected safe failure, got %v", err)
			}
		})
	}
}

func TestManagementRoleChangesHaveExactTargetsAndReadback(t *testing.T) {
	present := false
	writes := 0
	c := managementTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.Query().Get("username") != "LDAP_engineer7" || r.URL.Query().Get("role") != "ROLE_EXAMPLE" {
				t.Error("missing exact role filters")
			}
			items := `[]`
			count := 0
			if present {
				items = `[{"username":"LDAP_engineer7","role":"ROLE_EXAMPLE"}]`
				count = 1
			}
			fmt.Fprintf(w, `{"code":0,"data":{"totalCount":%d,"pageItems":%s}}`, count, items)
			return
		}
		writes++
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("username") != "LDAP_engineer7" || r.Form.Get("role") != "ROLE_EXAMPLE" {
			t.Error("missing mutation target")
		}
		present = r.Method == http.MethodPost
		fmt.Fprint(w, `{"code":0,"data":"ok"}`)
	})
	changed, err := c.BindRole("ROLE_EXAMPLE", "LDAP_engineer7")
	if err != nil || !changed || !present {
		t.Fatalf("bind: changed=%v err=%v", changed, err)
	}
	changed, err = c.BindRole("ROLE_EXAMPLE", "LDAP_engineer7")
	if err != nil || changed || writes != 1 {
		t.Fatalf("existing binding: changed=%v writes=%d err=%v", changed, writes, err)
	}
	changed, err = c.UnbindRole("ROLE_EXAMPLE", "LDAP_engineer7")
	if err != nil || !changed || present || writes != 2 {
		t.Fatalf("unbind: changed=%v writes=%d err=%v", changed, writes, err)
	}
	if _, err = c.UnbindRole("ROLE_EXAMPLE", ""); err == nil || writes != 2 {
		t.Fatal("empty username must fail before writing")
	}
}

func TestManagementPermissionWriteRequiresSuccessfulReadback(t *testing.T) {
	writes := 0
	c := managementTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"code":0,"data":{"totalCount":0,"pageItems":[]}}`)
			return
		}
		writes++
		_ = r.ParseForm()
		if r.Form.Get("resource") != "online:DEFAULT_GROUP:config/example" || r.Form.Get("action") != "rw" {
			t.Error("incorrect permission")
		}
		fmt.Fprint(w, `{"code":0,"data":"ok"}`)
	})
	_, err := c.AddPermission("ROLE_EXAMPLE", "online:DEFAULT_GROUP:config/example", "rw")
	if err == nil || writes != 1 || !strings.Contains(err.Error(), "readback") {
		t.Fatalf("false success after write: %v, writes=%d", err, writes)
	}
}

func TestManagementPaginationDetectsDrift(t *testing.T) {
	c := managementTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		total := 2
		if r.URL.Query().Get("pageNo") == "2" {
			total = 3
		}
		fmt.Fprintf(w, `{"code":0,"data":{"totalCount":%d,"pageItems":[{"username":"user%s"}]}}`, total, r.URL.Query().Get("pageNo"))
	})
	if _, err := c.ListUsers(AuthListOptions{Page: 1, Size: 1, All: true}); err == nil {
		t.Fatal("changed pagination total must fail")
	}
}

func TestManagementDoesNotExposeHTTPErrorBodies(t *testing.T) {
	c := managementTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"private-password-and-token"}`)
	})
	_, err := c.ListUsers(AuthListOptions{Page: 1, Size: 100})
	if err == nil || strings.Contains(err.Error(), "private-password") || !strings.Contains(err.Error(), "403") {
		t.Fatalf("unsafe or missing error: %v", err)
	}
}

func TestManagementPermissionChangesRoundTrip(t *testing.T) {
	present := false
	writes := 0
	c := managementTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			items := `[]`
			count := 0
			if present {
				items = `[{"role":"ROLE_EXAMPLE","resource":"online:DEFAULT_GROUP:config/example","action":"rw"}]`
				count = 1
			}
			fmt.Fprintf(w, `{"code":0,"data":{"totalCount":%d,"pageItems":%s}}`, count, items)
			return
		}
		writes++
		_ = r.ParseForm()
		if r.URL.Path != "/nacos/v3/auth/permission" || r.Form.Get("role") != "ROLE_EXAMPLE" || r.Form.Get("action") != "rw" || r.Form.Get("resource") != "online:DEFAULT_GROUP:config/example" {
			t.Error("incorrect permission mutation")
		}
		present = r.Method == http.MethodPost
		fmt.Fprint(w, `{"code":0,"data":"ok"}`)
	})
	for _, add := range []bool{true, true, false, false} {
		changed, err := c.changePermission("ROLE_EXAMPLE", "online:DEFAULT_GROUP:config/example", "rw", add)
		if err != nil {
			t.Fatal(err)
		}
		if changed && present != add {
			t.Fatal("write was not verified")
		}
	}
	if writes != 2 {
		t.Fatalf("expected two writes, got %d", writes)
	}
}

func TestManagementLoginDoesNotFallBackOrPrintBodies(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/nacos/v3/auth/user/login" {
			t.Error("unexpected fallback")
		}
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"private-password"}`)
	}))
	defer server.Close()
	c, err := NewManagementClient(strings.TrimPrefix(server.URL, "http://"), "http", AuthTypeNacos, "operator", "controlled-password", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ListUsers(AuthListOptions{Page: 1, Size: 100})
	if err == nil || strings.Contains(err.Error(), "private-password") || requests != 1 {
		t.Fatalf("unsafe login failure: %v", err)
	}
}

func TestManagementRedirectDoesNotRepeatWrites(t *testing.T) {
	destinationCalls := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalls++ }))
	defer destination.Close()
	c := managementTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	})
	_, err := c.managementRequest(http.MethodPost, "role", map[string][]string{"role": {"R"}, "username": {"user"}})
	if err == nil || destinationCalls != 0 {
		t.Fatalf("followed management redirect, err=%v", err)
	}
}
