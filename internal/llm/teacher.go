package llm

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/genai"
)

// TeacherSampleRateIn 是 AI 老师会话上行（学生麦克风）音频采样率；
// 下行复用 LiveSampleRate（24000）。均为 16-bit LE mono PCM。
const TeacherSampleRateIn = 16000

// teacherMaxAge 与拼读会话的 maxSessionAge 同一余量逻辑：官方音频会话
// 上限约 15 分钟，提前主动轮换；对话历史靠 SessionResumption handle 续。
const teacherMaxAge = 13 * time.Minute

// 自动 VAD 端点检测的内置默认值（可被设置页覆盖，见 TeacherConfig）。
// 学生是小孩，跟读单词常常只有不到一秒的短促发音：
//   - prefixPadding 是"提交 start-of-speech 前需要的持续语音时长"，必须
//     远小于最短发音（一个单音节词 ≈ 300-500ms），否则短词整个被漏掉、
//     模型毫无反应；代价是环境噪音更容易被误判为开口（有浏览器端降噪兜底）。
//   - silenceDuration 是"提交 end-of-speech 前需要的静音时长"，决定孩子
//     停嘴到老师开始回应的最小延迟；太小会把小孩慢吞吞的整句话拦腰切轮。
// 跟读响应慢先调这两个值（或引导用户去设置页调），别去动提示词。
const (
	teacherVADPrefixPaddingMs   = 40
	teacherVADSilenceDurationMs = 600
)

// TeacherConfig 是建立 AI 老师会话的参数集（settings 快照 + 拼好的指令），
// 会话存续期间不变；resumeHandle 每次轮换重连都不同，故单独作参数传。
type TeacherConfig struct {
	// Instruction 是完整 systemInstruction（BuildTeacherInstruction 的产物）。
	Instruction string
	// Voice 为空时用配置的默认音色。
	Voice string
	// VADPrefixPaddingMs / VADSilenceDurationMs 覆盖端点检测参数（毫秒），
	// ≤0 = 用内置默认。语义与取舍见 teacherVAD* 常量注释。
	VADPrefixPaddingMs   int
	VADSilenceDurationMs int
}

// vad 展开端点检测配置，未覆盖的值回落内置默认。Start/End 灵敏度 HIGH
// 与 Live 默认一致，写死是为了不被默认值变更影响。
func (tc TeacherConfig) vad() *genai.AutomaticActivityDetection {
	prefix, silence := tc.VADPrefixPaddingMs, tc.VADSilenceDurationMs
	if prefix <= 0 {
		prefix = teacherVADPrefixPaddingMs
	}
	if silence <= 0 {
		silence = teacherVADSilenceDurationMs
	}
	return &genai.AutomaticActivityDetection{
		StartOfSpeechSensitivity: genai.StartSensitivityHigh,
		EndOfSpeechSensitivity:   genai.EndSensitivityHigh,
		PrefixPaddingMs:          genai.Ptr(int32(prefix)),
		SilenceDurationMs:        genai.Ptr(int32(silence)),
	}
}

// TeacherSession 是 AI 老师的双工对话会话。
//
// 并发契约：Receive 只允许单一 goroutine 调用（gorilla 单读者规则）；
// 所有发送方法由内部 mu 串行化，可从多个 goroutine 调用（桥接层的
// 浏览器读循环与重连后的上下文重注入是两个不同的发送方）。
type TeacherSession struct {
	sess     *genai.Session
	openedAt time.Time
	mu       sync.Mutex
	goAway   atomic.Bool
}

// ConnectTeacher 建立对话型 Live 会话（计入请求限流，与其他出站调用共享
// limiter）。resumeHandle 非空时尝试恢复上一会话的对话历史，handle 失效
// 会在 Connect 时报错，调用方应清空 handle 重连（全新会话 + 重注入上下文
// 兜底）。
func (c *Client) ConnectTeacher(ctx context.Context, tc TeacherConfig, resumeHandle string) (*TeacherSession, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	voice := tc.Voice
	if voice == "" {
		voice = c.voice
	}
	cfg := &genai.LiveConnectConfig{
		ResponseModalities: []genai.Modality{genai.ModalityAudio},
		SpeechConfig: &genai.SpeechConfig{
			VoiceConfig: &genai.VoiceConfig{
				PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{VoiceName: voice},
			},
		},
		SystemInstruction: genai.NewContentFromText(tc.Instruction, genai.RoleUser),
		// 双向字幕：家长要能看见孩子说了什么被听成什么（纠音场景刚需）。
		InputAudioTranscription:  &genai.AudioTranscriptionConfig{},
		OutputAudioTranscription: &genai.AudioTranscriptionConfig{},
		// 服务端自动 VAD + barge-in（Interrupted 事件），端点检测不下放给
		// 前端；灵敏度按"小孩短促跟读"显式调参（默认值与取舍见 vad()
		// 及 teacherVAD* 常量注释），家长可在设置页覆盖。
		RealtimeInputConfig: &genai.RealtimeInputConfig{
			AutomaticActivityDetection: tc.vad(),
		},
		// 滑窗压缩兜底 token 上限：系统指令不进滑窗，人格不会被压掉。
		ContextWindowCompression: &genai.ContextWindowCompressionConfig{
			SlidingWindow: &genai.SlidingWindow{},
		},
		// 非透明 resumption：只存服务器推的最新 handle、重连时回填。
		// 听写进行到第几个词只存在于对话历史里，丢历史 = 听写从头来。
		SessionResumption: &genai.SessionResumptionConfig{Handle: resumeHandle},
	}
	sess, err := c.g.Live.Connect(ctx, c.teacherModel, cfg)
	if err != nil {
		return nil, fmt.Errorf("teacher connect: %w", err)
	}
	return &TeacherSession{sess: sess, openedAt: time.Now()}, nil
}

