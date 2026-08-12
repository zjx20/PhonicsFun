package httpapi

// AI 老师 WebSocket 桥接：浏览器 ↔ 本服务 ↔ Gemini Live。
//
// 端点 GET /api/teacher/live，帧语义：
//   - 二进制帧：裸 PCM16 LE mono。浏览器→服务 16kHz（麦克风），服务→浏览器
//     24kHz（老师语音，上游 chunk 原样转发，零转码）。
//   - 文本帧：JSON，均含 type。
//     浏览器→服务：context（当前单词组）、card（当前单词卡）、mic（on=false
//     暂停采集，恢复直接续发音频帧）、photo（学生拍照给老师看，data =
//     base64 JPEG，解码后 ≤ teacherMaxPhotoBytes，超限回非致命 error 帧）。
//     服务→浏览器：ready、transcript、interrupted、turn_complete、restarted、
//     error（fatal=true 后随即关连接）。
//
// 全局单路：新连接接管（takeover）时踢掉旧连接——既让刷新页面后旧 TCP
// 未死也不卡住新连接，也把 Live 并发预算固定为 pipeline 1 路 + 老师 1 路
//（免费层约 3 路，见 AGENTS.md 不变量 8）。
//
// 每连接三个 goroutine，读写职责固定（gorilla 一读一写规则）：
//   - browserReader（handler 本体）：唯一读浏览器 WS 的一方；
//   - runUpstream：唯一调用 sess.Receive 的一方，拥有上游会话生命周期
//     （建连/轮换/重连）；
//   - browserWriter：唯一写浏览器 WS 数据帧的一方（关闭帧除外，
//     WriteControl 并发安全）。
// 上游发送方有两个（browserReader 转音频/注入、runUpstream 重连重注入），
// 由 llm.TeacherSession 内部的锁串行化。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"phonicsfun/internal/llm"
)

const (
	// teacherReadWait：浏览器侧读超时。采集中音频帧连续到达；静音/暂停时
	// 靠浏览器对 ping 的自动 pong 续期（pingPeriod 必须小于它）。
	teacherReadWait   = 60 * time.Second
	teacherPingPeriod = 30 * time.Second
	teacherWriteWait  = 10 * time.Second
	// teacherOutBuf：下行帧缓冲。写满说明客户端消费过慢，音频帧直接丢
	//（JSON 事件帧不丢），防止慢客户端把内存拖爆。
	teacherOutBuf = 256
	// teacherDialRetries：上游连续建连失败（或连上即死）多少次后放弃。
	teacherDialRetries = 3
	// teacherMaxPhotoBytes：photo 消息解码后的大小上限。前端压到长边
	// ≤1024px 的 JPEG（约 100-300KB），上限留足余量、同时兜住异常客户端。
	teacherMaxPhotoBytes = 2 << 20
	// teacherReadLimit：浏览器单帧上限。音频帧只有几 KB，尺寸大头是
	// photo 文本帧：base64 膨胀 4/3 + JSON 包装，2MB 上限取 4MB 足够。
	teacherReadLimit = 4 << 20
)

var teacherUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// CSWSH 防护：本应用无认证、部署在内网，浏览器跨站页面能发起 WS。
	// 无 Origin（同源某些场景/非浏览器客户端）放行；有 Origin 时其 Host
	// 必须与请求 Host 一致（vite dev 代理会改写 Host，天然通过）。
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return strings.EqualFold(u.Host, r.Host)
	},
}

// teacherConn 抽象上游会话，测试注入 fake（对应 *llm.TeacherSession）。
type teacherConn interface {
	SendAudio(pcm []byte) error
	SendAudioStreamEnd() error
	InjectContext(text string) error
	SendImage(data []byte, mime string) error
	Receive() (*llm.TeacherEvent, error)
	NeedsRotation() bool
	Close() error
}

// teacherDialer 建立上游会话；生产实现包 llm.Client.ConnectTeacher。
type teacherDialer func(ctx context.Context, cfg llm.TeacherConfig, resumeHandle string) (teacherConn, error)

func (s *Server) handleTeacherLive(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.ReadSettings()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ws, err := teacherUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade 已写响应
	}
	b := &teacherBridge{
		ws:   ws,
		dial: s.teacherDial,
		cfg: llm.TeacherConfig{
			Instruction:          llm.BuildTeacherInstruction(settings.TeacherPrompt),
			Voice:                settings.TeacherVoice,
			VADPrefixPaddingMs:   settings.TeacherVADPrefixMs,
			VADSilenceDurationMs: settings.TeacherVADSilenceMs,
		},
		out:    make(chan wsFrame, teacherOutBuf),
		closed: make(chan struct{}),
	}
	s.takeoverTeacher(b)
	b.run(r.Context())
	s.releaseTeacher(b)
}

