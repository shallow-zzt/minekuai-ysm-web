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
	"time"

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
	return &App{panel: &fakePanel{}, config: Config{jobDir: t.TempDir(), SFTPAddress: listener.Addr().String(), SFTPUser: "test", SFTPPassword: "test", HostKeySHA256: ssh.FingerprintSHA256(signer.PublicKey()), RemoteRoot: "/", MaxUploadMB: 1, MaxLibraryMB: 1, UploadToken: strings.Repeat("p", 24), AdminToken: strings.Repeat("a", 24)}, slots: make(chan struct{}, 4)}
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

type fakePanel struct {
	err    error
	calls  int
	before func()
}

func (p *fakePanel) Reload(context.Context) error {
	p.calls++
	if p.before != nil {
		p.before()
	}
	return p.err
}
func (p *fakePanel) Status(context.Context) (PanelStatus, error) {
	return PanelStatus{State: "running", Message: "test"}, p.err
}

func TestUploadPublishesBeforeAPIAndPersistsResult(t *testing.T) {
	a := testApp(t)
	data := bytes.Repeat([]byte("ysm-test"), 32)
	panel := a.panel.(*fakePanel)
	panel.before = func() {
		remote, err := connect(context.Background(), a.config)
		if err != nil {
			t.Fatal(err)
		}
		defer remote.Close()
		models, uploads := a.roots()
		files, _, err := remote.models(models)
		if err != nil || len(files) != 1 {
			t.Fatalf("must publish before command: %v %v", files, err)
		}
		f, err := remote.Open(path.Join(models, files[0].Name))
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(f)
		f.Close()
		if !bytes.Equal(got, data) {
			t.Fatal("upload changed")
		}
		entries, err := remote.ReadDir(path.Join(uploads, "staging"))
		if err != nil || len(entries) != 0 {
			t.Fatal("staging not empty")
		}
		if _, err = remote.Stat("/config/ysm_upload_bridge"); !os.IsNotExist(err) {
			t.Fatal("created bridge directory")
		}
	}
	response := sendUpload(a, "test.ysm", data)
	if response.Code != 201 {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	var job Job
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if job.Status != "submitted" || panel.calls != 1 || job.File == "" {
		t.Fatalf("invalid result: %+v", job)
	}
	// A new application instance reads the result with no SFTP/panel dependency.
	restarted := &App{config: Config{jobDir: a.config.jobDir}, slots: make(chan struct{}, 4)}
	request := httptest.NewRequest("GET", "/api/jobs/"+job.ID, nil)
	out := httptest.NewRecorder()
	restarted.handler().ServeHTTP(out, request)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "submitted") {
		t.Fatal(out.Body.String())
	}
}
func TestReloadFailureKeepsUploadAndDoesNotReplay(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint(unknown), func(t *testing.T) {
			a := testApp(t)
			panel := a.panel.(*fakePanel)
			panel.err = &PanelError{Unknown: unknown, Message: "test failure"}
			response := sendUpload(a, "kept.ysm", bytes.Repeat([]byte("x"), 32))
			var job Job
			json.Unmarshal(response.Body.Bytes(), &job)
			want := "failed"
			if unknown {
				want = "unknown"
			}
			if job.Status != want || response.Code != 201 {
				t.Fatalf("%d %+v", response.Code, job)
			}
			remote, err := connect(context.Background(), a.config)
			if err != nil {
				t.Fatal(err)
			}
			defer remote.Close()
			models, _ := a.roots()
			if _, err = remote.Stat(path.Join(models, job.File)); err != nil {
				t.Fatal("lost published model", err)
			}
			for range 3 {
				j, err := a.readJob(job.ID)
				if err != nil || j.Status != want {
					t.Fatal(j, err)
				}
			}
			if panel.calls != 1 {
				t.Fatal("unexpected retry")
			}
		})
	}
}
func TestManualReloadIndependentOfSFTPAndCooldown(t *testing.T) {
	a := testApp(t)
	a.config.SFTPAddress = "invalid:1"
	job := a.submitReload("")
	if job.Status != "submitted" {
		t.Fatal(job)
	}
	job = a.submitReload("")
	if job.Status != "failed" || a.panel.(*fakePanel).calls != 1 {
		t.Fatal("cooldown failed", job)
	}
	a.lastReload = time.Now().Add(-11 * time.Second)
	if a.submitReload("").Status != "submitted" {
		t.Fatal("cooldown did not expire")
	}
}
func TestJobPersistenceFailureDoesNotSend(t *testing.T) {
	a := testApp(t)
	file := path.Join(t.TempDir(), "not-directory")
	os.WriteFile(file, []byte("x"), 0600)
	a.config.jobDir = file
	job := a.submitReload("already-saved.ysm")
	if job.Status != "uploaded" || a.panel.(*fakePanel).calls != 0 {
		t.Fatal(job)
	}
}
func TestInterruptedJobIsNeverReplayed(t *testing.T) {
	a := testApp(t)
	j := Job{ID: newID(), Status: "pending", Message: "interrupted"}
	if err := a.writeJob(j, false); err != nil {
		t.Fatal(err)
	}
	restarted := &App{config: a.config, panel: &fakePanel{}}
	record, err := restarted.readJob(j.ID)
	if err != nil || record.Status != "pending" || restarted.panel.(*fakePanel).calls != 0 {
		t.Fatal(record, err)
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

func TestStaticHealthAndRecordedJob(t *testing.T) {
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
	if result.Code != 200 || !strings.Contains(result.Body.String(), "submitted") {
		t.Fatal(result.Body.String())
	}
	rejected := sendUpload(a, "too-big.ysm", bytes.Repeat([]byte("x"), (1<<20)+1))
	if rejected.Code != 413 {
		t.Fatalf("oversized file accepted: %d", rejected.Code)
	}
}
