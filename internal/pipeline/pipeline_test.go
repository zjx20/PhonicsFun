package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"phonicsfun/internal/llm"
	"phonicsfun/internal/store"
	"phonicsfun/internal/wav"
)

// fakeTone 生成幅度足够过静音阈值的方波 PCM。
func fakeTone(d time.Duration) []byte {
	n := int(d * llm.LiveSampleRate / time.Second)
	out := make([]byte, n*2)
	for i := 0; i < n; i++ {
		v := int16(18000)
		if (i/24)%2 == 0 {
			v = -18000
		}
		out[i*2] = byte(uint16(v))
		out[i*2+1] = byte(uint16(v) >> 8)
	}
	return out
}

// fakeSpeech 模拟"逐条目朗读、条目间停顿"的一轮输出：items 段语音，段间
// 700ms 静音，首尾各 300ms 静音。
func fakeSpeech(items int) []byte {
	var out []byte
	out = append(out, wav.Silence(300*time.Millisecond, llm.LiveSampleRate)...)
	for i := 0; i < items; i++ {
		if i > 0 {
			out = append(out, wav.Silence(700*time.Millisecond, llm.LiveSampleRate)...)
		}
		out = append(out, fakeTone(400*time.Millisecond)...)
	}
	return append(out, wav.Silence(300*time.Millisecond, llm.LiveSampleRate)...)
}

// fakeLLM 以确定性方式模拟模型：文本生成产出单音节逐字母 v2 卡；音频侧
// 按脚本条目数返回带静音间隔的分段 PCM（驱动真实的分割/重组路径）。
type fakeLLM struct {
	mu           sync.Mutex
	genCalls     []string
	genFeedbacks []string       // 与 genCalls 一一对应的 feedback 参数
	genFail      map[string]int // word → 前 N 次调用返回可重试错误
	sessions     int
	speakPerSess []int
}

func (f *fakeLLM) GenerateCard(_ context.Context, word, feedback string) (*store.Card, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.genCalls = append(f.genCalls, word)
	f.genFeedbacks = append(f.genFeedbacks, feedback)
	if n := f.genFail[word]; n > 0 {
		f.genFail[word] = n - 1
		return nil, errors.New("fake transient failure")
	}
	c := &store.Card{
		Schema: store.CardSchemaVersion, Word: word, IPA: "/x/", Model: "fake",
		Senses:   []store.Sense{{POS: "n.", ZH: "测", EN: "test"}},
		Examples: []store.Example{{EN: "test " + word + ".", ZH: "测。"}},
	}
	syl := store.Syllable{Text: word, Respell: word}
	for _, r := range word {
		syl.Chunks = append(syl.Chunks, store.Chunk{Grapheme: string(r), Phoneme: "/x/", Respell: string(r), AnchorWord: "x"})
	}
	c.Syllables = []store.Syllable{syl}
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

func (s *fakeSession) Speak(_ context.Context, script string) ([]byte, error) {
	s.llm.mu.Lock()
	s.llm.speakPerSess[s.idx]++
	s.llm.mu.Unlock()
	// blend 主体轮脚本的条目行以 `- "` 开头，按条目数返回分段语音；
	// word 轮返回"慢速+常速"两段（脚本要求两遍之间停顿）
	if n := strings.Count(script, `- "`); n > 0 {
		return fakeSpeech(n), nil
	}
	return fakeSpeech(2), nil
}
func (s *fakeSession) WordDone()           { s.words++ }
func (s *fakeSession) NeedsRotation() bool { return s.words >= 2 } // 测试用小批量
func (s *fakeSession) Close() error        { return nil }

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
	// 每会话 speak 次数 = 词数×2（整词、blend 主体；收尾段本地拼装不占轮次），
	// 不超过 4
	for i, n := range f.speakPerSess {
		if n > 4 {
			t.Errorf("会话 %d speak %d 次，超过每会话 2 词上限", i, n)
		}
	}

	// cues 与 blend.wav 成对生成，且时间轴由重组构造：单音节 cat →
	// 3 个 chunk 段 + 1 个 tail 段 + tail 内的 word 子区间，
	// 起止单调递增、段间有插入的静音间隔（word 与 tail 重叠，不参与间隔校验）
	data, err := os.ReadFile(st.CuesPath(store.Slug("cat")))
	if err != nil {
		t.Fatalf("cues 未落盘: %v", err)
	}
	var cues store.Cues
	if err := json.Unmarshal(data, &cues); err != nil {
		t.Fatal(err)
	}
	if cues.SampleRate != llm.LiveSampleRate || len(cues.Cues) != 5 {
		t.Fatalf("cues 结构错误: %+v", cues)
	}
	wantKinds := []string{"chunk", "chunk", "chunk", "tail", "word"}
	for i, c := range cues.Cues {
		if c.Kind != wantKinds[i] {
			t.Errorf("cue %d kind = %s, want %s", i, c.Kind, wantKinds[i])
		}
		if c.EndMS <= c.StartMS {
			t.Errorf("cue %d 起止非法: %+v", i, c)
		}
		if i > 0 && c.Kind != "word" && c.StartMS < cues.Cues[i-1].EndMS+200 {
			t.Errorf("cue %d 与前段间隔不足（重组静音缺失）: %+v", i, cues.Cues)
		}
	}
	if cues.Cues[0].Chunk != 0 || cues.Cues[2].Chunk != 2 || cues.Cues[3].Chunk != -1 {
		t.Errorf("cue 下标错误: %+v", cues.Cues)
	}
	// 单音节词收尾无串读：word 子区间就是整个 tail 段
	if w, tl := cues.Cues[4], cues.Cues[3]; w.StartMS != tl.StartMS || w.EndMS != tl.EndMS {
		t.Errorf("单音节 word 子区间应与 tail 重合: %+v vs %+v", w, tl)
	}
}

