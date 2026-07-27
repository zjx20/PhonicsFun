// spike 是 Live API 链路的独立验证程序（对应实现计划里的 M0）：
// 建会话 → 发拼读轮/整词轮 → 收 PCM → 写 WAV，复用服务端同一套 llm 包代码。
//
// 用法：
//
//	GEMINI_API_KEY=... go run ./cmd/spike [-word cake] [-out ./spike-out] [-text]
//
// -text 额外验证 generateContent 文本链路（生成真实卡片再据此发音）；
// 默认用内置示例卡，不消耗文本模型配额。
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
	"phonicsfun/internal/store"
	"phonicsfun/internal/wav"
)

func main() {
	word := flag.String("word", "cake", "要拼读的单词")
	out := flag.String("out", "./spike-out", "WAV 输出目录")
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

	log.Printf("连接 Live (%s, voice=%s) ...", cfg.LiveModel, cfg.Voice)
	start := time.Now()
	sess, err := client.ConnectLive(ctx)
	if err != nil {
		log.Fatalf("ConnectLive: %v（若卡在连接，检查 HTTPS_PROXY 是否被 WebSocket 层遵守）", err)
	}
	defer sess.Close()
	log.Printf("会话已建立 (%s)", time.Since(start).Round(time.Millisecond))

	turns := []struct {
		name   string
		script string
	}{
		{"blend", llm.BuildBlendScript(card)},
		{"word", llm.BuildWordScript(card.Word)},
	}
	for _, t := range turns {
		log.Printf("--- %s 轮脚本 ---\n%s", t.name, t.script)
		start := time.Now()
		pcm, err := sess.Speak(ctx, t.script)
		if err != nil {
			log.Fatalf("Speak(%s): %v", t.name, err)
		}
		dur := wav.Duration(len(pcm), llm.LiveSampleRate)
		path := filepath.Join(*out, fmt.Sprintf("%s-%s.wav", store.Slug(card.Word), t.name))
		if err := os.WriteFile(path, wav.Encode(pcm, llm.LiveSampleRate), 0o644); err != nil {
			log.Fatal(err)
		}
		log.Printf("%s: %d bytes PCM, 音频时长 %s, 耗时 %s → %s",
			t.name, len(pcm), dur.Round(time.Millisecond), time.Since(start).Round(time.Millisecond), path)
	}
	sess.WordDone()
	log.Printf("完成。请人耳试听 %s 下的 WAV 文件。", *out)
}

// sampleCard 返回一张手工构造的示例卡（magic-e 词 cake 覆盖 silent chunk 路径）。
func sampleCard(word string) *store.Card {
	if word == "cake" {
		return &store.Card{
			Word: "cake",
			Chunks: []store.Chunk{
				{Grapheme: "c", Phoneme: "/k/", Respell: "k", AnchorWord: "kite"},
				{Grapheme: "a", Phoneme: "/eɪ/", Respell: "ay", AnchorWord: "name"},
				{Grapheme: "k", Phoneme: "/k/", Respell: "k", AnchorWord: "kite"},
				{Grapheme: "e", Silent: true},
			},
		}
	}
	// 其他词退化为逐字母拼读（仅供连通性测试；真实拆解靠 -text）
	c := &store.Card{Word: word}
	for _, r := range store.Slug(word) {
		c.Chunks = append(c.Chunks, store.Chunk{
			Grapheme: string(r), Respell: string(r), AnchorWord: word,
		})
	}
	return c
}
