// muse-go-gw：给 muse-protocol-go 套一层 OpenAI 兼容 HTTP 接口
// 路由：/v1/models  /v1/chat/completions  /healthz  /admin/diagnose  /media/
// 环境变量：
//   GATEWAY_KEY      可选，Bearer 鉴权；不设则不校验
//   MUSE_COOKIE      muse.ai cookie header 字符串（hatch_sess=...）
//   MUSE_COOKIE_FILE cookie 持久化文件；轮换后自动重写，重启时可从这读
//   PORT             监听端口，默认 8130
//   MUSE_PROXY       direct 或 http(s)://socks5:// 地址，默认 direct
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	muse "github.com/CoolkHz/muse-protocol-go/muse"
)

// ---------- 全局状态 ----------

var (
	eng       *muse.ProtoEngine
	accMu     sync.Mutex
	account   *muse.Account
	cookieF   string
	sem       chan struct{} // 同一账号串行，防踩踏
	httpCl    = &http.Client{Timeout: 20 * time.Second}
)

// ---------- cookie 读写 ----------

func parseCookieHeader(h string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(h, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if i := strings.Index(part, "="); i > 0 {
			out[part[:i]] = part[i+1:]
		}
	}
	return out
}

func cookieToHeader(c map[string]string) string {
	parts := make([]string, 0, len(c))
	for k, v := range c {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "; ")
}

func loadCookie() (map[string]string, error) {
	if h := os.Getenv("MUSE_COOKIE"); h != "" {
		return parseCookieHeader(h), nil
	}
	if cookieF != "" {
		if b, err := os.ReadFile(cookieF); err == nil && len(b) > 0 {
			return parseCookieHeader(string(b)), nil
		}
	}
	return nil, fmt.Errorf("没有 cookie：设 MUSE_COOKIE 或 MUSE_COOKIE_FILE")
}

func persistCookie(c map[string]string) {
	if cookieF == "" {
		return
	}
	tmp := cookieF + ".tmp"
	if err := os.WriteFile(tmp, []byte(cookieToHeader(c)), 0o600); err != nil {
		log.Printf("[warn] cookie 落盘失败: %v", err)
		return
	}
	if err := os.Rename(tmp, cookieF); err != nil {
		log.Printf("[warn] cookie 替换失败: %v", err)
		return
	}
	log.Printf("[info] cookie 已轮换并保存 (%d 项)", len(c))
}

// ---------- OpenAI 数据结构 ----------

type oaiMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (m oaiMessage) text() string {
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return s
	}
	var arr []map[string]any
	if err := json.Unmarshal(m.Content, &arr); err == nil {
		var b strings.Builder
		for _, p := range arr {
			if t, ok := p["text"].(string); ok {
				b.WriteString(t)
			}
		}
		return b.String()
	}
	return ""
}

type oaiChatReq struct {
	Model    string       `json:"model"`
	Messages []oaiMessage `json:"messages"`
	Stream   bool         `json:"stream"`
}