// scriptedSession 按预置顺序逐轮返回 PCM，用于直接测 BlendAudio。
type scriptedSession struct {
	outs [][]byte
	i    int
}

func (s *scriptedSession) Speak(_ context.Context, _ string) ([]byte, error) {
	o := s.outs[s.i]
	s.i++
	return o, nil
}
func (s *scriptedSession) WordDone()           {}
func (s *scriptedSession) NeedsRotation() bool { return false }
func (s *scriptedSession) Close() error        { return nil }

// twoSylCard: ac + tion，body 条目 = a、c、ak(音节)、shun(单 chunk 音节) 共 4 段。
func twoSylCard() *store.Card {
	return &store.Card{
		Schema: store.CardSchemaVersion, Word: "action",
		Syllables: []store.Syllable{
			{Text: "ac", Respell: "ak", Chunks: []store.Chunk{
				{Grapheme: "a", Respell: "a", AnchorWord: "apple"},
				{Grapheme: "c", Respell: "k", AnchorWord: "cat"},
			}},
			{Text: "tion", Respell: "shun", Chunks: []store.Chunk{
				{Grapheme: "tion", Respell: "shun", AnchorWord: "station"},
			}},
		},
	}
}

func TestBlendAudioTailAssembly(t *testing.T) {
	// 只有主体轮一次 Speak；收尾段由传入的 word.wav 两遍本地拼装：
	// 慢速遍（承担音节串读）+ tailWordGap + 常速遍
	sess := &scriptedSession{outs: [][]byte{fakeSpeech(4)}}
	slow, natural := fakeTone(900*time.Millisecond), fakeTone(600*time.Millisecond)
	_, cues, err := BlendAudio(context.Background(), sess, twoSylCard(), slow, natural)
	if err != nil {
		t.Fatal(err)
	}
	if sess.i != 1 {
		t.Errorf("Speak %d 次，收尾段不应再单独朗读", sess.i)
	}
	// cues：chunk a、chunk c、syllable ac、(chunk+syllable) tion、tail、word
	kinds := []string{}
	for _, c := range cues.Cues {
		kinds = append(kinds, c.Kind)
	}
	want := []string{"chunk", "chunk", "syllable", "chunk", "syllable", "tail", "word"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("cues kinds = %v", kinds)
	}
	tail := cues.Cues[len(cues.Cues)-2]
	tailDur := tail.EndMS - tail.StartMS
	// 收尾段不经静音分割、无 pad：精确 = 900(慢速) + 400(gap) + 600(常速)
	if tailDur < 1880 || tailDur > 1920 {
		t.Errorf("拼装后 tail 时长 = %dms，期望 1900ms", tailDur)
	}
	// word 子区间 = tail 里最后的整词常速遍：起点在慢速遍+gap 之后、终点与 tail 一致
	word := cues.Cues[len(cues.Cues)-1]
	if word.EndMS != tail.EndMS || word.StartMS <= tail.StartMS {
		t.Errorf("word 子区间越界: word=%+v tail=%+v", word, tail)
	}
	if d := word.EndMS - word.StartMS; d < 590 || d > 610 {
		t.Errorf("word 子区间时长 = %dms，应等于常速遍 600ms", d)
	}
	if off := word.StartMS - tail.StartMS; off < 1290 || off > 1310 {
		t.Errorf("word 子区间起点偏移 = %dms，应为慢速遍 900 + 间隔 400", off)
	}

	// 单音节词：无串读，收尾段就是慢速遍本身
	single := &store.Card{
		Word: "cat",
		Syllables: []store.Syllable{{Text: "cat", Respell: "kat", Chunks: []store.Chunk{
			{Grapheme: "c", Respell: "k", AnchorWord: "cat"},
			{Grapheme: "a", Respell: "a", AnchorWord: "apple"},
			{Grapheme: "t", Respell: "t", AnchorWord: "top"},
		}}},
	}
	sess2 := &scriptedSession{outs: [][]byte{fakeSpeech(3)}}
	_, cues2, err := BlendAudio(context.Background(), sess2, single, slow, natural)
	if err != nil {
		t.Fatal(err)
	}
	t2 := cues2.Cues[len(cues2.Cues)-2]
	if d := t2.EndMS - t2.StartMS; d < 850 || d > 950 {
		t.Errorf("单音节 tail 时长 = %dms，应等于慢速遍 900ms", d)
	}
	if w2 := cues2.Cues[len(cues2.Cues)-1]; w2.Kind != "word" || w2.StartMS != t2.StartMS || w2.EndMS != t2.EndMS {
		t.Errorf("单音节 word 子区间应与 tail 重合: %+v vs %+v", w2, t2)
	}
}

