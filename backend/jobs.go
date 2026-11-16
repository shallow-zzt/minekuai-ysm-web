package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

type Job struct {
	ID        string    `json:"id"`
	File      string    `json:"file,omitempty"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// Separate immutable request/result files avoid replacing an existing file on Windows.
// A crash between them remains visibly pending, and is never automatically replayed.
func (a *App) writeJob(job Job, result bool) error {
	if err := os.MkdirAll(a.config.jobDir, 0700); err != nil {
		return err
	}
	suffix := ".json"
	if result {
		suffix = ".result.json"
	}
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(a.config.jobDir, ".job-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(a.config.jobDir, job.ID+suffix))
}

func (a *App) readJob(id string) (Job, error) {
	var job Job
	b, err := os.ReadFile(filepath.Join(a.config.jobDir, id+".result.json"))
	if os.IsNotExist(err) {
		b, err = os.ReadFile(filepath.Join(a.config.jobDir, id+".json"))
	}
	if err != nil {
		return job, err
	}
	if len(b) > 16384 {
		return job, fmt.Errorf("oversized local job")
	}
	err = json.Unmarshal(b, &job)
	return job, err
}

func (a *App) submitReload(file string) Job {
	a.reloadMu.Lock()
	defer a.reloadMu.Unlock()
	job := Job{ID: newID(), File: file, Status: "pending", CreatedAt: time.Now().UTC(), Message: "尚无最终提交结果；可能正在提交或服务曾中断。请先查看麦块控制台，再按需手动重载。"}
	if err := a.writeJob(job, false); err != nil {
		log.Printf("create reload record %s: %v", job.ID, err)
		job.Status, job.Message = "failed", "本地提交记录无法保存，未发送重载指令。请检查 data/jobs 目录权限后重试。"
		if file != "" {
			job.Status = "uploaded"
			job.Message = "文件已上传，但本地记录无法保存，未发送重载指令。请检查 data/jobs 后手动重载，无需重复上传。"
		}
		return job
	}
	if time.Since(a.lastReload) < 10*time.Second {
		job.Status, job.Message = "failed", "重载操作间隔至少 10 秒，本次指令未发送；请稍后手动重载。已上传文件无需重复上传。"
	} else {
		a.lastReload = time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := a.panel.Reload(ctx)
		cancel()
		job.Status, job.Message = "submitted", "麦块 API 已接受 ysm model reload 指令；游戏内是否成功请查看控制台或模型列表。"
		if err != nil {
			job.Status, job.Message = "failed", err.Error()
			var panelErr *PanelError
			if errors.As(err, &panelErr) && panelErr.Unknown {
				job.Status = "unknown"
			}
		}
	}
	if file != "" && job.Status != "submitted" {
		job.Message = "文件已上传，无需重复上传。" + job.Message
	}
	if err := a.writeJob(job, true); err != nil {
		log.Printf("persist reload result %s: %v", job.ID, err)
		job.Message += " 本次结果未能写入本地记录，请保留此提示；后续查询可能仍显示等待确认。"
	}
	return job
}
