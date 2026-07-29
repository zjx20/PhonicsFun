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
	"errors"
	"fmt"
	"log"
	"os"
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
	// AudioVersion 是音频产物的缓存版本（store.AudioVersion），仅 audio
	// 为 done 时非零。前端音频/cues URL 以它做 ?v= 参数防缓存。
	AudioVersion int64  `json:"audioVersion,omitempty"`
	Error        string `json:"error,omitempty"`
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

// LLM 抽象 pipeline 依赖的模型调用。feedback 是用户重新生成时附带的
// 纠错意见（常规生成为空串）。
type LLM interface {
	GenerateCard(ctx context.Context, word, feedback string) (*store.Card, error)
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
	// slug → 用户重新生成时附带的纠错意见，text worker 处理该词时消费。
	// 不随 job 走：入队去重丢弃新 job 时反馈仍要生效（最后一次反馈赢）。
	// 只存内存，与 pending/running 状态同命——重启后 Recover 补缺口时无反馈，
	// 这是有意为之：磁盘上不引入第二事实源（见包注释的核心约定）。
	feedback map[string]string

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
		feedback:     map[string]string{},
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
// 避免删掉 worker 正在写的产物。feedback 是用户的纠错意见（可为空），
// 只注入文本生成 prompt——音频脚本机械拼装、不接受自由文本，所以
// target=audio 时 feedback 被忽略（发音标注问题应重生成文本，自动级联音频）。
func (p *Pipeline) Regenerate(word string, target RegenTarget, feedback string) error {
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
		if feedback != "" {
			p.mu.Lock()
			p.feedback[slug] = feedback
			p.mu.Unlock()
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
// 同时承担 schema 迁移：卡片存在但版本不是当前 CardSchemaVersion（或已
// 损坏读不出来）时，删除该词全部产物并重新生成——升级后旧数据自动重建。
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
			if !p.store.HasCard(slug) {
				p.enqueueText(job{Word: w, Slug: slug}, false)
				continue
			}
			card, err := p.store.ReadCard(slug)
			if err != nil || card.Schema != store.CardSchemaVersion {
				log.Printf("[recover] %s 卡片为旧版或损坏，删除产物重新生成", w)
				if err := p.store.DeleteCard(slug); err != nil {
					return err
				}
				if err := p.store.DeleteAudio(slug); err != nil {
					return err
				}
				p.enqueueText(job{Word: w, Slug: slug}, false)
				continue
			}
			if !p.store.HasAudio(slug, store.AudioWord) || !p.store.HasAudio(slug, store.AudioBlend) {
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
	if ws.Audio == StateDone {
		ws.AudioVersion = p.store.AudioVersion(slug)
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
		// 反馈随本次处理消费掉（无论后续成败）：它描述的是"上一版卡片"的
		// 问题，失败重试由用户再触发时会带上新反馈，旧反馈不能残留误伤
		// 之后与它无关的生成。
		feedback := p.feedback[j.Slug]
		delete(p.feedback, j.Slug)
		p.mu.Unlock()

		if p.store.HasCard(j.Slug) {
			p.set(j, func(s *wordState) { s.text = StateDone })
			p.maybeEnqueueAudio(j)
			continue
		}

		p.set(j, func(s *wordState) { s.text = StateRunning; s.err = "" })
		card, err := p.generateWithRetry(ctx, j.Word, feedback)
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

func (p *Pipeline) generateWithRetry(ctx context.Context, word, feedback string) (*store.Card, error) {
	var lastErr error
	for attempt := 0; attempt <= len(p.backoff); attempt++ {
		if attempt > 0 {
			if !sleepCtx(ctx, p.backoff[attempt-1]) {
				return nil, ctx.Err()
			}
		}
		card, err := p.llm.GenerateCard(ctx, word, feedback)
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
			// word 轮在前：blend 的收尾整词段直接复用 word.wav 里的
			// 慢速/常速遍，保证与"整词"按钮听到的同源
			if needWord {
				if lastErr = p.generateWord(ctx, sess, j, card); lastErr != nil {
					log.Printf("[audio] %s word 生成失败（将重试）: %v", j.Word, lastErr)
					closeSess()
					continue
				}
				needWord = false
			}
			if needBlend {
				if lastErr = p.generateBlend(ctx, sess, j, card); lastErr != nil {
					if errors.Is(lastErr, errWordAudioUnusable) {
						// 存量 word.wav 切不出两遍（旧脚本没有停顿指令）：
						// 删掉重新生成——是素材问题，会话不用换
						log.Printf("[audio] %s 的 word.wav 不可复用，删除后重新生成: %v", j.Word, lastErr)
						if lastErr = p.store.DeleteAudio(j.Slug); lastErr != nil {
							break
						}
						needWord = true
						continue
					}
					log.Printf("[audio] %s blend 生成失败（将重试）: %v", j.Word, lastErr)
					closeSess()
					continue
				}
				needBlend = false
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

// generateWord 整词轮：慢速 + 常速两遍，脚本要求两遍之间整秒静音。
// 落盘前硬校验能按静音切成两段——blend 收尾段依赖从 word.wav 里切出
// 这两遍复用，写盘时切不开的音频等于埋雷，直接判本轮失败重试。
func (p *Pipeline) generateWord(ctx context.Context, sess AudioSession, j job, card *store.Card) error {
	pcm, err := sess.Speak(ctx, llm.BuildWordScript(card.Word))
	if err != nil {
		return err
	}
	if dur := wav.Duration(len(pcm), llm.LiveSampleRate); dur < 500*time.Millisecond {
		return fmt.Errorf("word 音频过短（%s），疑似生成失败", dur.Round(time.Millisecond))
	}
	if _, err := wav.SplitBySilence(pcm, llm.LiveSampleRate, 2); err != nil {
		return fmt.Errorf("word 音频切不出慢速/常速两遍（blend 收尾依赖）: %w", err)
	}
	return p.store.WriteAudio(j.Slug, store.AudioWord, wav.Encode(pcm, llm.LiveSampleRate))
}

// errWordAudioUnusable 标记 word.wav 无法当作 blend 收尾段的素材
// （读不出/采样率不符/切不出两遍）。调用方删掉它重新生成，而不是重试 blend。
var errWordAudioUnusable = errors.New("word.wav 不可复用")

// wordTailSegments 从落盘的 word.wav 切出慢速遍与常速遍。blend 收尾段的
// 整词音频从这里来——与"整词"按钮播放的是同一份录音，听感同源。
func (p *Pipeline) wordTailSegments(slug string) (slow, natural []byte, err error) {
	data, err := os.ReadFile(p.store.AudioPath(slug, store.AudioWord))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", errWordAudioUnusable, err)
	}
	pcm, rate, err := wav.Decode(data)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", errWordAudioUnusable, err)
	}
	if rate != llm.LiveSampleRate {
		return nil, nil, fmt.Errorf("%w: 采样率 %d ≠ %d", errWordAudioUnusable, rate, llm.LiveSampleRate)
	}
	segs, err := wav.SplitBySilence(pcm, rate, 2)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", errWordAudioUnusable, err)
	}
	return segs[0], segs[1], nil
}

// blend 重组时插入的固定静音——拼读节奏由这些常量决定，而不是求模型
// "停顿一秒"（模型轮次里的大停顿只是分割依据，重组时全部替换掉）。
const (
	gapAfterChunk    = 250 * time.Millisecond // chunk 之间
	gapAfterSyllable = 600 * time.Millisecond // 一个音节收尾后
	gapBeforeTail    = 800 * time.Millisecond // 进入"连读+整词"前
)

// 收尾段（音节串读 + 整词）的紧凑化参数："拼音串读"的感觉——音节快放、
// 间隔极短，最后自然语速出整词。
const (
	tailSylGap   = 120 * time.Millisecond // 串读音节之间
	tailWordGap  = 400 * time.Millisecond // 串读结束到整词
	tailSylSpeed = 1.15                   // 串读音节的快放倍率（整词不加速）
)

// assembleTail 本地拼装收尾段，不再让模型单独朗读一轮：串读音节直接复用
// 主体轮切出的音节段（与点读同一份录音，只是快放），整词段来自 word.wav。
// 单音节词没有串读，收尾就是整词段本身。wordOff 是整词部分在返回 PCM 内
// 的字节偏移——cues 据此给整词部分单独标 "word" 段（点大字区播放用）。
func assembleTail(sylSegs [][]byte, wordPCM []byte) (pcm []byte, wordOff int) {
	if len(sylSegs) == 0 {
		return wordPCM, 0
	}
	var out []byte
	for i, s := range sylSegs {
		if i > 0 {
			out = append(out, wav.Silence(tailSylGap, llm.LiveSampleRate)...)
		}
		out = append(out, wav.Speedup(s, tailSylSpeed)...)
	}
	out = append(out, wav.Silence(tailWordGap, llm.LiveSampleRate)...)
	return append(out, wordPCM...), len(out)
}

// segDurBounds 是各类分段的时长上下限，越界视为该轮生成失败（模型没按
// 脚本读：夹带了 hint、漏读、或把多个条目连在一起）。
func segDurBounds(kind llm.BlendKind) (lo, hi time.Duration) {
	switch kind {
	case llm.BlendChunk:
		return 150 * time.Millisecond, 5 * time.Second
	case llm.BlendSyllable:
		return 250 * time.Millisecond, 5 * time.Second
	default: // tail：音节连读 + 整词，长词会比较长
		return 250 * time.Millisecond, 20 * time.Second
	}
}

// generateBlend 生成并落盘拼读音频与时间标注。整词段从已落盘的 word.wav
// 切出（切不出返回 errWordAudioUnusable，调用方删掉 word.wav 重来）。
// 写入顺序：先 cues 后 wav——完成态只看 wav，保证 wav 在则 cues 必在。
func (p *Pipeline) generateBlend(ctx context.Context, sess AudioSession, j job, card *store.Card) error {
	wordSlow, wordNatural, err := p.wordTailSegments(j.Slug)
	if err != nil {
		return err
	}
	wavData, cues, err := BlendAudio(ctx, sess, card, wordSlow, wordNatural)
	if err != nil {
		return err
	}
	if err := p.store.WriteCues(j.Slug, cues); err != nil {
		return err
	}
	return p.store.WriteAudio(j.Slug, store.AudioBlend, wavData)
}

// BlendAudio 生成拼读音频（WAV 字节）与时间标注：主体轮连续朗读全部条目
// → 按已知条目数做静音分割 → 收尾段本地拼装 → 修剪后按固定间隔重组。
// 收尾段不再让模型单独朗读：串读音节复用主体轮的音节段（与点读同源），
// 整词用调用方从 word.wav 切出的两遍——多音节词接常速遍（串读后自然收束），
// 单音节词用慢速遍（chunk 拼完直接出清晰整词）。同一份录音三处复用，
// 点读、拼读收尾、整词按钮的听感不会互相漂移。
// cues 的毫秒偏移由重组过程直接构造（PCM 字节数 ÷ 采样字节率），零误差。
// 导出给 cmd/spike 复用，保证验证程序走的是服务端同一条代码路径。
func BlendAudio(ctx context.Context, sess AudioSession, card *store.Card, wordSlow, wordNatural []byte) ([]byte, *store.Cues, error) {
	lines := llm.BuildBlendLines(card)
	body := lines[:len(lines)-1]

	bodyPCM, err := sess.Speak(ctx, llm.BlendBodyScript(body))
	if err != nil {
		return nil, nil, err
	}
	segs, err := wav.SplitBySilence(bodyPCM, llm.LiveSampleRate, len(body))
	if err != nil {
		return nil, nil, fmt.Errorf("blend 主体轮%w", err)
	}
	var sylSegs [][]byte
	for i, ln := range body {
		if ln.Kind == llm.BlendSyllable {
			sylSegs = append(sylSegs, segs[i])
		}
	}
	tailWord := wordNatural
	if len(sylSegs) == 0 {
		tailWord = wordSlow
	}
	tailPCM, tailWordOff := assembleTail(sylSegs, tailWord)
	segs = append(segs, tailPCM)

	for i, seg := range segs {
		d := wav.Duration(len(seg), llm.LiveSampleRate)
		if lo, hi := segDurBounds(lines[i].Kind); d < lo || d > hi {
			return nil, nil, fmt.Errorf("blend 第 %d 段（%s）时长 %s 超出 [%s, %s]，疑似朗读未按脚本",
				i, lines[i].Kind, d.Round(time.Millisecond), lo, hi)
		}
	}

	var pcm []byte
	cues := &store.Cues{Version: 1, SampleRate: llm.LiveSampleRate}
	msAt := func() int { return int(wav.Duration(len(pcm), llm.LiveSampleRate).Milliseconds()) }
	for i, seg := range segs {
		ln := lines[i]
		if i > 0 {
			gap := gapAfterChunk
			switch {
			case ln.Kind == llm.BlendTail:
				gap = gapBeforeTail
			case lines[i-1].Kind == llm.BlendSyllable:
				gap = gapAfterSyllable
			}
			pcm = append(pcm, wav.Silence(gap, llm.LiveSampleRate)...)
		}
		start := msAt()
		pcm = append(pcm, seg...)
		end := msAt()

		switch ln.Kind {
		case llm.BlendChunk:
			cues.Cues = append(cues.Cues, store.Cue{Kind: "chunk", Syllable: ln.Syllable, Chunk: ln.Chunk, StartMS: start, EndMS: end})
		case llm.BlendSyllable:
			// 单 chunk 音节（如 tion）没有独立 chunk 段，这一段同时充当
			// 该 chunk 的点读段
			if ln.Chunk >= 0 {
				cues.Cues = append(cues.Cues, store.Cue{Kind: "chunk", Syllable: ln.Syllable, Chunk: ln.Chunk, StartMS: start, EndMS: end})
			}
			cues.Cues = append(cues.Cues, store.Cue{Kind: "syllable", Syllable: ln.Syllable, Chunk: -1, StartMS: start, EndMS: end})
		default:
			cues.Cues = append(cues.Cues, store.Cue{Kind: "tail", Syllable: -1, Chunk: -1, StartMS: start, EndMS: end})
			// tail 段内的整词部分再标一个 "word" 子区间：点大字区单独播
			// 一遍完整读音。必须排在 tail 之后（见 store.Cue 注释的顺序约定）
			wordStart := start + int(wav.Duration(tailWordOff, llm.LiveSampleRate).Milliseconds())
			cues.Cues = append(cues.Cues, store.Cue{Kind: "word", Syllable: -1, Chunk: -1, StartMS: wordStart, EndMS: end})
		}
	}
	return wav.Encode(pcm, llm.LiveSampleRate), cues, nil
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
