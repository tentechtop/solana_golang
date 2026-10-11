package nodeapp

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

//go:embed web/*
var panelFiles embed.FS

func (c *Controller) OpenPanel() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return "", fmt.Errorf("管理器已经关闭")
	}
	if c.panel != nil {
		return c.panelURL, nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	token, err := randomToken()
	if err != nil {
		listener.Close()
		return "", err
	}
	origin := "http://" + listener.Addr().String()
	c.panelToken, c.panelURL = token, origin+"/#"+token
	c.panel = &http.Server{Handler: c.handler(listener.Addr().String(), origin, token), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	server := c.panel
	go func() { _ = server.Serve(listener) }()
	return c.panelURL, nil
}

func (c *Controller) handler(host, origin, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		if r.Host != host || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != origin) {
			http.Error(w, "仅允许本机管理面板", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			candidate := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) != 1 {
				http.Error(w, "会话无效", http.StatusUnauthorized)
				return
			}
			c.handleAPI(w, r)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "GET required", http.StatusMethodNotAllowed)
			return
		}
		name := r.URL.Path
		if name == "/" {
			name = "/index.html"
		}
		if name != "/index.html" && name != "/app.js" && name != "/style.css" {
			http.NotFound(w, r)
			return
		}
		data, err := panelFiles.ReadFile("web" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		switch name {
		case "/index.html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		case "/app.js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		case "/style.css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		}
		_, _ = w.Write(data)
	})
}

func jsonReply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (c *Controller) handleAPI(w http.ResponseWriter, r *http.Request) {
	readOnly := r.URL.Path == "/api/status" || r.URL.Path == "/api/logs" || r.URL.Path == "/api/pairing" || r.URL.Path == "/api/exit-request"
	if (readOnly && r.Method != "GET") || (!readOnly && r.Method != "POST") {
		jsonReply(w, 405, map[string]string{"error": "请求方法不允许"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var err error
	switch r.URL.Path {
	case "/api/status":
		jsonReply(w, 200, c.Snapshot(ctx))
		return
	case "/api/logs":
		jsonReply(w, 200, map[string]string{"text": c.Logs()})
		return
	case "/api/configure":
		var settings Settings
		decoder := json.NewDecoder(io.LimitReader(r.Body, 8193))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&settings); err == nil {
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				err = fmt.Errorf("设置 JSON 格式不正确")
			}
		}
		if err == nil {
			err = c.Configure(ctx, settings)
		}
	case "/api/start":
		if c.platform == "android" {
			err = fmt.Errorf("请通过 APP 的启动按钮启动前台服务")
		} else {
			err = c.Start()
		}
	case "/api/stop":
		err = c.Stop()
	case "/api/pairing":
		var pairing any
		pairing, err = c.Pairing(ctx)
		if err == nil {
			jsonReply(w, 200, pairing)
			return
		}
	case "/api/exit-request":
		var request any
		request, err = c.ExitRequest(ctx)
		if err == nil {
			jsonReply(w, 200, request)
			return
		}
	case "/api/quit":
		if c.platform == "android" {
			err = fmt.Errorf("请使用 APP 返回按钮")
		} else {
			err = c.Stop()
			if err == nil {
				jsonReply(w, 200, map[string]bool{"ok": true})
				go c.Close()
				return
			}
		}
	default:
		jsonReply(w, 404, map[string]string{"error": "接口不存在"})
		return
	}
	if err != nil {
		jsonReply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, 200, map[string]bool{"ok": true})
}
