package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var hostKeyMu sync.Mutex

// With no configured fingerprint, trust the first authenticated connection and
// persist its key. Subsequent connections must match, including after a restart.
func checkHostKey(c Config, fingerprint string, remember bool) error {
	if c.HostKeySHA256 != "" {
		if fingerprint != c.HostKeySHA256 {
			return fmt.Errorf("SFTP host key fingerprint mismatch")
		}
		return nil
	}
	if c.hostKeyDir == "" {
		return fmt.Errorf("SFTP host key storage directory is missing")
	}
	hostKeyMu.Lock()
	defer hostKeyMu.Unlock()
	addressHash := sha256.Sum256([]byte(strings.ToLower(c.SFTPAddress)))
	filename := filepath.Join(c.hostKeyDir, fmt.Sprintf("%x.sha256", addressHash))
	saved, err := os.ReadFile(filename)
	if os.IsNotExist(err) && !remember {
		return nil
	}
	if os.IsNotExist(err) {
		if err = os.MkdirAll(c.hostKeyDir, 0700); err != nil {
			return err
		}
		// Exclusive creation preserves another process's first key. Interrupted or
		// incomplete records fail closed instead of trusting a new identity.
		record, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil && !os.IsExist(err) {
			return err
		}
		if err == nil {
			_, writeErr := record.WriteString(fingerprint + "\n")
			syncErr := record.Sync()
			closeErr := record.Close()
			if writeErr != nil {
				return writeErr
			}
			if syncErr != nil {
				return syncErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
		saved, err = os.ReadFile(filename)
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(saved)) != fingerprint {
		return fmt.Errorf("SFTP server identity changed; verify the server before resetting %s", filename)
	}
	return nil
}
