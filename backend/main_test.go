package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strings"
	"sync"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// A real SSH/SFTP transport with an isolated in-memory filesystem exercises the normal HTTP entry points.
func testApp(t *testing.T) *App {
	t.Helper()
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(private)
	sc := &ssh.ServerConfig{PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if meta.User() == "test" && string(password) == "test" {
			return nil, nil
		}
		return nil, fmt.Errorf("bad login")
	}}
	sc.AddHostKey(signer)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { listener.Close() })
	handlers := sftp.InMemHandler()
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				sshConn, channels, requests, e := ssh.NewServerConn(conn, sc)
				if e != nil {
					conn.Close()
					return
				}
				defer sshConn.Close()
				go ssh.DiscardRequests(requests)
				for newChannel := range channels {
					ch, reqs, e := newChannel.Accept()
					if e != nil {
						return
					}
					go func() {
						for request := range reqs {
							request.Reply(request.Type == "subsystem", nil)
						}
					}()
					go func() { server := sftp.NewRequestServer(ch, handlers); defer server.Close(); server.Serve() }()
				}
			}()
		}
	}()
	return &App{config: Config{SFTPAddress: listener.Addr().String(), SFTPUser: "test", SFTPPassword: "test", HostKeySHA256: ssh.FingerprintSHA256(signer.PublicKey()), RemoteRoot: "/", MaxUploadMB: 1, MaxLibraryMB: 1, UploadToken: strings.Repeat("p", 24), AdminToken: strings.Repeat("a", 24)}, slots: make(chan struct{}, 4)}
}
func sendUpload(a *App, name string, data []byte) *httptest.ResponseRecorder {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, _ := writer.CreateFormFile("file", name)
	file.Write(data)
	writer.Close()
	req := httptest.NewRequest("POST", "/api/models", &body)
	req.Header.Set("Authorization", "Bearer "+a.config.UploadToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	a.handler().ServeHTTP(response, req)
	return response
}
func TestUploadQueueAndAck(t *testing.T) {
	a := testApp(t)
	data := bytes.Repeat([]byte("ysm-test"), 32)
	response := sendUpload(a, "测试模型.ysm", data)
	if response.Code != 201 {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	var job map[string]string
	json.Unmarshal(response.Body.Bytes(), &job)
	r, e := connect(context.Background(), a.config)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	models, bridge := a.roots()
	f, e := r.Open(path.Join(models, job["file"]))
	if e != nil {
		t.Fatal(e)
	}
	got, _ := io.ReadAll(f)
	f.Close()
	if !bytes.Equal(got, data) {
		t.Fatal("uploaded bytes changed")
	}
	if _, e = r.Stat(path.Join(bridge, "requests", job["id"]+".req")); e != nil {
		t.Fatal(e)
	}
	entries, e := r.ReadDir(path.Join(bridge, "staging"))
	if e != nil || len(entries) != 0 {
		t.Fatal("staging not empty")
	}
	ack := []byte(`{"status":"dispatched","message":"test"}`)
	if e = r.publish(path.Join(bridge, "staging", "ack.tmp"), path.Join(bridge, "acks", job["id"]+".json"), bytes.NewReader(ack)); e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest("GET", "/api/jobs/"+job["id"], nil)
	req.Header.Set("Authorization", "Bearer "+a.config.UploadToken)
	out := httptest.NewRecorder()
	a.handler().ServeHTTP(out, req)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "dispatched") {
		t.Fatal(out.Body.String())
	}
}
func TestAuthFingerprintAndConcurrentQuota(t *testing.T) {
	a := testApp(t)
	for _, url := range []string{"/api/models", "/api/reload"} {
		req := httptest.NewRequest("POST", url, nil)
		out := httptest.NewRecorder()
		a.handler().ServeHTTP(out, req)
		if out.Code != 401 {
			t.Fatal(out.Code)
		}
	}
	req := httptest.NewRequest("POST", "/api/reload", nil)
	req.Header.Set("Authorization", "Bearer "+a.config.UploadToken)
	out := httptest.NewRecorder()
	a.handler().ServeHTTP(out, req)
	if out.Code != 401 {
		t.Fatal("player can reload")
	}
	bad := a.config
	bad.HostKeySHA256 = "SHA256:invalid"
	if r, e := connect(context.Background(), bad); e == nil {
		r.Close()
		t.Fatal("accepted wrong host key")
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- sendUpload(a, "same.ysm", bytes.Repeat([]byte("x"), 700000)).Code }()
	}
	wg.Wait()
	close(codes)
	successes, conflicts := 0, 0
	for code := range codes {
		if code == http.StatusCreated {
			successes++
		}
		if code == 409 {
			conflicts++
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("quota results %d %d", successes, conflicts)
	}
}
func TestZipValidation(t *testing.T) {
	for _, name := range []string{"../escape.json", "/absolute.json", "a/../../x", "a\\b.json", "C:/x.json", "model.json"} {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			z := zip.NewWriter(&b)
			f, _ := z.Create(name)
			f.Write([]byte(`{"model":"test"}`))
			z.Close()
			tmp, e := os.CreateTemp(t.TempDir(), "*.zip")
			if e != nil {
				t.Fatal(e)
			}
			defer tmp.Close()
			tmp.Write(b.Bytes())
			e = validateUpload(tmp, "model.zip", int64(b.Len()))
			if (e == nil) != (name == "model.json") {
				t.Fatalf("validation %v", e)
			}
		})
	}
}

func TestStaticHealthAndPendingJob(t *testing.T) {
	a := testApp(t)
	a.config.WebDir = t.TempDir()
	if err := os.WriteFile(path.Join(a.config.WebDir, "index.html"), []byte("wardrobe-page"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"/", "/api/health"} {
		request := httptest.NewRequest("GET", endpoint, nil)
		response := httptest.NewRecorder()
		a.handler().ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("%s: %d", endpoint, response.Code)
		}
	}
	response := sendUpload(a, "pending.ysm", bytes.Repeat([]byte("x"), 32))
	var uploaded map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &uploaded); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/api/jobs/"+uploaded["id"], nil)
	request.Header.Set("Authorization", "Bearer "+a.config.UploadToken)
	result := httptest.NewRecorder()
	a.handler().ServeHTTP(result, request)
	if result.Code != 200 || !strings.Contains(result.Body.String(), "pending") {
		t.Fatal(result.Body.String())
	}
	rejected := sendUpload(a, "too-big.ysm", bytes.Repeat([]byte("x"), (1<<20)+1))
	if rejected.Code != 413 {
		t.Fatalf("oversized file accepted: %d", rejected.Code)
	}
}