// takeoverTeacher 把 b 设为当前唯一活动桥接，踢掉旧的。
func (s *Server) takeoverTeacher(b *teacherBridge) {
	s.teacherMu.Lock()
	old := s.teacher
	s.teacher = b
	s.teacherMu.Unlock()
	if old != nil {
		old.shutdown()
	}
}

func (s *Server) releaseTeacher(b *teacherBridge) {
	s.teacherMu.Lock()
	if s.teacher == b {
		s.teacher = nil
	}
	s.teacherMu.Unlock()
}

// CloseTeacher 主动关闭当前老师桥接。http.Server.Shutdown 不管 hijack 的
// 连接，进程退出前经 RegisterOnShutdown 调它，否则 5 秒后带活连接硬退。
func (s *Server) CloseTeacher() {
	s.teacherMu.Lock()
	b := s.teacher
	s.teacherMu.Unlock()
	if b != nil {
		b.shutdown()
	}
}

// wsFrame 是发往浏览器的一帧：binary 与 text 二选一。
type wsFrame struct {
	binary []byte
	text   []byte
}

type teacherBridge struct {
	ws     *websocket.Conn
	dial   teacherDialer
	cfg    llm.TeacherConfig // settings 快照，会话存续期间不变
	out    chan wsFrame
	closed chan struct{}
	once   sync.Once
	cancel context.CancelFunc

	mu           sync.Mutex
	sess         teacherConn // nil = 上游未就绪/轮换中（此时音频帧直接丢）
	speaking     bool        // 模型正在下发语音：注入排队到轮次边界
	pendingGroup string      // speaking 期间到达的便签，同类只留最新
	pendingCard  string
	pendingPhoto []byte // speaking 期间到达的照片，只留最新
	lastGroup    string // 最近一次便签/照片，轮换重连后重放
	lastCard     string
	lastPhoto    []byte
	resumeHandle string
}

// run 阻塞运行桥接直到任一侧断开（作为 browserReader）。
func (b *teacherBridge) run(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	b.cancel = cancel
	defer b.shutdown()
	go b.runUpstream(ctx)
	go b.browserWriter()

	b.ws.SetReadLimit(teacherReadLimit)
	b.ws.SetReadDeadline(time.Now().Add(teacherReadWait))
	b.ws.SetPongHandler(func(string) error {
		return b.ws.SetReadDeadline(time.Now().Add(teacherReadWait))
	})
	for {
		mt, data, err := b.ws.ReadMessage()
		if err != nil {
			return
		}
		b.ws.SetReadDeadline(time.Now().Add(teacherReadWait))
		switch mt {
		case websocket.BinaryMessage:
			b.forwardAudio(data)
		case websocket.TextMessage:
			b.handleClientMsg(data)
		}
	}
}

// shutdown 幂等关停：唤醒三个 goroutine 各自退出。
func (b *teacherBridge) shutdown() {
	b.once.Do(func() {
		close(b.closed)
		if b.cancel != nil {
			b.cancel()
		}
		// WriteControl 并发安全，尽力通知浏览器正常关闭
		b.ws.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second))
		b.ws.Close()
		b.mu.Lock()
		sess := b.sess
		b.sess = nil
		b.mu.Unlock()
		if sess != nil {
			sess.Close() // 解除 runUpstream 的 Receive 阻塞
		}
	})
}

// --- browserReader 侧 ---

func (b *teacherBridge) forwardAudio(pcm []byte) {
	b.mu.Lock()
	sess := b.sess
	b.mu.Unlock()
	if sess == nil {
		return // 轮换/重连中：麦克风帧不值得缓存，直接丢
	}
	// 发送失败不在这里处理：runUpstream 的 Receive 会先感知会话死亡并轮换
	if err := sess.SendAudio(pcm); err != nil {
		log.Printf("[teacher] 上行音频失败: %v", err)
	}
}

type teacherClientMsg struct {
	Type  string `json:"type"`
	Group *struct {
		Name  string   `json:"name"`
		Words []string `json:"words"`
	} `json:"group"`
	Word string `json:"word"`
	IPA  string `json:"ipa"`
	ZH   string `json:"zh"`
	On   bool   `json:"on"`
	Data string `json:"data"` // photo：base64 JPEG
}