func TestSchemaMigrationOnRecover(t *testing.T) {
	f := &fakeLLM{}
	p, st := newTestPipeline(t, f)

	// 手工落一张旧版卡（Schema 0）+ 假音频，模拟基线版本的存量数据
	old := &store.Card{Word: "cat", IPA: "/x/"}
	if err := st.WriteCard(store.Slug("cat"), old); err != nil {
		t.Fatal(err)
	}
	for _, k := range []store.AudioKind{store.AudioWord, store.AudioBlend} {
		if err := st.WriteAudio(store.Slug("cat"), k, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.CreateGroup("g", []string{"cat"}); err != nil {
		t.Fatal(err)
	}

	p.Start(context.Background())
	defer p.Stop()
	if err := p.Recover(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return allDone(p, []string{"cat"}) })

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.genCalls) != 1 || f.genCalls[0] != "cat" {
		t.Fatalf("旧版卡应触发重新生成, genCalls = %v", f.genCalls)
	}
	card, err := st.ReadCard(store.Slug("cat"))
	if err != nil || card.Schema != store.CardSchemaVersion {
		t.Errorf("迁移后卡片仍是旧版: %+v, %v", card, err)
	}
	if _, err := os.Stat(st.CuesPath(store.Slug("cat"))); err != nil {
		t.Error("迁移后 cues 未生成")
	}
}

