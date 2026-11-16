package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Panel interface {
	Status(context.Context) (PanelStatus, error)
	Reload(context.Context) error
}

type App struct {
	config       Config
	writes       sync.Mutex
	slots        chan struct{}
	panel        Panel
	reloadMu     sync.Mutex
	lastReload   time.Time
	panelMu      sync.Mutex
	panelChecked time.Time
	panelCached  PanelStatus
}

var jobID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func newID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}
func sameToken(a, b string) bool {
	x := sha256.Sum256([]byte(a))
	y := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}
func (a *App) authorize(next http.HandlerFunc, admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		header := r.Header.Get("Authorization")
		token := strings.TrimPrefix(header, "Bearer ")
		required := a.config.UploadToken
		if admin && a.config.AdminToken != "" {
			required = a.config.AdminToken
		}
		isAdmin := a.config.AdminToken != "" && sameToken(token, a.config.AdminToken)
		if required != "" && (!strings.HasPrefix(header, "Bearer ") || (!isAdmin && !sameToken(token, required))) {
			fail(w, 401, "口令无效或权限不足")
			return
		}
		select {
		case a.slots <- struct{}{}:
			defer func() { <-a.slots }()
		default:
			fail(w, 429, "服务忙，请稍后重试")
			return
		}
		next(w, r)
	}
}
func (a *App) remote(w http.ResponseWriter, r *http.Request) (*Remote, bool) {
	remote, e := connect(r.Context(), a.config)
	if e != nil {
		log.Printf("SFTP connect: %v", e)
		fail(w, 502, "SFTP 连接失败，请联系服主检查连接配置和服务端日志")
		return nil, false
	}
	return remote, true
}
func (a *App) roots() (string, string) {
	return path.Join(a.config.RemoteRoot, "config/yes_steve_model/custom"), path.Join(a.config.RemoteRoot, "config/ysm_web_uploads")
}
func prepare(remote *Remote, models, uploads string) error {
	for _, p := range []string{models, path.Join(uploads, "staging")} {
		if e := remote.MkdirAll(p); e != nil {
			return e
		}
	}
	return nil
}
func (a *App) list(w http.ResponseWriter, r *http.Request) {
	remote, ok := a.remote(w, r)
	if !ok {
		return
	}
	defer remote.Close()
	models, _ := a.roots()
	files, total, e := remote.models(models)
	if os.IsNotExist(e) {
		files = []ModelFile{}
		e = nil
	}
	if e != nil {
		log.Printf("list: %v", e)
		fail(w, 502, "读取模型目录失败")
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Modified.After(files[j].Modified) })
	jsonResponse(w, 200, map[string]any{"files": files, "total_bytes": total, "max_upload_bytes": a.config.MaxUploadMB << 20, "max_library_bytes": a.config.MaxLibraryMB << 20})
}
func (a *App) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, (a.config.MaxUploadMB<<20)+(1<<20))
	reader, e := r.MultipartReader()
	if e != nil {
		fail(w, 400, "需要 multipart/form-data 文件上传")
		return
	}
	part, e := reader.NextPart()
	if e != nil || part.FormName() != "file" {
		fail(w, 400, "需要名为 file 的文件字段")
		return
	}
	name := part.FileName()
	if !safeName(name) {
		fail(w, 400, "仅支持 .ysm 和 .zip 模型文件")
		return
	}
	temp, e := os.CreateTemp("", "ysm-upload-*")
	if e != nil {
		fail(w, 500, "创建上传缓存失败")
		return
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	size, e := io.Copy(temp, io.LimitReader(part, (a.config.MaxUploadMB<<20)+1))
	if e != nil || size > a.config.MaxUploadMB<<20 {
		fail(w, 413, "文件超出大小限制或传输中断")
		return
	}
	if e = validateUpload(temp, name, size); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if _, e = reader.NextPart(); e != io.EOF {
		fail(w, 400, "每次只允许上传一个文件")
		return
	}
	if _, e = temp.Seek(0, 0); e != nil {
		fail(w, 500, "读取缓存失败")
		return
	}
	a.writes.Lock()
	defer a.writes.Unlock()
	remote, ok := a.remote(w, r)
	if !ok {
		return
	}
	defer remote.Close()
	models, uploads := a.roots()
	if e = prepare(remote, models, uploads); e != nil {
		log.Printf("prepare: %v", e)
		fail(w, 502, "创建远端目录失败，请检查权限")
		return
	}
	_, total, e := remote.models(models)
	if e != nil {
		fail(w, 502, "检查容量失败")
		return
	}
	if total+size > a.config.MaxLibraryMB<<20 {
		fail(w, 409, "模型库已达到容量上限，请联系服主")
		return
	}
	id := newID()
	remoteName := id + "_" + name
	if e = remote.publish(path.Join(uploads, "staging", id+".upload.part"), path.Join(models, remoteName), temp); e != nil {
		log.Printf("publish %s: %v", id, e)
		fail(w, 502, "模型发布失败，请刷新文件列表核实后重试")
		return
	}
	// Finish submission even if the browser disconnects after the file is committed.
	job := a.submitReload(remoteName)
	jsonResponse(w, 201, job)
}
func (a *App) job(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !jobID.MatchString(id) {
		fail(w, 400, "请求编号无效")
		return
	}
	record, err := a.readJob(id)
	if os.IsNotExist(err) {
		fail(w, 404, "未找到这次重载记录")
		return
	}
	if err != nil {
		fail(w, 500, "读取本地重载记录失败")
		return
	}
	jsonResponse(w, 200, record)
}
func (a *App) reload(w http.ResponseWriter, r *http.Request) {
	// Reload needs only the panel API; SFTP availability does not block it.
	jsonResponse(w, 202, a.submitReload(""))
}
func (a *App) panelStatus(w http.ResponseWriter, r *http.Request) {
	a.panelMu.Lock()
	defer a.panelMu.Unlock()
	if time.Since(a.panelChecked) >= 10*time.Second {
		state, err := a.panel.Status(r.Context())
		if err != nil {
			state = PanelStatus{State: "error", Message: err.Error()}
		}
		a.panelCached, a.panelChecked = state, time.Now()
	}
	jsonResponse(w, 200, a.panelCached)
}
func (a *App) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /api/access", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		jsonResponse(w, 200, map[string]bool{"upload_required": a.config.UploadToken != "", "reload_required": a.config.AdminToken != "" || a.config.UploadToken != ""})
	})
	mux.HandleFunc("GET /api/panel", a.authorize(a.panelStatus, false))
	mux.HandleFunc("GET /api/models", a.authorize(a.list, false))
	mux.HandleFunc("POST /api/models", a.authorize(a.upload, false))
	mux.HandleFunc("GET /api/jobs/{id}", a.authorize(a.job, false))
	mux.HandleFunc("POST /api/reload", a.authorize(a.reload, true))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "API 不存在") })
	mux.Handle("/", http.FileServer(http.Dir(a.config.WebDir)))
	crossOrigin := http.NewCrossOriginProtection()
	crossOrigin.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fail(w, 403, "请从本站页面提交操作") }))
	protected := crossOrigin.Handler(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		protected.ServeHTTP(w, r)
	})
}
func main() {
	cfgPath := flag.String("config", "config.json", "configuration file")
	flag.Parse()
	logDir := filepath.Join(filepath.Dir(*cfgPath), "data", "logs")
	if e := os.MkdirAll(logDir, 0700); e != nil {
		log.Fatal(e)
	}
	logPath := filepath.Join(logDir, "startup.log")
	logFile, e := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if e != nil {
		log.Fatal(e)
	}
	defer logFile.Close()
	log.SetOutput(io.MultiWriter(os.Stderr, logFile))
	log.Printf("Starting YSM Upload (Minekuai API). Log file: %s", logPath)
	c, e := loadConfig(*cfgPath)
	if e != nil {
		log.Fatal(e)
	}
	if e := os.MkdirAll(c.jobDir, 0700); e != nil {
		log.Fatal("Cannot create local job directory")
	}
	a := &App{config: c, slots: make(chan struct{}, 4), panel: NewPanelClient(c.MinekuaiAPIKey, c.MinekuaiInstanceID)}
	server := &http.Server{Addr: c.Listen, Handler: a.handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 150 * time.Second, WriteTimeout: 180 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	listener, e := net.Listen("tcp", c.Listen)
	if e != nil {
		log.Fatalf("Cannot listen on %s: %v", c.Listen, e)
	}
	log.Printf("YSM Upload listening on http://%s", listener.Addr())
	if e := server.Serve(listener); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
