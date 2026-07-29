// Package httpapi exposes the REST API and serves the embedded SPA.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"

	"phonicsfun/internal/llm"
	"phonicsfun/internal/pipeline"
	"phonicsfun/internal/store"
)

// maxUploadBytes 限制提取请求体大小。前端已把图片压到长边 1600px 的
// JPEG（远小于此），这里只是防御异常客户端；Gemini 请求总上限是 20MB。
const maxUploadBytes = 15 << 20

type Server struct {
	store *store.Store
	pipe  *pipeline.Pipeline
	llm   *llm.Client
	dist  fs.FS // 前端构建产物根（含 index.html）
	mux   *http.ServeMux
}

func New(st *store.Store, p *pipeline.Pipeline, l *llm.Client, dist fs.FS) *Server {
	s := &Server{store: st, pipe: p, llm: l, dist: dist, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /api/extract", s.handleExtract)
	s.mux.HandleFunc("POST /api/groups", s.handleCreateGroup)
	s.mux.HandleFunc("GET /api/groups", s.handleListGroups)
	s.mux.HandleFunc("GET /api/groups/{id}", s.handleGetGroup)
	s.mux.HandleFunc("PUT /api/groups/{id}", s.handleUpdateGroup)
	s.mux.HandleFunc("DELETE /api/groups/{id}", s.handleDeleteGroup)
	s.mux.HandleFunc("GET /api/words/{slug}", s.handleGetCard)
	s.mux.HandleFunc("GET /api/words/{slug}/audio/{file}", s.handleAudio)
	s.mux.HandleFunc("POST /api/words/{slug}/regenerate", s.handleRegenerate)
	s.mux.HandleFunc("/", s.handleSPA)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// --- 提取 ---

func (s *Server) handleExtract(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	var (
		words []string
		err   error
	)
	ct := r.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "multipart/form-data"):
		file, hdr, ferr := func() (io.ReadCloser, string, error) {
			f, h, e := r.FormFile("image")
			if e != nil {
				return nil, "", e
			}
			return f, h.Header.Get("Content-Type"), nil
		}()
		if ferr != nil {
			httpError(w, http.StatusBadRequest, "缺少 image 字段: "+ferr.Error())
			return
		}
		defer file.Close()
		data, rerr := io.ReadAll(file)
		if rerr != nil {
			httpError(w, http.StatusBadRequest, "读取图片失败: "+rerr.Error())
			return
		}
		mime := hdr
		if mime == "" || mime == "application/octet-stream" {
			mime = http.DetectContentType(data)
		}
		words, err = s.llm.ExtractWordsFromImage(r.Context(), mime, data)
	default:
		var body struct {
			Text string `json:"text"`
		}
		if derr := json.NewDecoder(r.Body).Decode(&body); derr != nil || strings.TrimSpace(body.Text) == "" {
			httpError(w, http.StatusBadRequest, "请提供非空的 text 字段")
			return
		}
		words, err = s.llm.ExtractWords(r.Context(), body.Text)
	}
	if err != nil {
		log.Printf("[extract] %v", err)
		httpError(w, http.StatusBadGateway, "AI 提取失败，请重试: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"words": words})
}

// --- 组 ---

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string   `json:"name"`
		Words []string `json:"words"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "请求体解析失败")
		return
	}
	words := dedupeWords(body.Words)
	if len(words) == 0 {
		httpError(w, http.StatusBadRequest, "单词列表为空")
		return
	}
	g, err := s.store.CreateGroup(strings.TrimSpace(body.Name), words)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.pipe.EnqueueWords(words)
	writeJSON(w, http.StatusOK, map[string]string{"id": g.ID})
}

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.ListGroups()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type item struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		CreatedAt string `json:"createdAt"`
		Total     int    `json:"total"`
		Ready     int    `json:"ready"`
	}
	out := make([]item, 0, len(groups))
	for _, g := range groups {
		out = append(out, item{
			ID: g.ID, Name: g.Name, CreatedAt: g.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			Total: len(g.Words), Ready: s.pipe.ReadyCount(g),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	g, err := s.store.GetGroup(r.PathValue("id"))
	if err != nil {
		httpError(w, http.StatusNotFound, "组不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": g.ID, "name": g.Name, "createdAt": g.CreatedAt,
		"words": s.pipe.GroupStatus(g),
	})
}

// handleUpdateGroup 全量替换组的名称与词表（编辑保存）。成功后把新词表
// 全量入队：缺产物的词（新增/改拼写）自动生成，已完成的词被 worker 秒过，
// 顺带补齐历史缺口。被移除的词的产物清理在 store.UpdateGroup 内完成。
func (s *Server) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string   `json:"name"`
		Words []string `json:"words"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "请求体解析失败")
		return
	}
	words := dedupeWords(body.Words)
	if len(words) == 0 {
		httpError(w, http.StatusBadRequest, "单词列表为空")
		return
	}
	g, err := s.store.UpdateGroup(r.PathValue("id"), body.Name, words)
	if err != nil {
		if os.IsNotExist(err) {
			httpError(w, http.StatusNotFound, "组不存在")
			return
		}
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.pipe.EnqueueWords(words)
	writeJSON(w, http.StatusOK, map[string]string{"id": g.ID})
}

