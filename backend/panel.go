package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	panelAPIBase       = "https://api.minekuai.cn/panel/client-api"
	panelTimeout       = 15 * time.Second
	panelResponseLimit = 64 << 10
)

// PanelClient only supports reading this instance and sending the fixed YSM
// reload command. Its origin is deliberately not configurable.
type PanelClient struct {
	apiKey     string
	instanceID string
	client     *http.Client
}

type PanelStatus struct {
	State   string `json:"state"`
	Message string `json:"message"`
}

// Unknown means a reload might already have reached the server. Callers must
// not automatically retry it or report that it definitely failed to arrive.
// Message is always application-owned text, never an upstream response/error.
type PanelError struct {
	Unknown bool
	Message string
}

func (e *PanelError) Error() string { return e.Message }

func NewPanelClient(apiKey, instanceID string) *PanelClient {
	return &PanelClient{
		apiKey:     strings.TrimSpace(apiKey),
		instanceID: strings.TrimSpace(instanceID),
		client: &http.Client{
			Timeout: panelTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Status uses the client-api instance endpoint. Deployed responses expose the
// runtime state inside stats.attributes; older plugin shapes use current_state.
// The older website gateway's /resources endpoint is not used here.
func (p *PanelClient) Status(ctx context.Context) (PanelStatus, error) {
	body, err := p.request(ctx, false)
	if err != nil {
		return PanelStatus{}, err
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil || envelope == nil {
		return PanelStatus{}, panelInvalidResponse(false)
	}
	// Accommodate both the direct client-api shape and a code/data envelope.
	if data, ok := envelope["data"]; ok {
		var nested map[string]json.RawMessage
		if json.Unmarshal(data, &nested) != nil || nested == nil {
			return PanelStatus{}, panelInvalidResponse(false)
		}
		envelope = nested
	}
	if attributes, ok := envelope["attributes"]; ok {
		var nested map[string]json.RawMessage
		if json.Unmarshal(attributes, &nested) != nil || nested == nil {
			return PanelStatus{}, panelInvalidResponse(false)
		}
		envelope = nested
	}
	// The top-level status may be null or administrative metadata. Prefer the
	// daemon's live runtime state when it is supplied in stats.attributes.
	if raw, ok := envelope["stats"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var stats map[string]json.RawMessage
		if json.Unmarshal(raw, &stats) != nil || stats == nil {
			return PanelStatus{}, panelInvalidResponse(false)
		}
		if attributes, ok := stats["attributes"]; ok {
			var nested map[string]json.RawMessage
			if json.Unmarshal(attributes, &nested) != nil || nested == nil {
				return PanelStatus{}, panelInvalidResponse(false)
			}
			stats = nested
		}
		if _, hasState := stats["current_state"]; hasState {
			envelope = stats
		}
	}
	var suspended bool
	if raw, ok := envelope["is_suspended"]; ok && json.Unmarshal(raw, &suspended) != nil {
		return PanelStatus{}, panelInvalidResponse(false)
	}
	if suspended {
		return PanelStatus{State: "suspended", Message: "面板实例已暂停"}, nil
	}
	var state string
	rawState, ok := envelope["current_state"]
	if !ok || bytes.Equal(bytes.TrimSpace(rawState), []byte("null")) || bytes.Equal(bytes.TrimSpace(rawState), []byte(`""`)) {
		rawState = envelope["status"]
	}
	if json.Unmarshal(rawState, &state) != nil || state == "" {
		return PanelStatus{}, panelInvalidResponse(false)
	}
	switch state {
	case "running":
		return PanelStatus{State: state, Message: "服务器运行中"}, nil
	case "offline":
		return PanelStatus{State: state, Message: "服务器已关闭"}, nil
	case "starting":
		return PanelStatus{State: state, Message: "服务器启动中"}, nil
	case "stopping":
		return PanelStatus{State: state, Message: "服务器关闭中"}, nil
	default:
		return PanelStatus{State: "unknown", Message: "API 已连接，实例状态未知"}, nil
	}
}

// Reload reports acceptance by the panel, not completion inside Minecraft.
// It never retries because an interrupted response may follow successful delivery.
func (p *PanelClient) Reload(ctx context.Context) error {
	_, err := p.request(ctx, true)
	return err
}

func (p *PanelClient) request(ctx context.Context, reload bool) ([]byte, error) {
	if p == nil || p.client == nil || !panelValidKey(p.apiKey) {
		return nil, &PanelError{Message: "麦块 API Key 未配置或格式无效"}
	}
	if !panelValidInstanceID(p.instanceID) {
		return nil, &PanelError{Message: "麦块实例 ID 未配置或格式无效"}
	}
	if ctx.Err() != nil {
		return nil, &PanelError{Message: "请求已取消，未向麦块发送请求"}
	}
	method := http.MethodGet
	endpoint := panelAPIBase + "/servers/" + p.instanceID
	var payload io.Reader
	if reload {
		method = http.MethodPost
		endpoint += "/command"
		payload = strings.NewReader(`{"command":"ysm model reload"}`)
	}
	// A context deadline also bounds injected clients and response-body reads.
	ctx, cancel := context.WithTimeout(ctx, panelTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, endpoint, payload)
	if err != nil {
		return nil, &PanelError{Message: "无法创建麦块 API 请求"}
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Accept", "application/json")
	if reload {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, &PanelError{Unknown: reload, Message: panelConnectionMessage(reload)}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, panelResponseError(resp.StatusCode, reload)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, panelResponseLimit+1))
	if err != nil || len(body) > panelResponseLimit {
		return nil, panelInvalidResponse(reload)
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		if reload {
			return body, nil // Includes the standard command response: HTTP 204.
		}
		return nil, panelInvalidResponse(false)
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil || envelope == nil {
		return nil, panelInvalidResponse(reload)
	}
	if raw, ok := envelope["code"]; ok {
		code, ok := panelBusinessCode(raw)
		if !ok {
			return nil, panelInvalidResponse(reload)
		}
		if code != 0 && code != http.StatusOK {
			return nil, panelResponseError(code, reload)
		}
	}
	if raw, ok := envelope["success"]; ok {
		var success bool
		if json.Unmarshal(raw, &success) != nil {
			return nil, panelInvalidResponse(reload)
		}
		if !success {
			return nil, &PanelError{Message: "麦块 API 拒绝了请求"}
		}
	}
	// Some HTTP APIs return an error payload with a successful HTTP status.
	for _, key := range []string{"error", "errors"} {
		if raw, ok := envelope[key]; ok && !panelEmptyError(raw) {
			return nil, &PanelError{Message: "麦块 API 拒绝了请求"}
		}
	}
	return body, nil
}

func panelValidKey(key string) bool {
	if key == "" || len(key) > 4096 {
		return false
	}
	for _, r := range key {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}

func panelValidInstanceID(id string) bool {
	if len(id) < 1 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func panelBusinessCode(raw json.RawMessage) (int, bool) {
	var code int
	if json.Unmarshal(raw, &code) == nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return code, true
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		code, err := strconv.Atoi(text)
		return code, err == nil
	}
	return 0, false
}

func panelEmptyError(raw json.RawMessage) bool {
	switch string(bytes.TrimSpace(raw)) {
	case "null", `""`, "[]", "{}", "false":
		return true
	default:
		return false
	}
}

func panelResponseError(code int, reload bool) *PanelError {
	err := &PanelError{Message: "麦块 API 拒绝了请求"}
	switch code {
	case 401:
		err.Message = "麦块 API Key 无效或已失效"
	case 403:
		err.Message = "麦块 API 权限不足或当前 IP 未获允许"
	case 404:
		err.Message = "麦块实例不存在或当前 API Key 无权访问"
	case 409:
		err.Message = "麦块实例当前状态不允许此操作"
	case 422:
		err.Message = "麦块 API 拒绝了请求参数"
	case 429:
		err.Message = "麦块 API 请求过于频繁，请稍后再试"
	default:
		if code >= 300 && code < 400 {
			err.Message = "麦块 API 返回重定向，已停止请求"
		} else if code >= 500 || code == http.StatusRequestTimeout {
			err.Unknown = reload
			err.Message = panelConnectionMessage(reload)
		}
	}
	return err
}

func panelConnectionMessage(reload bool) string {
	if reload {
		return "未能确认重载指令是否送达，请检查面板控制台后再决定是否重试"
	}
	return "暂时无法连接麦块 API，请稍后再试"
}

func panelInvalidResponse(reload bool) *PanelError {
	message := "麦块 API 返回了无法识别的响应"
	if reload {
		message = "麦块 API 响应异常，无法确认重载指令是否送达，请检查面板控制台"
	}
	return &PanelError{Unknown: reload, Message: message}
}
