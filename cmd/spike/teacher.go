package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"phonicsfun/internal/config"
	"phonicsfun/internal/llm"
	"phonicsfun/internal/wav"
)

// runTeacher 验证 AI 老师对话链路：注入 [CONTEXT] 便签后用文本轮提问，
// 确认老师"知道"当前单词且不朗读便签本身；再发一张测试照片提问，确认
// "拍照给老师看"链路可用。每轮回复 PCM 存 WAV 供试听。
func runTeacher(ctx context.Context, client *llm.Client, cfg *config.Config, out, persona string) {
	if err := os.MkdirAll(out, 0o755); err != nil {
		log.Fatal(err)
	}
	instr := llm.BuildTeacherInstruction(persona)
	log.Printf("连接 Teacher Live (%s, voice=%s) ...", cfg.TeacherModel, cfg.Voice)
	start := time.Now()
	sess, err := client.ConnectTeacher(ctx, llm.TeacherConfig{Instruction: instr}, "")
	if err != nil {
		log.Fatalf("ConnectTeacher: %v", err)
	}
	defer sess.Close()
	log.Printf("会话已建立 (%s)", time.Since(start).Round(time.Millisecond))

	// 与 httpapi 桥接完全相同的便签格式
	notes := []string{
		llm.ContextNoteGroup("7月30日 单词组", []string{"cat", "dog", "fish"}),
		llm.ContextNoteCard("cat", "/kæt/", "猫"),
	}
	for _, n := range notes {
		log.Printf("注入: %s", n)
		if err := sess.InjectContext(n); err != nil {
			log.Fatalf("InjectContext: %v", err)
		}
	}

	// 唯一的接收 goroutine（Receive 单读者规则），事件经 channel 分发给
	// 各轮消费。
	events := make(chan teacherItem, 64)
	go func() {
		defer close(events)
		for {
			ev, rerr := sess.Receive()
			events <- teacherItem{ev, rerr}
			if rerr != nil {
				return
			}
		}
	}()

	turns := []struct {
		photo []byte // 非 nil：提问前先发这张照片（走生产的 SendImage 路径）
		q     string
	}{
		// 验证上下文：老师应介绍 cat，且绝不提及 [CONTEXT] 便签的存在
		{nil, "Teacher, can you introduce this word to me?"},
		// 验证听写玩法的进入与词组顺序
		{nil, "我们来听写吧"},
		// 验证拍照给老师看：老师应说出照片内容（黄色圆形、蓝色背景）
		{photoJPEG(), "Teacher, look at my photo! What do you see?"},
	}
	var resumeHandle string
	for i, turn := range turns {
		if turn.photo != nil {
			log.Printf("发送照片（蓝底黄圆 JPEG，%d 字节）", len(turn.photo))
			if err := sess.SendImage(turn.photo, "image/jpeg"); err != nil {
				log.Fatalf("SendImage: %v", err)
			}
		}
		q := turn.q
		log.Printf("--- 学生（文本轮 %d）: %s", i+1, q)
		if err := sess.SendText(q); err != nil {
			log.Fatalf("SendText: %v", err)
		}
		pcm, transcript, handle, err := collectTurn(ctx, events)
		if handle != "" {
			resumeHandle = handle
		}
		if err != nil {
			log.Fatalf("收轮次 %d: %v", i+1, err)
		}
		path := filepath.Join(out, filenameForTurn(i+1))
		if err := os.WriteFile(path, wav.Encode(pcm, llm.LiveSampleRate), 0o644); err != nil {
			log.Fatal(err)
		}
		log.Printf("老师转写: %s", transcript)
		log.Printf("回复音频 %s → %s", wav.Duration(len(pcm), llm.LiveSampleRate).Round(time.Millisecond), path)
		if strings.Contains(strings.ToUpper(transcript), "[CONTEXT]") || strings.Contains(transcript, "CONTEXT") {
			log.Printf("⚠️  转写疑似泄露 [CONTEXT] 便签，需收紧提示词！")
		}
	}
	if resumeHandle != "" {
		log.Printf("已收到 SessionResumption handle（%d 字符），续期链路可用", len(resumeHandle))
	} else {
		log.Printf("⚠️  未收到 SessionResumption handle")
	}
	log.Printf("完成。人耳试听 %s 下的 teacher-*.wav：确认①介绍的是 cat；②听写按 cat, dog, fish 顺序；③没有朗读便签内容；④老师说出了照片内容（黄色圆形/蓝色背景）。", out)
}

func filenameForTurn(n int) string {
	return "teacher-turn-" + string(rune('0'+n)) + ".wav"
}

// photoJPEG 生成一张特征明确的测试照片（蓝底居中黄圆），老师能否说出
// 内容即验证图片入上下文的链路。
func photoJPEG() []byte {
	const w, h, r = 480, 360, 100
	bg := color.RGBA{60, 130, 250, 255}
	fg := color.RGBA{255, 210, 40, 255}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := x-w/2, y-h/2
			if dx*dx+dy*dy <= r*r {
				img.SetRGBA(x, y, fg)
			} else {
				img.SetRGBA(x, y, bg)
			}
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		log.Fatal(err)
	}
	return buf.Bytes()
}

type teacherItem struct {
	ev  *llm.TeacherEvent
	err error
}

// collectTurn 从共享事件流收齐一轮回复：攒 PCM 与输出转写直到 TurnComplete。
func collectTurn(ctx context.Context, events <-chan teacherItem) (pcm []byte, transcript, resumeHandle string, err error) {
	deadline := time.After(90 * time.Second)
	var sb strings.Builder
	for {
		select {
		case it, ok := <-events:
			if !ok {
				return pcm, sb.String(), resumeHandle, context.Canceled
			}
			if it.err != nil {
				return pcm, sb.String(), resumeHandle, it.err
			}
			ev := it.ev
			pcm = append(pcm, ev.Audio...)
			sb.WriteString(ev.OutputTranscript)
			if ev.ResumeHandle != "" {
				resumeHandle = ev.ResumeHandle
			}
			if ev.TurnComplete {
				return pcm, sb.String(), resumeHandle, nil
			}
		case <-deadline:
			return pcm, sb.String(), resumeHandle, context.DeadlineExceeded
		case <-ctx.Done():
			return pcm, sb.String(), resumeHandle, ctx.Err()
		}
	}
}