func flattenPrompt(msgs []oaiMessage) string {
	var b strings.Builder
	for _, m := range msgs {
		t := strings.TrimSpace(m.text())
		if t == "" {
			continue
		}
		switch m.Role {
		case "system":
			b.WriteString("[设定] " + t + "\n")
		case "user":
			b.WriteString(t + "\n")
		case "assistant":
			b.WriteString("[之前的回复] " + t + "\n")
		default:
			b.WriteString(t + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// ---------- 工具 ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func checkAuth(r *http.Request) bool {
	key := os.Getenv("GATEWAY_KEY")
	if key == "" {
		return true
	}
	return r.Header.Get("Authorization") == "Bearer "+key
}

func withAccount(ctx context.Context, fn func(ctx context.Context, acc *muse.Account) error) error {
	accMu.Lock()
	acc := account
	accMu.Unlock()
	if acc == nil {
		return fmt.Errorf("账号未就绪")
	}
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	return fn(ctx, acc)
}

// ---------- HTTP 处理 ----------

func handleModels(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{
		"object": "list",
		"data": []map[string]any{
			{"id": "muse-spark", "object": "model", "owned_by": "muse"},
			{"id": "muse-image", "object": "model", "owned_by": "muse"},
			{"id": "muse-video", "object": "model", "owned_by": "muse"},
		},
	})
}

func sseChunk(id, model, content string, finish *string) string {
	delta := map[string]string{}
	if content != "" {
		delta["content"] = content
	}
	fr := any(nil)
	if finish != nil {
		fr = *finish
	}
	b, _ := json.Marshal(map[string]any{
		"id": id, "object": "chat.completions.chunk", "created": time.Now().Unix(),
		"model": model,
		"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": fr}},
	})
	return "data: " + string(b) + "\n\n"
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	var req oaiChatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	prompt := flattenPrompt(req.Messages)
	if prompt == "" {
		writeJSON(w, 400, map[string]string{"error": "messages 为空"})
		return
	}
	model := req.Model
	if model == "" {
		model = "muse-spark"
	}
	id := fmt.Sprintf("chatcmpl-muse%d", time.Now().UnixNano())

	if model == "muse-image" || model == "muse-video" {
		kind := "image"
		if model == "muse-video" {
			kind = "video"
		}
		var files []muse.Media
		err := withAccount(r.Context(), func(ctx context.Context, acc *muse.Account) error {
			var e error
			files, e = eng.Generate(ctx, acc, muse.GenRequest{Prompt: prompt, Kind: kind, N: 1})
			return e
		})
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		var b strings.Builder
		b.WriteString("已生成 " + kind + "：\n")
		for _, f := range files {
			fmt.Fprintf(&b, "- %s (%s, %d bytes) 本地路径 media/%s\n", f.Filename, f.Mime, f.Bytes, f.Filename)
		}
		if len(files) == 0 {
			b.WriteString("（上游没返回文件）")
		}
		content := b.String()
		writeJSON(w, 200, map[string]any{
			"id": id, "object": "chat.completions", "created": time.Now().Unix(), "model": model,
			"choices": []map[string]any{{"index": 0, "message": map[string]string{"role": "assistant", "content": content}, "finish_reason": "stop"}},
		})
		return
	}

	// 普通对话
	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fl := w.(http.Flusher)
		fl.Flush()
		err := withAccount(r.Context(), func(ctx context.Context, acc *muse.Account) error {
			_, e := eng.Chat(ctx, acc, prompt, nil, func(delta string) {
				if delta == "" {
					return
				}
				fmt.Fprint(w, sseChunk(id, model, delta, nil))
				fl.Flush()
			})
			return e
		})
		if err != nil {
			fmt.Fprintf(w, "data: {\"error\":%q}\n\n", err.Error())
			fl.Flush()
			return
		}
		stop := "stop"
		fmt.Fprint(w, sseChunk(id, model, "", &stop))
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
		return
	}

	var res *muse.ChatResult
	err := withAccount(r.Context(), func(ctx context.Context, acc *muse.Account) error {
		var e error
		res, e = eng.Chat(ctx, acc, prompt, nil, nil)
		return e
	})
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	content := res.Text
	if len(res.Media) > 0 {
		var b strings.Builder
		b.WriteString(content)
		b.WriteString("\n\n[媒体附件]\n")
		for _, f := range res.Media {
			fmt.Fprintf(&b, "- media/%s (%s, %d bytes)\n", f.Filename, f.Mime, f.Bytes)
		}
		content = b.String()
	}
	writeJSON(w, 200, map[string]any{
		"id": id, "object": "chat.completions", "created": time.Now().Unix(), "model": model,
		"choices": []map[string]any{{"index": 0, "message": map[string]string{"role": "assistant", "content": content}, "finish_reason": "stop"}},
		"usage":   map[string]int{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
	})
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "time": time.Now().Format(time.RFC3339)})
}

func handleDiagnose(w http.ResponseWriter, r *http.Request) {
	err := withAccount(r.Context(), func(ctx context.Context, acc *muse.Account) error {
		d, e := eng.Diagnose(ctx, acc)
		if e != nil {
			return e
		}
		writeJSON(w, 200, d)
		return nil
	})
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
	}
}

// ---------- 启动 ----------

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8130"
	}
	cookieF = os.Getenv("MUSE_COOKIE_FILE")
	proxy := os.Getenv("MUSE_PROXY")
	if proxy == "" {
		proxy = "direct"
	}

	cookies, err := loadCookie()
	if err != nil {
		log.Fatalf("[fatal] %v", err)
	}

	cfg := muse.Config{
		Proxy:   proxy,
		DataDir: os.Getenv("DATA_DIR"),
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	eng = muse.NewClient(cfg)
	defer eng.Close()

	account = &muse.Account{
		ID:            "main",
		Cookies:       cookies,
		CookieExpires: map[string]int64{},
	}
	eng.SetHooks(
		func(_ string, c map[string]string, _ map[string]int64) {
			accMu.Lock()
			account = &muse.Account{ID: "main", Cookies: c, CookieExpires: map[string]int64{}}
			accMu.Unlock()
			persistCookie(c)
		},
		nil,
	)
	go eng.Janitor(context.Background())
	sem = make(chan struct{}, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", handleModels)
	mux.HandleFunc("/v1/chat/completions", handleChat)
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/admin/diagnose", handleDiagnose)
	mux.Handle("/media/", http.StripPrefix("/media/", http.FileServer(http.Dir(cfg.DataDir+"/media"))))

	srv := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("[info] muse-go-gw 监听 :%s (账号 1 个, cookie %d 项)", port, len(cookies))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[fatal] %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Printf("[info] 收到退出信号，关闭中…")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
