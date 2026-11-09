package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type Config struct {
	Listen        string `json:"listen"`
	WebDir        string `json:"web_dir"`
	UploadToken   string `json:"upload_token"`
	AdminToken    string `json:"admin_token"`
	SFTPAddress   string `json:"sftp_address"`
	SFTPUser      string `json:"sftp_user"`
	SFTPPassword  string `json:"sftp_password"`
	SFTPKeyFile   string `json:"sftp_key_file"`
	HostKeySHA256 string `json:"host_key_sha256"`
	RemoteRoot    string `json:"remote_root"`
	MaxUploadMB   int64  `json:"max_upload_mb"`
	MaxLibraryMB  int64  `json:"max_library_mb"`
	hostKeyDir    string
}

func loadConfig(filename string) (Config, error) {
	c := Config{Listen: "127.0.0.1:18081", WebDir: "web/out", RemoteRoot: "/", MaxUploadMB: 64, MaxLibraryMB: 2048}
	b, e := os.ReadFile(filename)
	if e != nil {
		return c, e
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return c, e
	}
	for key, target := range map[string]*string{"YSM_UPLOAD_TOKEN": &c.UploadToken, "YSM_ADMIN_TOKEN": &c.AdminToken, "YSM_SFTP_PASSWORD": &c.SFTPPassword} {
		if v := os.Getenv(key); v != "" {
			*target = v
		}
	}
	if c.SFTPAddress == "" || c.SFTPUser == "" {
		return c, fmt.Errorf("configure SFTP address and user")
	}
	if c.HostKeySHA256 != "" && !strings.HasPrefix(c.HostKeySHA256, "SHA256:") {
		return c, fmt.Errorf("optional host_key_sha256 must start with SHA256:")
	}
	c.hostKeyDir = filepath.Join(filepath.Dir(filename), "data", "sftp-host-keys")
	if c.SFTPPassword == "" && c.SFTPKeyFile == "" {
		return c, fmt.Errorf("configure SFTP password or private key file")
	}
	if !strings.HasPrefix(c.RemoteRoot, "/") || strings.Contains(c.RemoteRoot, "\\") {
		return c, fmt.Errorf("remote_root must be an absolute SFTP path")
	}
	c.RemoteRoot = path.Clean(c.RemoteRoot)
	if c.MaxUploadMB < 1 || c.MaxUploadMB > 64 || c.MaxLibraryMB < c.MaxUploadMB {
		return c, fmt.Errorf("max_upload_mb must be 1..64 and max_library_mb must be larger")
	}
	return c, nil
}
