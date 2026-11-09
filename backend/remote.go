package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type Remote struct {
	*sftp.Client
	connection *ssh.Client
}

func (r *Remote) Close() { r.Client.Close(); r.connection.Close() }

func connect(ctx context.Context, c Config) (*Remote, error) {
	auth := []ssh.AuthMethod{}
	if c.SFTPKeyFile != "" {
		b, e := os.ReadFile(c.SFTPKeyFile)
		if e != nil {
			return nil, e
		}
		key, e := ssh.ParsePrivateKey(b)
		if e != nil {
			return nil, e
		}
		auth = append(auth, ssh.PublicKeys(key))
	}
	if c.SFTPPassword != "" {
		auth = append(auth, ssh.Password(c.SFTPPassword))
	}
	var fingerprint string
	cfg := &ssh.ClientConfig{User: c.SFTPUser, Auth: auth, Timeout: 15 * time.Second, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		fingerprint = ssh.FingerprintSHA256(key)
		return checkHostKey(c, fingerprint, false)
	}}
	conn, e := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, "tcp", c.SFTPAddress)
	if e != nil {
		return nil, e
	}
	conn.SetDeadline(time.Now().Add(120 * time.Second))
	cc, ch, req, e := ssh.NewClientConn(conn, c.SFTPAddress, cfg)
	if e != nil {
		conn.Close()
		return nil, e
	}
	client := ssh.NewClient(cc, ch, req)
	if e = checkHostKey(c, fingerprint, true); e != nil {
		client.Close()
		return nil, e
	}
	sf, e := sftp.NewClient(client)
	if e != nil {
		client.Close()
		return nil, e
	}
	return &Remote{sf, client}, nil
}

// Temporary files live outside YSM's scanning directories. Rename publishes only complete files.
func (r *Remote) publish(stage, destination string, body io.Reader) error {
	f, e := r.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if e != nil {
		return e
	}
	defer r.Remove(stage)
	_, copyErr := io.Copy(f, body)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return r.Rename(stage, destination)
}

func (r *Remote) smallFile(name string) ([]byte, error) {
	f, e := r.Open(name)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 16385))
	if len(b) > 16384 {
		return nil, fmt.Errorf("oversized bridge record")
	}
	return b, e
}

type ModelFile struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

func (r *Remote) models(root string) ([]ModelFile, int64, error) {
	files := []ModelFile{}
	var total int64
	count := 0
	var walk func(string, int) error
	walk = func(dir string, depth int) error {
		if depth > 16 {
			return fmt.Errorf("model directory exceeds 16 levels")
		}
		entries, e := r.ReadDir(dir)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			count++
			if count > 20000 {
				return fmt.Errorf("model directory exceeds 20000 entries")
			}
			if entry.Mode()&os.ModeSymlink != 0 {
				continue
			}
			full := path.Join(dir, entry.Name())
			if entry.IsDir() {
				if e := walk(full, depth+1); e != nil {
					return e
				}
				continue
			}
			if !entry.Mode().IsRegular() {
				continue
			}
			total += entry.Size()
			ext := strings.ToLower(path.Ext(entry.Name()))
			if ext == ".ysm" || ext == ".zip" {
				files = append(files, ModelFile{strings.TrimPrefix(full, root+"/"), entry.Size(), entry.ModTime().UTC()})
			}
		}
		return nil
	}
	e := walk(root, 0)
	return files, total, e
}

func (r *Remote) request(root, id string) error {
	return r.publish(path.Join(root, "staging", id+".req.part"), path.Join(root, "requests", id+".req"), bytes.NewBufferString("reload\n"))
}
