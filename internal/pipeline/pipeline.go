// Package pipeline drives background generation: a text worker and an audio
// worker, each a single goroutine draining an ordered queue.
//
// 核心约定：force（重新生成）不进 job——调用方先删产物文件再入队，worker
// 只看"产物文件是否存在"决定做不做。这样磁盘永远是唯一事实源，进程随时
// 崩溃/重启，恢复扫描（Recover）都能把缺口补齐，不会出现"新文本配旧音频
// 却被当作完成"的错位。
package pipeline

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"phonicsfun/internal/llm"
	"phonicsfun/internal/store"
	"phonicsfun/internal/wav"
)

type State string

const (
	StatePending State = "pending"
	StateRunning State = "running"
	StateDone    State = "done"
	StateFailed  State = "failed"
)

type WordStatus struct {
	Word  string `json:"word"`
	Slug  string `json:"slug"`
	Text  State  `json:"text"`
	Audio State  `json:"audio"`
	Error string `json:"error,omitempty"`
}

// RegenTarget 是 regenerate 接口的粒度。
type RegenTarget string

const (
	RegenText  RegenTarget = "text"
	RegenAudio RegenTarget = "audio"
	RegenBoth  RegenTarget = "both"
)

// AudioSession 抽象一条 Live 会话（便于测试注入）。
type AudioSession interface {
	Speak(ctx context.Context, script string) ([]byte, error)
	WordDone()
	NeedsRotation() bool
	Close() error
}

// LLM 抽象 pipeline 依赖的模型调用。
type LLM interface {
	GenerateCard(ctx context.Context, word string) (*store.Card, error)
	ConnectLive(ctx context.Context) (AudioSession, error)
}

// WrapClient 把 *llm.Client 适配成 LLM 接口。
func WrapClient(c *llm.Client) LLM { return clientAdapter{c} }

type clientAdapter struct{ *llm.Client }

func (a clientAdapter) ConnectLive(ctx context.Context) (AudioSession, error) {
	return a.Client.ConnectLive(ctx)
}

type wordState struct {
	word  string
	text  State
	audio State
	err   string
}

type Pipeline struct {
	store *store.Store
	llm   LLM

	textQ  *jobQueue
	audioQ *jobQueue

	mu      sync.Mutex
	states  map[string]*wordState // slug → 内存状态；文件缺位时的补充事实源
	inText  map[string]bool       // 队列去重
	inAudio map[string]bool

	// 重试节奏，测试中注入零值加速
	backoff      []time.Duration
	audioRetries int

	wg     sync.WaitGroup
	cancel context.CancelFunc
}

func New(st *store.Store, l LLM) *Pipeline {
	return &Pipeline{
		store:        st,
		llm:          l,
		textQ:        newJobQueue(),
		audioQ:       newJobQueue(),
		states:       map[string]*wordState{},
		inText:       map[string]bool{},
		inAudio:      map[string]bool{},
		backoff:      []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 15 * time.Second, 60 * time.Second},
		audioRetries: 3,
	}
}

// Start 启动两条 worker。
func (p *Pipeline) Start(ctx context.Context) {
	ctx, p.cancel = context.WithCancel(ctx)
	p.wg.Add(2)
	go func() { defer p.wg.Done(); p.textWorker(ctx) }()
	go func() { defer p.wg.Done(); p.audioWorker(ctx) }()
}

// Stop 停止接收新任务并等待 worker 退出。
func (p *Pipeline) Stop() {
	p.textQ.close()
	p.audioQ.close()
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
}

// EnqueueWords 批量入队（导入新组）。已有产物的词会被 worker 秒过。
func (p *Pipeline) EnqueueWords(words []string) {
	for _, w := range words {
		p.enqueueText(job{Word: w, Slug: store.Slug(w)}, false)
	}
}

// Regenerate 删除对应产物并高优先级重新入队。若该词正在生成中则拒绝，
// 避免删掉 worker 正在写的产物。
func (p *Pipeline) Regenerate(word string, target RegenTarget) error {
	slug := store.Slug(word)
	p.mu.Lock()
	if st, ok := p.states[slug]; ok && (st.text == StateRunning || st.audio == StateRunning) {
		p.mu.Unlock()
		return fmt.Errorf("该词正在生成中，请稍后再试")
	}
	p.mu.Unlock()

	switch target {
	case RegenText, RegenBoth:
		// 文本变了拼读脚本就变了，音频必须级联重生
		if err := p.store.DeleteCard(slug); err != nil {
			return err
		}
		if err := p.store.DeleteAudio(slug); err != nil {
			return err
		}
		p.enqueueText(job{Word: word, Slug: slug}, true)
	case RegenAudio:
		if err := p.store.DeleteAudio(slug); err != nil {
			return err
		}
		p.enqueueAudio(job{Word: word, Slug: slug}, true)
	default:
		return fmt.Errorf("未知 target: %q", target)
	}
	return nil
}