// SendAudio 发送一段学生麦克风 PCM（16kHz 16-bit LE mono）。
func (s *TeacherSession) SendAudio(pcm []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sess.SendRealtimeInput(genai.LiveRealtimeInput{
		Audio: &genai.Blob{Data: pcm, MIMEType: fmt.Sprintf("audio/pcm;rate=%d", TeacherSampleRateIn)},
	})
}

// SendAudioStreamEnd 通知服务端麦克风流暂停（本地播放让位/用户静音）。
// 之后直接继续 SendAudio 即恢复。
func (s *TeacherSession) SendAudioStreamEnd() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sess.SendRealtimeInput(genai.LiveRealtimeInput{AudioStreamEnd: true})
}

// InjectContext 注入一条应用状态便签（当前单词组/卡片等）。
// TurnComplete=false：服务端只把它累积进 prompt、不触发生成，下一次学生
// 开口的语音轮自然携带该上下文。绝不能用 realtime Text 发——那会被当成
// 用户活动打断模型正在说的话。
func (s *TeacherSession) InjectContext(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sess.SendClientContent(genai.LiveClientContentInput{
		Turns:        []*genai.Content{genai.NewContentFromText(text, genai.RoleUser)},
		TurnComplete: genai.Ptr(false),
	})
}

// SendText 以完整用户轮发送一段文本并立即触发生成。正常对话走 SendAudio
// 由服务端 VAD 分轮；这条路径供无麦克风的验证（cmd/spike -teacher）使用。
func (s *TeacherSession) SendText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sess.SendClientContent(genai.LiveClientContentInput{
		Turns:        []*genai.Content{genai.NewContentFromText(text, genai.RoleUser)},
		TurnComplete: genai.Ptr(true),
	})
}

// --- 上下文便签 ---
// 便签格式与 teacherBaseInstruction 的 CONTEXT NOTES 协议是一对契约：
// 老师被要求对 [CONTEXT] 开头的消息静默记忆、绝不朗读。桥接层与 spike
// 共用这三个拼装函数，保证验证的就是生产格式。

// ContextNoteGroup 生成"学生切到了某个单词组"的便签。
func ContextNoteGroup(name string, words []string) string {
	return fmt.Sprintf("[CONTEXT] The student opened word group %q. Words in this group, in order: %s. This is background information — do NOT respond to it.",
		name, strings.Join(words, ", "))
}

// ContextNoteCard 生成"学生正看着某张单词卡"的便签；ipa/zh 可为空
// （卡片文本尚未生成时只有单词本身）。
func ContextNoteCard(word, ipa, zh string) string {
	extra := ""
	if ipa != "" {
		extra += " (IPA " + ipa
		if zh != "" {
			extra += ", Chinese: " + zh
		}
		extra += ")"
	} else if zh != "" {
		extra += " (Chinese: " + zh + ")"
	}
	return fmt.Sprintf("[CONTEXT] The student is now looking at the card for %q%s. Do NOT respond to it.", word, extra)
}

// ContextNoteRefreshed 是会话轮换重连后的衔接便签，随后应重发最近的
// group/card 便签。
const ContextNoteRefreshed = "[CONTEXT] The connection was refreshed. Continue the current activity naturally."

// TeacherEvent 是一条 LiveServerMessage 的桥接友好投影；一条消息可能同时
// 置多个字段（如最后一段音频与 TurnComplete 同帧）。
type TeacherEvent struct {
	Audio              []byte // 模型语音 PCM（24kHz 16-bit LE mono）
	InputTranscript    string // 学生语音的转写增量
	OutputTranscript   string // 老师语音的转写增量
	Interrupted        bool   // 学生打断：客户端应立即清空播放队列
	TurnComplete       bool
	GenerationComplete bool
	GoAway             bool   // 服务器预告断开（NeedsRotation 随之为真）
	ResumeHandle       string // 非空 = 新的可恢复状态 handle
}

// Receive 阻塞读取下一条服务器消息并映射为 TeacherEvent。
// 只允许单一 goroutine 调用；出错后会话不可再用。
func (s *TeacherSession) Receive() (*TeacherEvent, error) {
	msg, err := s.sess.Receive()
	if err != nil {
		return nil, err
	}
	ev := &TeacherEvent{}
	if msg.GoAway != nil {
		s.goAway.Store(true)
		ev.GoAway = true
	}
	if u := msg.SessionResumptionUpdate; u != nil && u.Resumable && u.NewHandle != "" {
		ev.ResumeHandle = u.NewHandle
	}
	if sc := msg.ServerContent; sc != nil {
		if sc.ModelTurn != nil {
			for _, p := range sc.ModelTurn.Parts {
				if p.InlineData != nil {
					ev.Audio = append(ev.Audio, p.InlineData.Data...)
				}
			}
		}
		if sc.InputTranscription != nil {
			ev.InputTranscript = sc.InputTranscription.Text
		}
		if sc.OutputTranscription != nil {
			ev.OutputTranscript = sc.OutputTranscription.Text
		}
		ev.Interrupted = sc.Interrupted
		ev.TurnComplete = sc.TurnComplete
		ev.GenerationComplete = sc.GenerationComplete
	}
	return ev, nil
}

// NeedsRotation 报告是否应当关闭本会话、带 resumption handle 换新会话。
func (s *TeacherSession) NeedsRotation() bool {
	return time.Since(s.openedAt) > teacherMaxAge || s.goAway.Load()
}

func (s *TeacherSession) Close() error {
	return s.sess.Close()
}
