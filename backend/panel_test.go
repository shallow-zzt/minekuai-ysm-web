package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type panelTestTransport func(*http.Request) (*http.Response, error)

func (f panelTestTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func panelTestResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func panelTestClient(transport panelTestTransport) *PanelClient {
	p := NewPanelClient("mkc_test_secret", "1234abcd")
	p.client.Transport = transport
	return p
}

func requirePanelError(t *testing.T, err error, unknown bool) *PanelError {
	t.Helper()
	var pe *PanelError
	if !errors.As(err, &pe) {
		t.Fatalf("want PanelError, got %T: %v", err, err)
	}
	if pe.Unknown != unknown {
		t.Fatalf("Unknown = %v, want %v: %s", pe.Unknown, unknown, pe.Message)
	}
	if strings.Contains(pe.Message, "mkc_test_secret") || strings.Contains(pe.Message, "raw-upstream-detail") {
		t.Fatalf("upstream data leaked into message: %s", pe.Message)
	}
	return pe
}

func TestPanelReloadUsesOnlyFixedCommandAndDestination(t *testing.T) {
	calls := 0
	p := panelTestClient(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodPost || req.URL.String() != "https://api.minekuai.cn/panel/client-api/servers/1234abcd/command" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL)
		}
		if req.Header.Get("Authorization") != "Bearer mkc_test_secret" || req.Header.Get("Content-Type") != "application/json" {
			t.Errorf("missing required headers")
		}
		body, err := io.ReadAll(req.Body)
		if err != nil || string(body) != `{"command":"ysm model reload"}` {
			t.Errorf("unexpected command body %q: %v", body, err)
		}
		deadline, ok := req.Context().Deadline()
		if !ok || time.Until(deadline) > panelTimeout {
			t.Errorf("request lacks bounded deadline")
		}
		return panelTestResponse(http.StatusNoContent, ""), nil
	})
	if err := p.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || p.client.Timeout != panelTimeout {
		t.Fatalf("calls = %d, timeout = %s", calls, p.client.Timeout)
	}
}

func TestPanelStatusDecodesClientAPIShapes(t *testing.T) {
	tests := []struct {
		name, body, state string
	}{
		{"observed live", `{"identifier":"1234abcd","uuid":"example","name":"example","description":"","status":null,"limits":{},"stats":{"object":"stats","attributes":{"current_state":"running","is_suspended":false,"resources":{}}}}`, "running"},
		{"runtime priority", `{"status":"offline","current_state":"stopping","stats":{"attributes":{"current_state":"running"}}}`, "running"},
		{"runtime offline", `{"status":null,"stats":{"attributes":{"current_state":"offline"}}}`, "offline"},
		{"runtime suspended", `{"status":null,"stats":{"attributes":{"current_state":"running","is_suspended":true}}}`, "suspended"},
		{"runtime wrapped", `{"code":200,"data":{"stats":{"attributes":{"current_state":"running"}}}}`, "running"},
		{"deployed direct", `{"identifier":"1234abcd","uuid":"example","name":"example","status":"running","limits":{},"stats":{}}`, "running"},
		{"deployed stopped", `{"status":"offline","stats":{}}`, "offline"},
		{"deployed envelope", `{"code":200,"data":{"status":"running"}}`, "running"},
		{"null legacy state", `{"current_state":null,"status":"starting"}`, "starting"},
		{"direct", `{"current_state":"running","resources":{}}`, "running"},
		{"attributes", `{"object":"server","attributes":{"current_state":"offline"}}`, "offline"},
		{"envelope", `{"code":200,"data":{"attributes":{"current_state":"starting"}}}`, "starting"},
		{"string code", `{"code":"0","data":{"current_state":"stopping"}}`, "stopping"},
		{"suspended", `{"current_state":"running","is_suspended":true}`, "suspended"},
		{"future state", `{"current_state":"raw-upstream-detail"}`, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := panelTestClient(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.URL.Path != "/panel/client-api/servers/1234abcd" || req.Body != nil {
					t.Errorf("wrong status route: %s %s", req.Method, req.URL)
				}
				return panelTestResponse(http.StatusOK, tt.body), nil
			})
			status, err := p.Status(context.Background())
			if err != nil || status.State != tt.state || status.Message == "" || strings.Contains(status.Message, "raw-upstream-detail") {
				t.Fatalf("status = %+v, err = %v", status, err)
			}
		})
	}
}

