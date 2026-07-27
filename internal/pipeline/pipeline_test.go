package pipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"phonicsfun/internal/store"
)

// fakeLLM 以确定性方式模拟模型：文本生成逐字母拆卡，音频返回足够长的 PCM。
type fakeLLM struct {
	mu           sync.Mutex
	genCalls     []string
	genFail      map[string]int // word → 前 N 次调用返回可重试错误
	sessions     int
	speakPerSess []int
}

func (f *fakeLLM) GenerateCard(_ context.Context, word string) (*store.Card, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.genCalls = append(f.genCalls, word)
	if n := f.genFail[word]; n > 0 {
		f.genFail[word] = n - 1
		return nil, errors.New("fake transient failure")
	}
	c := &store.Card{Word: word, IPA: "/x/", DefinitionZH: "测", DefinitionEN: "test", Model: "fake"}
	for _, r := range word {
		c.Chunks = append(c.Chunks, store.Chunk{Grapheme: string(r), Phoneme: "/x/", Respell: string(r), AnchorWord: "x"})
	}
	return c, nil
}

func (f *fakeLLM) ConnectLive(_ context.Context) (AudioSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions++
	f.speakPerSess = append(f.speakPerSess, 0)
	return &fakeSession{llm: f, idx: len(f.speakPerSess) - 1}, nil
}

type fakeSession struct {
	llm   *fakeLLM
	idx   int
	words int
}

func (s *fakeSession) Speak(_ context.Context, _ string) ([]byte, error) {
	s.llm.mu.Lock()
	s.llm.speakPerSess[s.idx]++
	s.llm.mu.Unlock()
	return make([]byte, 24000*2*10), nil // 10 秒 PCM，任何 sanity check 都能过
}
func (s *fakeSession) WordDone()            { s.words++ }
func (s *fakeSession) NeedsRotation() bool  { return s.words >= 2 } // 测试用小批量
func (s *fakeSession) Close() error         { return nil }

func newTestPipeline(t *testing.T, f *fakeLLM) (*Pipeline, *store.Store) {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := New(st, f)
	p.backoff = []time.Duration{0, 0, 0, 0, 0} // 测试不等退避
	return p, st
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待超时")
}

func allDone(p *Pipeline, words []string) bool {
	for _, w := range words {
		st := p.Status(w)
		if st.Text != StateDone || st.Audio != StateDone {
			return false
		}
	}
	return true
}

func TestEndToEndGeneration(t *testing.T) {
	f := &fakeLLM{}
	p, st := newTestPipeline(t, f)
	p.Start(context.Background())
	defer p.Stop()

	words := []string{"cat", "dog", "fish", "bird", "cow"}
	p.EnqueueWords(words)
	waitFor(t, func() bool { return allDone(p, words) })

	for _, w := range words {
		slug := store.Slug(w)
		if !st.HasCard(slug) || !st.HasAudio(slug, store.AudioBlend) || !st.HasAudio(slug, store.AudioWord) {
			t.Errorf("%s 产物不齐", w)
		}
	}
	// 会话轮换：5 词、每会话 2 词 → 至少 3 个会话
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sessions < 3 {
		t.Errorf("sessions = %d, 轮换未生效", f.sessions)
	}
	// 每会话 speak 次数 = 词数×2，不超过 4
	for i, n := range f.speakPerSess {
		if n > 4 {
			t.Errorf("会话 %d speak %d 次，超过每会话 2 词上限", i, n)
		}
	}
}