func TestSkipExistingAndRecover(t *testing.T) {
	f := &fakeLLM{}
	p, st := newTestPipeline(t, f)

	// 预置 cat 的全部产物、dog 只有卡片
	for _, w := range []string{"cat", "dog"} {
		card, _ := f.GenerateCard(context.Background(), w, "")
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
	if err := p.Regenerate("cat", RegenText, ""); err != nil {
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
	if err := p.Regenerate("cat", RegenAudio, ""); err != nil {
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

	if err := p.Regenerate("cat", RegenTarget("bogus"), ""); err == nil {
		t.Error("未知 target 应报错")
	}
}

// TestRegenerateFeedback：用户反馈必须到达文本生成调用，且只对本次重新
// 生成生效——消费后不得残留到之后与它无关的生成。
func TestRegenerateFeedback(t *testing.T) {
	f := &fakeLLM{}
	p, _ := newTestPipeline(t, f)
	p.Start(context.Background())
	defer p.Stop()

	p.EnqueueWords([]string{"cat"})
	waitFor(t, func() bool { return allDone(p, []string{"cat"}) })
	f.mu.Lock()
	if f.genFeedbacks[len(f.genFeedbacks)-1] != "" {
		t.Errorf("常规生成不应带反馈: %q", f.genFeedbacks)
	}
	f.mu.Unlock()

	if err := p.Regenerate("cat", RegenText, "音标不对，应该是重音在第一音节"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return allDone(p, []string{"cat"}) })
	f.mu.Lock()
	if got := f.genFeedbacks[len(f.genFeedbacks)-1]; got != "音标不对，应该是重音在第一音节" {
		t.Errorf("反馈未到达 GenerateCard: %q", got)
	}
	f.mu.Unlock()

	// 再来一次不带反馈的重新生成：上次的反馈必须已被消费
	if err := p.Regenerate("cat", RegenBoth, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return allDone(p, []string{"cat"}) })
	f.mu.Lock()
	if got := f.genFeedbacks[len(f.genFeedbacks)-1]; got != "" {
		t.Errorf("旧反馈残留到了后续生成: %q", got)
	}
	f.mu.Unlock()
}

// TestAudioVersionBumpsOnRegenerate：audioVersion 是前端音频/cues URL 的
// 防缓存参数，audio-only 重生成（card.json 不动、generated_at 不变）后它
// 必须变化，否则浏览器会继续播缓存里的旧音频。
func TestAudioVersionBumpsOnRegenerate(t *testing.T) {
	f := &fakeLLM{}
	p, _ := newTestPipeline(t, f)
	p.Start(context.Background())
	defer p.Stop()

	p.EnqueueWords([]string{"cat"})
	waitFor(t, func() bool { return allDone(p, []string{"cat"}) })
	v1 := p.Status("cat").AudioVersion
	if v1 == 0 {
		t.Fatal("音频就绪后 audioVersion 应非零")
	}

	time.Sleep(10 * time.Millisecond) // mtime 是毫秒级，确保重写后可分辨
	if err := p.Regenerate("cat", RegenAudio, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return allDone(p, []string{"cat"}) })
	if v2 := p.Status("cat").AudioVersion; v2 <= v1 {
		t.Errorf("audio-only 重生成后 audioVersion 未变化: %d → %d", v1, v2)
	}
}

// TestUnusableWordAudioRegenerated：存量 word.wav 是旧脚本产物（没有两遍
// 之间的停顿，切不出慢速/常速）时，blend 生成应删掉它连同重新生成，最终
// 落盘的 word.wav 必须能切出两遍。
func TestUnusableWordAudioRegenerated(t *testing.T) {
	f := &fakeLLM{}
	p, st := newTestPipeline(t, f)

	card, _ := f.GenerateCard(context.Background(), "cat", "")
	if err := st.WriteCard(store.Slug("cat"), card); err != nil {
		t.Fatal(err)
	}
	unusable := wav.Encode(fakeTone(2*time.Second), llm.LiveSampleRate)
	if err := st.WriteAudio(store.Slug("cat"), store.AudioWord, unusable); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateGroup("g", []string{"cat"}); err != nil {
		t.Fatal(err)
	}

	p.Start(context.Background())
	defer p.Stop()
	if err := p.Recover(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return allDone(p, []string{"cat"}) })

	data, err := os.ReadFile(st.AudioPath(store.Slug("cat"), store.AudioWord))
	if err != nil {
		t.Fatal(err)
	}
	pcm, rate, err := wav.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wav.SplitBySilence(pcm, rate, 2); err != nil {
		t.Errorf("重新生成的 word.wav 仍切不出两遍: %v", err)
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
	card, _ := f.GenerateCard(context.Background(), "cat", "")
	if err := st.WriteCard("cat", card); err != nil {
		t.Fatal(err)
	}
	if got := p.Status("cat"); got.Text != StateDone || got.Audio != StatePending {
		t.Errorf("有卡无音频应为 done/pending: %+v", got)
	}
}