func TestPanelPreservesCompletePrintableAPIKey(t *testing.T) {
	const key = "mkc_test.first-segment_second+third/part=last"
	p := NewPanelClient(" \r\n"+key+"\r\n", "1234abcd")
	p.client.Transport = panelTestTransport(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Authorization"); got != "Bearer "+key {
			t.Fatal("API key was truncated or modified")
		}
		return panelTestResponse(http.StatusNoContent, ""), nil
	})
	if err := p.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPanelReloadAcceptsSuccessfulEnvelopes(t *testing.T) {
	for _, body := range []string{"", `{"code":200,"data":null}`, `{"code":"200"}`, `{"code":0}`, `{"success":true}`, `{}`} {
		t.Run(body, func(t *testing.T) {
			p := panelTestClient(func(*http.Request) (*http.Response, error) { return panelTestResponse(http.StatusOK, body), nil })
			if err := p.Reload(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPanelReloadDistinguishesRejectionFromUnknownDelivery(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		unknown bool
	}{
		{"authentication", 401, `{"message":"mkc_test_secret raw-upstream-detail"}`, false},
		{"permission", 403, "raw-upstream-detail", false},
		{"missing", 404, "raw-upstream-detail", false},
		{"offline", 409, "raw-upstream-detail", false},
		{"parameters", 422, "raw-upstream-detail", false},
		{"rate limit", 429, "raw-upstream-detail", false},
		{"server failure", 502, "raw-upstream-detail", true},
		{"request timeout", 408, "raw-upstream-detail", true},
		{"business authentication", 200, `{"code":401,"msg":"raw-upstream-detail"}`, false},
		{"business string rejection", 200, `{"code":"403","msg":"raw-upstream-detail"}`, false},
		{"business generic rejection", 200, `{"code":1,"msg":"raw-upstream-detail"}`, false},
		{"business internal error", 200, `{"code":500,"msg":"raw-upstream-detail"}`, true},
		{"success false", 200, `{"success":false,"message":"raw-upstream-detail"}`, false},
		{"error", 200, `{"error":"raw-upstream-detail"}`, false},
		{"errors", 200, `{"errors":[{"detail":"raw-upstream-detail"}]}`, false},
		{"invalid json", 200, "raw-upstream-detail", true},
		{"trailing json", 200, `{"code":200}{"code":403}`, true},
		{"null json", 200, "null", true},
		{"null code", 200, `{"code":null}`, true},
		{"invalid code", 200, `{"code":true}`, true},
		{"too large", 200, strings.Repeat(" ", panelResponseLimit+1), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			p := panelTestClient(func(*http.Request) (*http.Response, error) {
				calls++
				return panelTestResponse(tt.status, tt.body), nil
			})
			requirePanelError(t, p.Reload(context.Background()), tt.unknown)
			if calls != 1 {
				t.Fatalf("must not retry a command, calls = %d", calls)
			}
		})
	}
}

func TestPanelNeverFollowsRedirectsWithCredentials(t *testing.T) {
	calls := 0
	p := panelTestClient(func(*http.Request) (*http.Response, error) {
		calls++
		resp := panelTestResponse(http.StatusTemporaryRedirect, "")
		resp.Header.Set("Location", "https://untrusted.invalid/collect")
		return resp, nil
	})
	requirePanelError(t, p.Reload(context.Background()), false)
	if calls != 1 {
		t.Fatalf("redirect was followed: %d requests", calls)
	}
}

func TestPanelNetworkAndBodyFailuresDoNotLeakOrRetry(t *testing.T) {
	for _, testBodyFailure := range []bool{false, true} {
		calls := 0
		p := panelTestClient(func(*http.Request) (*http.Response, error) {
			calls++
			if testBodyFailure {
				resp := panelTestResponse(http.StatusOK, "")
				resp.Body = panelFailingReader{}
				return resp, nil
			}
			return nil, errors.New("mkc_test_secret raw-upstream-detail")
		})
		requirePanelError(t, p.Reload(context.Background()), true)
		if calls != 1 {
			t.Fatalf("command retried: %d", calls)
		}
		_, err := p.Status(context.Background())
		requirePanelError(t, err, false)
	}
}

type panelFailingReader struct{}

func (panelFailingReader) Read([]byte) (int, error) { return 0, errors.New("raw-upstream-detail") }
func (panelFailingReader) Close() error             { return nil }

func TestPanelDeadlineAndCancellation(t *testing.T) {
	p := panelTestClient(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	requirePanelError(t, p.Reload(ctx), true)
	if time.Since(started) > time.Second {
		t.Fatal("request failed to respect caller deadline")
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	requirePanelError(t, p.Reload(ctx), false)
}

func TestPanelInvalidConfigNeverSendsARequest(t *testing.T) {
	for _, tt := range []struct{ key, id string }{
		{"", "1234abcd"}, {"secret\nInjected: header", "1234abcd"},
		{"secret", "../other"}, {"secret", "id/command"}, {"secret", "id?other"}, {"secret", ""},
	} {
		p := NewPanelClient(tt.key, tt.id)
		p.client.Transport = panelTestTransport(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid configuration made a network request")
			return nil, nil
		})
		requirePanelError(t, p.Reload(context.Background()), false)
	}
}

func TestPanelStatusRejectsMalformedOrMissingState(t *testing.T) {
	for _, body := range []string{"", `{}`, `{"data":null}`, `{"attributes":[]}`, `{"current_state":123}`, `{"current_state":"running","is_suspended":"false"}`} {
		p := panelTestClient(func(*http.Request) (*http.Response, error) { return panelTestResponse(http.StatusOK, body), nil })
		_, err := p.Status(context.Background())
		requirePanelError(t, err, false)
	}
}