func (b *teacherBridge) handleClientMsg(data []byte) {
	var m teacherClientMsg
	if err := json.Unmarshal(data, &m); err != nil {
		return
	}
	switch m.Type {
	case "context":
		if m.Group == nil {
			return
		}
		b.noteAndInject(true, llm.ContextNoteGroup(m.Group.Name, m.Group.Words))
	case "card":
		if m.Word == "" {
			return
		}
		b.noteAndInject(false, llm.ContextNoteCard(m.Word, m.IPA, m.ZH))
	case "photo":
		photo, err := base64.StdEncoding.DecodeString(m.Data)
		if err != nil || len(photo) == 0 {
			b.sendJSON(map[string]any{"type": "error", "message": "照片数据无效，请重新拍一张"})
			return
		}
		if len(photo) > teacherMaxPhotoBytes {
			b.sendJSON(map[string]any{"type": "error", "message": "照片太大，请重新拍一张"})
			return
		}
		b.photoAndSend(photo)
	case "mic":
		if m.On {
			return // 恢复采集无需通知，浏览器直接续发音频帧
		}
		b.mu.Lock()
		sess := b.sess
		b.mu.Unlock()
		if sess != nil {
			if err := sess.SendAudioStreamEnd(); err != nil {
				log.Printf("[teacher] audioStreamEnd 失败: %v", err)
			}
		}
	}
}

// noteAndInject 记录最新便签并注入。模型说话中或上游未就绪时排队（同类
// 只留最新），在轮次边界（TurnComplete/Interrupted）flush——clientContent
// 插进正在生成的轮次中间是 SDK 明确不建议的用法。
func (b *teacherBridge) noteAndInject(isGroup bool, note string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if isGroup {
		b.lastGroup = note
	} else {
		b.lastCard = note
	}
	if b.sess == nil || b.speaking {
		if isGroup {
			b.pendingGroup = note
		} else {
			b.pendingCard = note
		}
		return
	}
	b.injectLocked(b.sess, note)
}

// injectLocked 在持有 b.mu 时注入便签（TeacherSession 自身的锁只串行化
// 单次发送，注入的先后顺序由 b.mu 保证）。
func (b *teacherBridge) injectLocked(sess teacherConn, note string) {
	if err := sess.InjectContext(note); err != nil {
		log.Printf("[teacher] 注入上下文失败: %v", err)
	}
}

// photoAndSend 记录最新照片并发给上游，排队规则与 noteAndInject 一致
//（说话中/未就绪时排队到轮次边界，只留最新）。
func (b *teacherBridge) photoAndSend(photo []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastPhoto = photo
	if b.sess == nil || b.speaking {
		b.pendingPhoto = photo
		return
	}
	b.sendImageLocked(b.sess, photo)
}

// sendImageLocked 在持有 b.mu 时发照片（协议固定 JPEG，前端压缩产物）。
func (b *teacherBridge) sendImageLocked(sess teacherConn, photo []byte) {
	if err := sess.SendImage(photo, "image/jpeg"); err != nil {
		log.Printf("[teacher] 发送照片失败: %v", err)
	}
}

// --- runUpstream 侧 ---

func (b *teacherBridge) runUpstream(ctx context.Context) {
	failures := 0
	first := true
	for {
		select {
		case <-b.closed:
			return
		default:
		}
		b.mu.Lock()
		handle := b.resumeHandle
		b.mu.Unlock()
		sess, err := b.dial(ctx, b.cfg, handle)
		if err != nil && handle != "" {
			// handle 失效（过期/服务端拒绝）：清掉，退回全新会话 + 重注入
			log.Printf("[teacher] 带 handle 重连失败，改用全新会话: %v", err)
			b.mu.Lock()
			b.resumeHandle = ""
			b.mu.Unlock()
			sess, err = b.dial(ctx, b.cfg, "")
		}
		if err != nil {
			failures++
			if failures >= teacherDialRetries || !llm.IsRetryable(err) {
				b.fatal("AI 老师连接失败：" + err.Error())
				return
			}
			if !b.sleep(time.Duration(1<<failures) * time.Second / 2) {
				return
			}
			continue
		}

		b.mu.Lock()
		b.sess = sess
		b.speaking = false
		b.mu.Unlock()
		if first {
			first = false
			// ready 之前就到达的便签（前端连接竞速）此刻补注入
			b.turnBoundary(sess)
			b.sendJSON(map[string]any{"type": "ready"})
		} else {
			// 无论是否带 handle 恢复成功都重放便签与最近照片：幂等，且防
			// handle 静默失效导致老师"失忆"当前组/卡/照片（照片重放多花
			// 一点图像 token，每 13 分钟一次可以接受）
			b.mu.Lock()
			group, card, photo := b.lastGroup, b.lastCard, b.lastPhoto
			b.injectLocked(sess, llm.ContextNoteRefreshed)
			if group != "" {
				b.injectLocked(sess, group)
			}
			if card != "" {
				b.injectLocked(sess, card)
			}
			if photo != nil {
				b.sendImageLocked(sess, photo)
			}
			b.pendingGroup, b.pendingCard, b.pendingPhoto = "", "", nil
			b.mu.Unlock()
			b.sendJSON(map[string]any{"type": "restarted"})
		}

		gotEvent := b.receiveLoop(sess)
		b.mu.Lock()
		b.sess = nil
		b.mu.Unlock()
		sess.Close()
		select {
		case <-b.closed:
			return
		default:
		}
		if gotEvent {
			failures = 0
		} else {
			// 连上即死（一条事件都没收到）视同建连失败，防无限重连
			failures++
			if failures >= teacherDialRetries {
				b.fatal("AI 老师会话反复中断，请稍后重试")
				return
			}
			if !b.sleep(time.Duration(1<<failures) * time.Second / 2) {
				return
			}
		}
	}
}

