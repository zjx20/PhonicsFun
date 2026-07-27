package llm

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"google.golang.org/genai"
)

// Live 会话约束（调研于 2026-07：音频会话上限约 15 分钟，免费层约 3 并发）。
// 会话复用的收益是省 WebSocket 建立开销和会话配额，不是省 token。
const (
	// maxWordsPerSession 之后主动轮换，配合 maxSessionAge 保证远离 15 分钟上限。
	maxWordsPerSession = 8
	maxSessionAge      = 13 * time.Minute
	// turnTimeout 保护 Receive 循环：正常一轮 5-20s，超时说明会话已挂。
	turnTimeout = 90 * time.Second
)

// LiveSampleRate 是 Live API 音频输出的采样率（16-bit LE mono PCM）。
const LiveSampleRate = 24000

// LiveSession 是一条复用的 Live API 会话。非并发安全：由单一 audio worker
// 串行驱动。
type LiveSession struct {
	sess     *genai.Session
	openedAt time.Time
	words    int
	goAway   atomic.Bool
}

// ConnectLive 建立新的 Live 会话（计入请求限流）。
func (c *Client) ConnectLive(ctx context.Context) (*LiveSession, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	sess, err := c.g.Live.Connect(ctx, c.liveModel, &genai.LiveConnectConfig{
		ResponseModalities: []genai.Modality{genai.ModalityAudio},
		SpeechConfig: &genai.SpeechConfig{
			VoiceConfig: &genai.VoiceConfig{
				PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{VoiceName: c.voice},
			},
		},
		SystemInstruction: genai.NewContentFromText(liveSystemInstruction, genai.RoleUser),
	})
	if err != nil {
		return nil, fmt.Errorf("live connect: %w", err)
	}
	return &LiveSession{sess: sess, openedAt: time.Now()}, nil
}

// Speak 发送一段朗读脚本并收齐这一轮的全部 PCM（24kHz 16-bit LE mono）。
// 出错或超时后会话不可再用，调用方应 Close 并重连。
func (s *LiveSession) Speak(ctx context.Context, script string) ([]byte, error) {
	if err := s.sess.SendRealtimeInput(genai.LiveRealtimeInput{Text: script}); err != nil {
		return nil, fmt.Errorf("live send: %w", err)
	}

	type result struct {
		pcm []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		var pcm []byte
		for {
			msg, err := s.sess.Receive()
			if err != nil {
				done <- result{nil, fmt.Errorf("live receive: %w", err)}
				return
			}
			if msg.GoAway != nil {
				// 服务器预告即将断开：记下来，当前轮继续收完
				s.goAway.Store(true)
			}
			sc := msg.ServerContent
			if sc == nil {
				continue
			}
			if sc.ModelTurn != nil {
				for _, p := range sc.ModelTurn.Parts {
					if p.InlineData != nil {
						pcm = append(pcm, p.InlineData.Data...)
					}
				}
			}
			if sc.TurnComplete {
				done <- result{pcm, nil}
				return
			}
		}
	}()

	select {
	case r := <-done:
		return r.pcm, r.err
	case <-time.After(turnTimeout):
		s.sess.Close() // 解除 Receive 阻塞，收集 goroutine 随之退出
		return nil, errors.New("live 轮次超时")
	case <-ctx.Done():
		s.sess.Close()
		return nil, ctx.Err()
	}
}

// WordDone 在一个单词的两段音频都完成后调用，推进轮换计数。
func (s *LiveSession) WordDone() { s.words++ }

// NeedsRotation 报告是否应当关闭本会话、换新会话继续。
func (s *LiveSession) NeedsRotation() bool {
	return s.words >= maxWordsPerSession ||
		time.Since(s.openedAt) > maxSessionAge ||
		s.goAway.Load()
}

func (s *LiveSession) Close() error {
	return s.sess.Close()
}
