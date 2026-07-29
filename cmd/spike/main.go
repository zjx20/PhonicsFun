// spike 是 Live API 链路的独立验证程序（对应实现计划里的 M0）：
// 建会话 → 整词轮（慢速+常速，切出两遍）→ blend 主体轮（连续朗读→静音
// 分割→本地拼装收尾段→重组+时间标注），复用服务端同一套 llm/pipeline
// 包代码路径。
//
// 用法：
//
//	GEMINI_API_KEY=... go run ./cmd/spike [-word action] [-out ./spike-out] [-text]
//
// -text 额外验证 generateContent 文本链路（生成真实卡片再据此发音）；
// 默认用内置示例卡，不消耗文本模型配额。输出除成品 blend/word WAV 与
// cues JSON 外，还保存分割前的两轮原始音频（raw-body/raw-tail），便于
// 排查静音分割问题。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"phonicsfun/internal/config"
	"phonicsfun/internal/llm"
	"phonicsfun/internal/pipeline"
	"phonicsfun/internal/store"
	"phonicsfun/internal/wav"
)

func main() {
	word := flag.String("word", "action", "要拼读的单词")
	out := flag.String("out", "./spike-out", "输出目录")
	genText := flag.Bool("text", false, "先用文本模型生成真实卡片（额外验证 generateContent 链路）")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("配置: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client, err := llm.New(ctx, cfg)
	if err != nil {
		log.Fatalf("客户端: %v", err)
	}

	card := sampleCard(*word)
	if *genText {
		log.Printf("生成卡片文本: %s ...", *word)
		start := time.Now()
		card, err = client.GenerateCard(ctx, *word)
		if err != nil {
			log.Fatalf("GenerateCard: %v", err)
		}
		j, _ := json.MarshalIndent(card, "", "  ")
		log.Printf("卡片 (%s):\n%s", time.Since(start).Round(time.Millisecond), j)
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	slug := store.Slug(card.Word)

	log.Printf("连接 Live (%s, voice=%s) ...", cfg.LiveModel, cfg.Voice)
	start := time.Now()
	sess, err := client.ConnectLive(ctx)
	if err != nil {
		log.Fatalf("ConnectLive: %v（若卡在连接，检查 HTTPS_PROXY 是否被 WebSocket 层遵守）", err)
	}
	defer sess.Close()
	log.Printf("会话已建立 (%s)", time.Since(start).Round(time.Millisecond))

	// word 轮在前：blend 收尾段要从中切出慢速/常速两遍复用
	rec := &recordingSession{inner: sess, out: *out, slug: slug}
	log.Printf("--- word 轮脚本 ---\n%s", llm.BuildWordScript(card.Word))
	start = time.Now()
	pcm, err := rec.Speak(ctx, llm.BuildWordScript(card.Word))
	if err != nil {
		log.Fatalf("Speak(word): %v", err)
	}
	wordSegs, err := wav.SplitBySilence(pcm, llm.LiveSampleRate, 2)
	if err != nil {
		log.Fatalf("word 音频切不出慢速/常速两遍: %v（原始音频已存 %s）", err, *out)
	}
	wordPath := filepath.Join(*out, slug+"-word.wav")
	if err := os.WriteFile(wordPath, wav.Encode(pcm, llm.LiveSampleRate), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("word: 音频时长 %s（慢速遍 %s / 常速遍 %s）, 耗时 %s → %s",
		wav.Duration(len(pcm), llm.LiveSampleRate).Round(time.Millisecond),
		wav.Duration(len(wordSegs[0]), llm.LiveSampleRate).Round(time.Millisecond),
		wav.Duration(len(wordSegs[1]), llm.LiveSampleRate).Round(time.Millisecond),
		time.Since(start).Round(time.Millisecond), wordPath)

	// blend：走 pipeline.BlendAudio 同一条路径（收尾段本地拼装，无收尾轮）
	log.Printf("--- blend 主体轮脚本 ---\n%s", llm.BlendBodyScript(llm.BuildBlendLines(card)))
	start = time.Now()
	wavData, cues, err := pipeline.BlendAudio(ctx, rec, card, wordSegs[0], wordSegs[1])
	if err != nil {
		log.Fatalf("BlendAudio: %v（原始音频已存 %s，可人耳排查停顿）", err, *out)
	}
	blendPath := filepath.Join(*out, slug+"-blend.wav")
	if err := os.WriteFile(blendPath, wavData, 0o644); err != nil {
		log.Fatal(err)
	}
	cuesJSON, _ := json.MarshalIndent(cues, "", "  ")
	cuesPath := filepath.Join(*out, slug+"-blend.cues.json")
	if err := os.WriteFile(cuesPath, cuesJSON, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("blend 完成，耗时 %s → %s", time.Since(start).Round(time.Millisecond), blendPath)
	log.Printf("cues（核对每段起止与听感是否一致）:\n%s", cuesJSON)

	sess.WordDone()
	log.Printf("完成。请人耳试听 %s 下的 WAV，重点确认 blend 的\"逐块拼→合音节→连读成词\"节奏。", *out)
}

// recordingSession 包装真实会话，把每轮 Speak 的原始 PCM 存为
// <slug>-raw-<n>.wav，便于对比分割前后的音频。
type recordingSession struct {
	inner pipeline.AudioSession
	out   string
	slug  string
	n     int
}

func (r *recordingSession) Speak(ctx context.Context, script string) ([]byte, error) {
	pcm, err := r.inner.Speak(ctx, script)
	if pcm != nil {
		r.n++
		path := filepath.Join(r.out, fmt.Sprintf("%s-raw-%d.wav", r.slug, r.n))
		if werr := os.WriteFile(path, wav.Encode(pcm, llm.LiveSampleRate), 0o644); werr == nil {
			log.Printf("原始轮次音频 → %s（%s）", path, wav.Duration(len(pcm), llm.LiveSampleRate).Round(time.Millisecond))
		}
	}
	return pcm, err
}
func (r *recordingSession) WordDone()           { r.inner.WordDone() }
func (r *recordingSession) NeedsRotation() bool { return r.inner.NeedsRotation() }
func (r *recordingSession) Close() error        { return r.inner.Close() }

// sampleCard 返回手工构造的 v2 示例卡；action 覆盖多音节 + 单 chunk 音节
// （tion），cake 覆盖 magic-e silent chunk。其他词退化为单音节逐字母拼读
// （仅供连通性测试；真实拆解靠 -text）。
func sampleCard(word string) *store.Card {
	switch word {
	case "action":
		return &store.Card{
			Schema: store.CardSchemaVersion, Word: "action", IPA: "/ˈækʃən/",
			Senses:   []store.Sense{{POS: "n.", ZH: "行动；动作", EN: "something you do"}},
			Examples: []store.Example{{EN: "Let's take action now.", ZH: "我们现在就行动吧。"}},
			Syllables: []store.Syllable{
				{Text: "ac", Respell: "ak", Chunks: []store.Chunk{
					{Grapheme: "a", Phoneme: "/æ/", Respell: "a", AnchorWord: "apple"},
					{Grapheme: "c", Phoneme: "/k/", Respell: "k", AnchorWord: "cat"},
				}},
				{Text: "tion", Respell: "shun", Chunks: []store.Chunk{
					{Grapheme: "tion", Phoneme: "/ʃən/", Respell: "shun", AnchorWord: "station"},
				}},
			},
		}
	case "cake":
		return &store.Card{
			Schema: store.CardSchemaVersion, Word: "cake", IPA: "/keɪk/",
			Senses:   []store.Sense{{POS: "n.", ZH: "蛋糕", EN: "a sweet baked food"}},
			Examples: []store.Example{{EN: "Mom made a big cake.", ZH: "妈妈做了一个大蛋糕。"}},
			Syllables: []store.Syllable{{Text: "cake", Respell: "kayk", Chunks: []store.Chunk{
				{Grapheme: "c", Phoneme: "/k/", Respell: "k", AnchorWord: "kite"},
				{Grapheme: "a", Phoneme: "/eɪ/", Respell: "ay", AnchorWord: "name"},
				{Grapheme: "k", Phoneme: "/k/", Respell: "k", AnchorWord: "kite"},
				{Grapheme: "e", Silent: true},
			}}},
		}
	}
	c := &store.Card{Schema: store.CardSchemaVersion, Word: word}
	syl := store.Syllable{Text: store.Slug(word), Respell: word}
	for _, r := range store.Slug(word) {
		syl.Chunks = append(syl.Chunks, store.Chunk{
			Grapheme: string(r), Respell: string(r), AnchorWord: word,
		})
	}
	c.Syllables = []store.Syllable{syl}
	return c
}