// receiveLoop 消费一条上游会话直到出错或需要轮换；返回是否收到过事件。
func (b *teacherBridge) receiveLoop(sess teacherConn) (gotEvent bool) {
	for {
		ev, err := sess.Receive()
		if err != nil {
			return gotEvent
		}
		gotEvent = true
		if len(ev.Audio) > 0 {
			b.mu.Lock()
			b.speaking = true
			b.mu.Unlock()
			b.enqueue(wsFrame{binary: ev.Audio})
		}
		if ev.InputTranscript != "" {
			b.sendJSON(map[string]any{"type": "transcript", "role": "user", "text": ev.InputTranscript})
		}
		if ev.OutputTranscript != "" {
			b.sendJSON(map[string]any{"type": "transcript", "role": "teacher", "text": ev.OutputTranscript})
		}
		if ev.ResumeHandle != "" {
			b.mu.Lock()
			b.resumeHandle = ev.ResumeHandle
			b.mu.Unlock()
		}
		if ev.Interrupted {
			b.turnBoundary(sess)
			b.sendJSON(map[string]any{"type": "interrupted"})
		}
		if ev.TurnComplete {
			b.turnBoundary(sess)
			b.sendJSON(map[string]any{"type": "turn_complete"})
		}
		// 轮换只在轮次边界做，别把老师的话拦腰截断
		if (ev.TurnComplete || ev.Interrupted || ev.GoAway) && sess.NeedsRotation() {
			return gotEvent
		}
	}
}

// turnBoundary 在轮次边界清 speaking 并 flush 排队的便签与照片。
func (b *teacherBridge) turnBoundary(sess teacherConn) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.speaking = false
	if b.pendingGroup != "" {
		b.injectLocked(sess, b.pendingGroup)
		b.pendingGroup = ""
	}
	if b.pendingCard != "" {
		b.injectLocked(sess, b.pendingCard)
		b.pendingCard = ""
	}
	if b.pendingPhoto != nil {
		b.sendImageLocked(sess, b.pendingPhoto)
		b.pendingPhoto = nil
	}
}

// --- browserWriter 侧 ---

func (b *teacherBridge) browserWriter() {
	ping := time.NewTicker(teacherPingPeriod)
	defer ping.Stop()
	for {
		select {
		case f := <-b.out:
			b.ws.SetWriteDeadline(time.Now().Add(teacherWriteWait))
			var err error
			if f.binary != nil {
				err = b.ws.WriteMessage(websocket.BinaryMessage, f.binary)
			} else {
				err = b.ws.WriteMessage(websocket.TextMessage, f.text)
			}
			if err != nil {
				b.shutdown()
				return
			}
		case <-ping.C:
			if err := b.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(teacherWriteWait)); err != nil {
				b.shutdown()
				return
			}
		case <-b.closed:
			return
		}
	}
}

// enqueue 投递下行帧。音频帧在缓冲满时丢弃（慢客户端听感缺一小段，但
// 内存有界）；JSON 事件帧必达（阻塞投递，closed 兜底解锁）。
func (b *teacherBridge) enqueue(f wsFrame) {
	if f.binary != nil {
		select {
		case b.out <- f:
		default:
		}
		return
	}
	select {
	case b.out <- f:
	case <-b.closed:
	}
}

func (b *teacherBridge) sendJSON(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	b.enqueue(wsFrame{text: data})
}

// fatal 通知浏览器不可恢复错误后关停。
func (b *teacherBridge) fatal(msg string) {
	b.sendJSON(map[string]any{"type": "error", "message": msg, "fatal": true})
	// 给 writer 一点时间把错误帧发出去
	time.Sleep(100 * time.Millisecond)
	b.shutdown()
}

// sleep 可被关停打断的退避；返回 false 表示桥接已关。
func (b *teacherBridge) sleep(d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-b.closed:
		return false
	}
}