func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	purge := r.URL.Query().Get("purge") == "1"
	if err := s.store.DeleteGroup(r.PathValue("id"), purge); err != nil {
		if os.IsNotExist(err) {
			httpError(w, http.StatusNotFound, "组不存在")
			return
		}
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- 单词 ---

func (s *Server) handleGetCard(w http.ResponseWriter, r *http.Request) {
	slug := store.Slug(r.PathValue("slug")) // 再归一化，兼防路径穿越
	if slug == "" || !s.store.HasCard(slug) {
		httpError(w, http.StatusNotFound, "卡片尚未生成")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	http.ServeFile(w, r, s.store.CardPath(slug))
}

func (s *Server) handleAudio(w http.ResponseWriter, r *http.Request) {
	slug := store.Slug(r.PathValue("slug"))
	var kind store.AudioKind
	switch r.PathValue("file") {
	case "word.wav":
		kind = store.AudioWord
	case "blend.wav":
		kind = store.AudioBlend
	case "blend.cues.json":
		// blend 的时间标注，与 blend.wav 成对生成（先 cues 后 wav 落盘）；
		// 旧数据可能没有，前端拿 404 即降级（无高亮/点读）
		if slug == "" {
			httpError(w, http.StatusNotFound, "未知单词")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		http.ServeFile(w, r, s.store.CuesPath(slug))
		return
	default:
		httpError(w, http.StatusNotFound, "未知音频")
		return
	}
	if slug == "" || !s.store.HasAudio(slug, kind) {
		httpError(w, http.StatusNotFound, "音频尚未生成")
		return
	}
	// http.ServeFile 自带 Range 支持（iOS Safari 的 <audio> 依赖）
	http.ServeFile(w, r, s.store.AudioPath(slug, kind))
}

func (s *Server) handleRegenerate(w http.ResponseWriter, r *http.Request) {
	slug := store.Slug(r.PathValue("slug"))
	var body struct {
		Target string `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "请求体解析失败")
		return
	}
	word, err := s.wordForSlug(slug)
	if err != nil {
		httpError(w, http.StatusNotFound, "未找到该单词")
		return
	}
	if err := s.pipe.Regenerate(word, pipeline.RegenTarget(body.Target)); err != nil {
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// wordForSlug 反查 slug 对应的原词：优先取已生成卡片里的 word（撇号等
// 信息在 slug 里已丢失），卡片不存在时（如文本生成失败后重试）扫描组。
func (s *Server) wordForSlug(slug string) (string, error) {
	if card, err := s.store.ReadCard(slug); err == nil {
		return card.Word, nil
	}
	groups, err := s.store.ListGroups()
	if err != nil {
		return "", err
	}
	for _, g := range groups {
		for _, w := range g.Words {
			if store.Slug(w) == slug {
				return w, nil
			}
		}
	}
	return "", errors.New("slug 无对应单词")
}

// --- SPA ---

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		httpError(w, http.StatusNotFound, "接口不存在")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if f, err := s.dist.Open(path); err == nil {
		f.Close()
		http.ServeFileFS(w, r, s.dist, path)
		return
	}
	// SPA fallback：一切未知路径回 index.html（hash 路由不会走到这，保险）
	if f, err := s.dist.Open("index.html"); err == nil {
		f.Close()
		http.ServeFileFS(w, r, s.dist, "index.html")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "前端尚未构建：请先执行 make web 再重新编译。")
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[http] 编码响应失败: %v", err)
	}
}

func httpError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// dedupeWords 清洗前端传来的词表：trim、去空、按 slug 去重保序。
func dedupeWords(words []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range words {
		w = strings.TrimSpace(w)
		slug := store.Slug(w)
		if w == "" || slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, w)
	}
	return out
}
