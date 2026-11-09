package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestOptionalAccess(t *testing.T) {
	cases := []struct {
		name, upload, admin, token string
		reload                     bool
		status                     int
	}{
		{"public upload", "", "", "", false, 204},
		{"public reload", "", "", "", true, 204},
		{"admin only public upload", "", "secret", "", false, 204},
		{"admin only protected reload", "", "secret", "", true, 401},
		{"one password upload", "secret", "", "secret", false, 204},
		{"one password reload", "secret", "", "secret", true, 204},
		{"one password no token", "secret", "", "", true, 401},
		{"split passwords", "player", "admin", "player", true, 401},
		{"admin uploads", "player", "admin", "admin", false, 204},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &App{config: Config{UploadToken: c.upload, AdminToken: c.admin}, slots: make(chan struct{}, 4)}
			request := httptest.NewRequest("POST", "/api/models", nil)
			if c.token != "" {
				request.Header.Set("Authorization", "Bearer "+c.token)
			}
			result := httptest.NewRecorder()
			a.authorize(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }, c.reload)(result, request)
			if result.Code != c.status {
				t.Fatalf("got %d, want %d", result.Code, c.status)
			}
		})
	}
}

func TestFirstAuthenticatedHostKeyIsRemembered(t *testing.T) {
	a := testApp(t)
	expected := a.config.HostKeySHA256
	a.config.HostKeySHA256 = ""
	a.config.hostKeyDir = filepath.Join(t.TempDir(), "hosts")
	bad := a.config
	bad.SFTPPassword = "wrong"
	if remote, err := connect(context.Background(), bad); err == nil {
		remote.Close()
		t.Fatal("bad password accepted")
	}
	if _, err := os.Stat(a.config.hostKeyDir); !os.IsNotExist(err) {
		t.Fatal("failed authentication stored a host key")
	}
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			remote, err := connect(context.Background(), a.config)
			if err != nil {
				failures <- err
				return
			}
			remote.Close()
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(a.config.hostKeyDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("host files: %v %v", entries, err)
	}
	pin := filepath.Join(a.config.hostKeyDir, entries[0].Name())
	stored, err := os.ReadFile(pin)
	if err != nil || strings.TrimSpace(string(stored)) != expected {
		t.Fatal("stored wrong key")
	}
	// A fresh connection reads the saved key, so the policy also survives restarts.
	remote, err := connect(context.Background(), a.config)
	if err != nil {
		t.Fatal(err)
	}
	remote.Close()
	if err = os.WriteFile(pin, []byte("SHA256:different\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if remote, err = connect(context.Background(), a.config); err == nil {
		remote.Close()
		t.Fatal("accepted changed server identity")
	}
}

func TestMinimalConfigAndPublicUpload(t *testing.T) {
	t.Setenv("YSM_UPLOAD_TOKEN", "")
	t.Setenv("YSM_ADMIN_TOKEN", "")
	t.Setenv("YSM_SFTP_PASSWORD", "")
	filename := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(filename, []byte(`{"sftp_address":"example.test:22","sftp_user":"test","sftp_password":"test"}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig(filename)
	if err != nil {
		t.Fatal(err)
	}
	if config.UploadToken != "" || config.AdminToken != "" || config.HostKeySHA256 != "" {
		t.Fatal("minimal config needs extra secrets")
	}
	a := testApp(t)
	a.config.UploadToken = ""
	a.config.AdminToken = ""
	result := sendUpload(a, "public.ysm", bytes.Repeat([]byte("x"), 32))
	if result.Code != 201 {
		t.Fatalf("public upload: %d %s", result.Code, result.Body.String())
	}
	request := httptest.NewRequest("GET", "/api/access", nil)
	result = httptest.NewRecorder()
	a.handler().ServeHTTP(result, request)
	var access map[string]bool
	if err = json.Unmarshal(result.Body.Bytes(), &access); err != nil {
		t.Fatal(err)
	}
	if access["upload_required"] || access["reload_required"] {
		t.Fatal("public site reports required passwords")
	}
	request = httptest.NewRequest("POST", "/api/reload", nil)
	request.Header.Set("Origin", "https://unrelated.test")
	result = httptest.NewRecorder()
	a.handler().ServeHTTP(result, request)
	if result.Code != 403 {
		t.Fatal("cross-origin browser write allowed")
	}
	request = httptest.NewRequest("POST", "/api/reload", nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	result = httptest.NewRecorder()
	a.handler().ServeHTTP(result, request)
	if result.Code != 202 {
		t.Fatalf("public reload: %d %s", result.Code, result.Body.String())
	}
}