func TestSkipExistingAndRecover(t *testing.T) {
	f := &fakeLLM{}
	p, st := newTestPipeline(t, f)

	// 预置 cat 的全部产物、dog 只有卡片
	for _, w := range []string{"cat", "dog"} {
		card, _ := f.GenerateCard(context.Background(), w)
		if err := st.WriteCard(store.Slug(w), card); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range []store.AudioKind{store.AudioWord, store.AudioBlend} {
		if err := st.WriteAudio(store.Slug("cat"), k, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.CreateGroup("g", []string{"cat", "dog", "new"}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.genCalls = nil
	f.mu.Unlock()

	p.Start(context.Background())
	defer p.Stop()
	if err := p.Recover(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return allDone(p, []string{"cat", "dog", "new"}) })

	f.mu.Lock()
	defer f.mu.Unlock()
	// cat 全齐、dog 有卡 → 只有 new 需要生成文本
	if len(f.genCalls) != 1 || f.genCalls[0] != "new" {
		t.Errorf("genCalls = %v, 只应生成 new", f.genCalls)
	}
}

func TestRetryThenFail(t *testing.T) {
	f := &fakeLLM{genFail: map[string]int{"bad": 100}} // 永远失败
	p, _ := newTestPipeline(t, f)
	p.backoff = []time.Duration{0, 0} // 3 次尝试
	p.Start(context.Background())
	defer p.Stop()

	p.EnqueueWords([]string{"bad"})
	waitFor(t, func() bool { return p.Status("bad").Text == StateFailed })

	f.mu.Lock()
	calls := len(f.genCalls)
	f.mu.Unlock()
	if calls != 3 {
		t.Errorf("尝试 %d 次, 期望 3（1 + 2 重试）", calls)
	}

	// 瞬时失败后成功
	f2 := &fakeLLM{genFail: map[string]int{"flaky": 2}}
	p2, _ := newTestPipeline(t, f2)
	p2.Start(context.Background())
	defer p2.Stop()
	p2.EnqueueWords([]string{"flaky"})
	waitFor(t, func() bool { return allDone(p2, []string{"flaky"}) })
}

func TestRegenerateCascade(t *testing.T) {
	f := &fakeLLM{}
	p, _ := newTestPipeline(t, f)
	p.Start(context.Background())
	defer p.Stop()

	p.EnqueueWords([]string{"cat"})
	waitFor(t, func() bool { return allDone(p, []string{"cat"}) })

	f.mu.Lock()
	genBefore, speaksBefore := len(f.genCalls), totalSpeaks(f)
	f.mu.Unlock()

	// text 重生成必须级联音频
	if err := p.Regenerate("cat", RegenText); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return allDone(p, []string{"cat"}) })
	f.mu.Lock()
	if len(f.genCalls) != genBefore+1 {
		t.Errorf("regenerate text 未触发文本生成")
	}
	if totalSpeaks(f) != speaksBefore+2 {
		t.Errorf("regenerate text 未级联音频重生: speaks %d → %d", speaksBefore, totalSpeaks(f))
	}
	f.mu.Unlock()

	// audio-only 重生成不触发文本
	f.mu.Lock()
	genBefore, speaksBefore = len(f.genCalls), totalSpeaks(f)
	f.mu.Unlock()
	if err := p.Regenerate("cat", RegenAudio); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return allDone(p, []string{"cat"}) })
	f.mu.Lock()
	if len(f.genCalls) != genBefore {
		t.Errorf("regenerate audio 不应触发文本生成")
	}
	if totalSpeaks(f) != speaksBefore+2 {
		t.Errorf("regenerate audio 未重生音频")
	}
	f.mu.Unlock()

	if err := p.Regenerate("cat", RegenTarget("bogus")); err == nil {
		t.Error("未知 target 应报错")
	}
}

func totalSpeaks(f *fakeLLM) int {
	n := 0
	for _, s := range f.speakPerSess {
		n += s
	}
	return n
}

func TestStatusDerivation(t *testing.T) {
	f := &fakeLLM{}
	p, st := newTestPipeline(t, f)
	// 不启动 worker：纯推导
	if got := p.Status("ghost"); got.Text != StatePending || got.Audio != StatePending {
		t.Errorf("未知词应为 pending: %+v", got)
	}
	card, _ := f.GenerateCard(context.Background(), "cat")
	if err := st.WriteCard("cat", card); err != nil {
		t.Fatal(err)
	}
	if got := p.Status("cat"); got.Text != StateDone || got.Audio != StatePending {
		t.Errorf("有卡无音频应为 done/pending: %+v", got)
	}
}