// Recover 扫描全部组，把缺产物的词重新入队（进程重启后调用）。
func (p *Pipeline) Recover() error {
	groups, err := p.store.ListGroups()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, g := range groups {
		for _, w := range g.Words {
			slug := store.Slug(w)
			if slug == "" || seen[slug] {
				continue
			}
			seen[slug] = true
			switch {
			case !p.store.HasCard(slug):
				p.enqueueText(job{Word: w, Slug: slug}, false)
			case !p.store.HasAudio(slug, store.AudioWord) || !p.store.HasAudio(slug, store.AudioBlend):
				p.enqueueAudio(job{Word: w, Slug: slug}, false)
			}
		}
	}
	return nil
}

// Status 返回一个词的当前状态：内存记录优先，否则由产物文件推导。
func (p *Pipeline) Status(word string) WordStatus {
	slug := store.Slug(word)
	ws := WordStatus{Word: word, Slug: slug}

	p.mu.Lock()
	st, ok := p.states[slug]
	if ok {
		ws.Text, ws.Audio, ws.Error = st.text, st.audio, st.err
	}
	p.mu.Unlock()

	if ws.Text == "" {
		if p.store.HasCard(slug) {
			ws.Text = StateDone
		} else {
			ws.Text = StatePending
		}
	}
	if ws.Audio == "" {
		if p.store.HasAudio(slug, store.AudioWord) && p.store.HasAudio(slug, store.AudioBlend) {
			ws.Audio = StateDone
		} else {
			ws.Audio = StatePending
		}
	}
	return ws
}

// --- 内部 ---

func (p *Pipeline) enqueueText(j job, front bool) {
	if j.Slug == "" {
		return
	}
	p.mu.Lock()
	if p.inText[j.Slug] {
		p.mu.Unlock()
		return
	}
	p.inText[j.Slug] = true
	p.setLocked(j, func(s *wordState) { s.text = StatePending })
	p.mu.Unlock()
	p.textQ.push(j, front)
}

func (p *Pipeline) enqueueAudio(j job, front bool) {
	if j.Slug == "" {
		return
	}
	p.mu.Lock()
	if p.inAudio[j.Slug] {
		p.mu.Unlock()
		return
	}
	p.inAudio[j.Slug] = true
	p.setLocked(j, func(s *wordState) { s.audio = StatePending })
	p.mu.Unlock()
	p.audioQ.push(j, front)
}

func (p *Pipeline) set(j job, fn func(*wordState)) {
	p.mu.Lock()
	p.setLocked(j, fn)
	p.mu.Unlock()
}

func (p *Pipeline) setLocked(j job, fn func(*wordState)) {
	st, ok := p.states[j.Slug]
	if !ok {
		st = &wordState{word: j.Word}
		p.states[j.Slug] = st
	}
	fn(st)
}

func (p *Pipeline) textWorker(ctx context.Context) {
	for {
		j, ok := p.textQ.pop()
		if !ok {
			return
		}
		p.mu.Lock()
		delete(p.inText, j.Slug)
		p.mu.Unlock()

		if p.store.HasCard(j.Slug) {
			p.set(j, func(s *wordState) { s.text = StateDone })
			p.maybeEnqueueAudio(j)
			continue
		}

		p.set(j, func(s *wordState) { s.text = StateRunning; s.err = "" })
		card, err := p.generateWithRetry(ctx, j.Word)
		if err != nil {
			log.Printf("[text] %s 失败: %v", j.Word, err)
			p.set(j, func(s *wordState) { s.text = StateFailed; s.err = err.Error() })
			continue
		}
		if err := p.store.WriteCard(j.Slug, card); err != nil {
			p.set(j, func(s *wordState) { s.text = StateFailed; s.err = err.Error() })
			continue
		}
		log.Printf("[text] %s 完成", j.Word)
		p.set(j, func(s *wordState) { s.text = StateDone })
		p.maybeEnqueueAudio(j)
	}
}

func (p *Pipeline) maybeEnqueueAudio(j job) {
	if !p.store.HasAudio(j.Slug, store.AudioWord) || !p.store.HasAudio(j.Slug, store.AudioBlend) {
		p.enqueueAudio(j, false)
	} else {
		p.set(j, func(s *wordState) { s.audio = StateDone })
	}
}

