package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAPIKeyFileAndEnvPrecedence(t *testing.T) {
	t.Setenv("YSM_MINEKUAI_API_KEY", "")
	t.Setenv("YSM_MINEKUAI_INSTANCE_ID", "")
	dir := t.TempDir()
	filename := filepath.Join(dir, "config.json")
	cfg := map[string]any{"sftp_address": "host:22", "sftp_user": "user", "sftp_password": "password", "minekuai_instance_id": "1234abcd", "minekuai_api_key_file": "APIKEY.txt"}
	writeConfig := func() {
		b, _ := json.Marshal(cfg)
		if err := os.WriteFile(filename, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig()
	key := "mkc_identifier.abcdefghijklmnopqrstuvwxyz+/="
	if err := os.WriteFile(filepath.Join(dir, "APIKEY.txt"), []byte("\ufeff"+key+"\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig(filename)
	if err != nil {
		t.Fatal(err)
	}
	if got.MinekuaiAPIKey != key {
		t.Fatal("key was altered")
	}
	if got.Listen != "127.0.0.1:18081" || got.WebDir != filepath.Join(dir, "web", "out") || got.jobDir != filepath.Join(dir, "data", "jobs") {
		t.Fatal("wrong config defaults/relative paths")
	}
	cfg["minekuai_api_key"] = "mkc_inline.value"
	writeConfig()
	got, err = loadConfig(filename)
	if err != nil || got.MinekuaiAPIKey != "mkc_inline.value" {
		t.Fatal("inline key precedence", err)
	}
	t.Setenv("YSM_MINEKUAI_API_KEY", "mkc_environment.value")
	cfg["minekuai_api_key_file"] = "missing-file"
	writeConfig()
	got, err = loadConfig(filename)
	if err != nil || got.MinekuaiAPIKey != "mkc_environment.value" {
		t.Fatal("env key precedence", err)
	}
	t.Setenv("YSM_MINEKUAI_API_KEY", "mkc_line\nAuthorization:bad")
	if _, err = loadConfig(filename); err == nil {
		t.Fatal("accepted multiline credential")
	}
}