func (p *Pipeline) generateWithRetry(ctx context.Context, word string) (*store.Card, error) {
	var lastErr error
	for attempt := 0; attempt <= len(p.backoff); attempt++ {
		if attempt > 0 {
			if !sleepCtx(ctx, p.backoff[attempt-1]) {
				return nil, ctx.Err()
			}
		}
		card, err := p.llm.GenerateCard(ctx, word)
		if err == nil {
			return card, nil
		}
		lastErr = err
		if !llm.IsRetryable(err) {
			return nil, err
		}
		log.Printf("[text] %s 第 %d 次尝试失败（将退避重试）: %v", word, attempt+1, err)
	}
	return nil, lastErr
}

func (p *Pipeline) audioWorker(ctx context.Context) {
	var sess AudioSession
	closeSess := func() {
		if sess != nil {
			sess.Close()
			sess = nil
		}
	}
	defer closeSess()

	for {
		j, ok := p.audioQ.pop()
		if !ok {
			return
		}
		p.mu.Lock()
		delete(p.inAudio, j.Slug)
		p.mu.Unlock()

		needBlend := !p.store.HasAudio(j.Slug, store.AudioBlend)
		needWord := !p.store.HasAudio(j.Slug, store.AudioWord)
		if !needBlend && !needWord {
			p.set(j, func(s *wordState) { s.audio = StateDone })
			continue
		}

		card, err := p.store.ReadCard(j.Slug)
		if err != nil {
			p.set(j, func(s *wordState) { s.audio = StateFailed; s.err = "读取卡片失败: " + err.Error() })
			continue
		}

		p.set(j, func(s *wordState) { s.audio = StateRunning; s.err = "" })
		var lastErr error
		ok = false
		for attempt := 0; attempt < p.audioRetries; attempt++ {
			if attempt > 0 && !sleepCtx(ctx, p.backoff[min(attempt-1, len(p.backoff)-1)]) {
				break
			}
			if sess != nil && sess.NeedsRotation() {
				closeSess()
			}
			if sess == nil {
				sess, lastErr = p.llm.ConnectLive(ctx)
				if lastErr != nil {
					log.Printf("[audio] 建会话失败: %v", lastErr)
					sess = nil
					continue
				}
			}
			if needBlend {
				if lastErr = p.speakTo(ctx, sess, j, store.AudioBlend, llm.BuildBlendScript(card), minBlendDur(card)); lastErr != nil {
					closeSess()
					continue
				}
				needBlend = false
			}
			if needWord {
				if lastErr = p.speakTo(ctx, sess, j, store.AudioWord, llm.BuildWordScript(card.Word), 500*time.Millisecond); lastErr != nil {
					closeSess()
					continue
				}
				needWord = false
			}
			sess.WordDone()
			ok = true
			break
		}
		if ok {
			log.Printf("[audio] %s 完成", j.Word)
			p.set(j, func(s *wordState) { s.audio = StateDone })
		} else {
			log.Printf("[audio] %s 失败: %v", j.Word, lastErr)
			errMsg := "未知错误"
			if lastErr != nil {
				errMsg = lastErr.Error()
			}
			p.set(j, func(s *wordState) { s.audio = StateFailed; s.err = errMsg })
		}
	}
}

func (p *Pipeline) speakTo(ctx context.Context, sess AudioSession, j job, kind store.AudioKind, script string, minDur time.Duration) error {
	pcm, err := sess.Speak(ctx, script)
	if err != nil {
		return err
	}
	if dur := wav.Duration(len(pcm), llm.LiveSampleRate); dur < minDur {
		return fmt.Errorf("%s 音频过短（%s < %s），疑似生成失败", kind, dur.Round(time.Millisecond), minDur)
	}
	return p.store.WriteAudio(j.Slug, kind, wav.Encode(pcm, llm.LiveSampleRate))
}

// minBlendDur：拼读音频至少要容纳每个非 silent 音素 0.4s，且不低于 1s。
func minBlendDur(card *store.Card) time.Duration {
	n := 0
	for _, ch := range card.Chunks {
		if !ch.Silent {
			n++
		}
	}
	d := time.Duration(n) * 400 * time.Millisecond
	if d < time.Second {
		d = time.Second
	}
	return d
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

// GroupStatus 汇总一个组的逐词状态。
func (p *Pipeline) GroupStatus(g *store.Group) []WordStatus {
	out := make([]WordStatus, 0, len(g.Words))
	for _, w := range g.Words {
		out = append(out, p.Status(w))
	}
	return out
}

// ReadyCount 统计文本和音频都就绪的词数。
func (p *Pipeline) ReadyCount(g *store.Group) int {
	n := 0
	for _, w := range g.Words {
		st := p.Status(w)
		if st.Text == StateDone && st.Audio == StateDone {
			n++
		}
	}
	return n
}
