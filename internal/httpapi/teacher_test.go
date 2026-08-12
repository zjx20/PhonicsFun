package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"

	"phonicsfun/internal/llm"
	"phonicsfun/internal/store"
)

// fakeTeacher 是可编排的上游会话：测试往 events 里推事件，Receive 依次
// 吐出；所有发送调用被记录。
type fakeTeacher struct {
	mu        sync.Mutex
	audio     [][]byte
	injected  []string
	images    [][]byte
	streamEnd int
	rotate    bool
	events    chan *llm.TeacherEvent
	done      chan struct{}
	closeOnce sync.Once
}

func newFakeTeacher() *fakeTeacher {
	return &fakeTeacher{events: make(chan *llm.TeacherEvent, 16), done: make(chan struct{})}
}

func (f *fakeTeacher) SendAudio(p []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]byte, len(p))
	copy(cp, p)
	f.audio = append(f.audio, cp)
	return nil
}

func (f *fakeTeacher) SendAudioStreamEnd() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.streamEnd++
	return nil
}

func (f *fakeTeacher) InjectContext(t string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.injected = append(f.injected, t)
	return nil
}

func (f *fakeTeacher) SendImage(data []byte, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	f.images = append(f.images, cp)
	return nil
}

func (f *fakeTeacher) snapshotImages() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.images...)
}

func (f *fakeTeacher) Receive() (*llm.TeacherEvent, error) {
	select {
	case ev := <-f.events:
		return ev, nil
	case <-f.done:
		return nil, errors.New("session closed")
	}
}

func (f *fakeTeacher) NeedsRotation() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rotate
}

func (f *fakeTeacher) setRotate() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rotate = true
}

func (f *fakeTeacher) Close() error {
	f.closeOnce.Do(func() { close(f.done) })
	return nil
}

func (f *fakeTeacher) snapshot() (audio [][]byte, injected []string, streamEnd int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.audio...), append([]string(nil), f.injected...), f.streamEnd
}

// scriptedDialer 依次派发 fake 会话并记录每次的 resumeHandle 与 cfg。
type scriptedDialer struct {
	mu      sync.Mutex
	conns   []*fakeTeacher
	handles []string
	cfgs    []llm.TeacherConfig
}

func (d *scriptedDialer) dial(_ context.Context, cfg llm.TeacherConfig, handle string) (teacherConn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handles = append(d.handles, handle)
	d.cfgs = append(d.cfgs, cfg)
	if len(d.conns) == 0 {
		return nil, errors.New("no more conns")
	}
	c := d.conns[0]
	d.conns = d.conns[1:]
	return c, nil
}

func newTeacherTestClient(t *testing.T, d *scriptedDialer) (*websocket.Conn, func()) {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return newTeacherTestClientWithStore(t, d, st)
}

func newTeacherTestClientWithStore(t *testing.T, d *scriptedDialer, st *store.Store) (*websocket.Conn, func()) {
	t.Helper()
	srv := New(st, nil, nil, fstest.MapFS{})
	srv.teacherDial = d.dial
	ts := httptest.NewServer(srv)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/teacher/live"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		ts.Close()
		t.Fatalf("dial: %v", err)
	}
	return ws, func() { ws.Close(); srv.CloseTeacher(); ts.Close() }
}

// 设置页保存的音色与 VAD 覆盖值必须原样流进上游拨号参数。
func TestTeacherSettingsFlowToDial(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteSettings(&store.Settings{
		TeacherVoice: "Puck", TeacherVADPrefixMs: 100, TeacherVADSilenceMs: 900,
	}); err != nil {
		t.Fatal(err)
	}
	d := &scriptedDialer{conns: []*fakeTeacher{newFakeTeacher()}}
	ws, cleanup := newTeacherTestClientWithStore(t, d, st)
	defer cleanup()
	readUntilJSON(t, ws, "ready")

	d.mu.Lock()
	cfg := d.cfgs[0]
	d.mu.Unlock()
	if cfg.Voice != "Puck" || cfg.VADPrefixPaddingMs != 100 || cfg.VADSilenceDurationMs != 900 {
		t.Errorf("settings 未流到拨号参数: %+v", cfg)
	}
}

// readUntilJSON 读帧直到出现指定 type 的 JSON 帧（跳过其余），超时报错。
func readUntilJSON(t *testing.T, ws *websocket.Conn, wantType string) map[string]any {
	t.Helper()
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("等 %q 时读帧失败: %v", wantType, err)
		}
		if mt != websocket.TextMessage {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("坏 JSON 帧: %s", data)
		}
		if m["type"] == wantType {
			return m
		}
	}
}

// readUntilBinary 读帧直到出现二进制帧。
func readUntilBinary(t *testing.T, ws *websocket.Conn) []byte {
	t.Helper()
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("等二进制帧失败: %v", err)
		}
		if mt == websocket.BinaryMessage {
			return data
		}
	}
}

// waitFor 轮询直到条件满足或超时。
func waitFor(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", desc)
}

func TestTeacherBridgeAudioBothWays(t *testing.T) {
	fake := newFakeTeacher()
	d := &scriptedDialer{conns: []*fakeTeacher{fake}}
	ws, cleanup := newTeacherTestClient(t, d)
	defer cleanup()

	readUntilJSON(t, ws, "ready")

	// 上行：浏览器音频帧 → fake
	up := []byte{1, 2, 3, 4}
	if err := ws.WriteMessage(websocket.BinaryMessage, up); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "上行音频到达", func() bool {
		audio, _, _ := fake.snapshot()
		return len(audio) == 1 && string(audio[0]) == string(up)
	})

	// 下行：fake 音频事件 → 浏览器二进制帧；转写 → JSON
	fake.events <- &llm.TeacherEvent{Audio: []byte{9, 8, 7}, OutputTranscript: "hello"}
	if got := readUntilBinary(t, ws); string(got) != string([]byte{9, 8, 7}) {
		t.Errorf("下行音频不一致: %v", got)
	}
	m := readUntilJSON(t, ws, "transcript")
	if m["role"] != "teacher" || m["text"] != "hello" {
		t.Errorf("转写帧不对: %v", m)
	}

	// mic off → AudioStreamEnd
	ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"mic","on":false}`))
	waitFor(t, "streamEnd 到达", func() bool {
		_, _, se := fake.snapshot()
		return se == 1
	})
}

func TestTeacherContextInjectionAndSpeakingQueue(t *testing.T) {
	fake := newFakeTeacher()
	d := &scriptedDialer{conns: []*fakeTeacher{fake}}
	ws, cleanup := newTeacherTestClient(t, d)
	defer cleanup()
	readUntilJSON(t, ws, "ready")

	// 空闲时注入：立即到达
	ws.WriteMessage(websocket.TextMessage,
		[]byte(`{"type":"context","group":{"name":"g1","words":["cat","dog"]}}`))
	waitFor(t, "组便签注入", func() bool {
		_, inj, _ := fake.snapshot()
		return len(inj) == 1 && strings.Contains(inj[0], `"g1"`) && strings.Contains(inj[0], "cat, dog")
	})

	// 模型说话中注入：排队
	fake.events <- &llm.TeacherEvent{Audio: []byte{1}}
	readUntilBinary(t, ws)
	ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"card","word":"cat","ipa":"/kæt/","zh":"猫"}`))
	time.Sleep(150 * time.Millisecond) // 给错误路径一个暴露窗口
	if _, inj, _ := fake.snapshot(); len(inj) != 1 {
		t.Fatalf("说话中不应注入, got %v", inj)
	}

	// 轮次结束：flush 排队便签
	fake.events <- &llm.TeacherEvent{TurnComplete: true}
	readUntilJSON(t, ws, "turn_complete")
	waitFor(t, "卡便签 flush", func() bool {
		_, inj, _ := fake.snapshot()
		return len(inj) == 2 && strings.Contains(inj[1], `"cat"`)
	})
}

// photo 帧：空闲时立即转发（解码后的 JPEG 字节）；模型说话中排队，轮次
// 边界 flush；超限/坏数据回非致命 error 帧且不转发。
func TestTeacherPhotoForwardQueueAndReject(t *testing.T) {
	fake := newFakeTeacher()
	d := &scriptedDialer{conns: []*fakeTeacher{fake}}
	ws, cleanup := newTeacherTestClient(t, d)
	defer cleanup()
	readUntilJSON(t, ws, "ready")

	photoMsg := func(raw []byte) []byte {
		msg, _ := json.Marshal(map[string]string{
			"type": "photo", "data": base64.StdEncoding.EncodeToString(raw),
		})
		return msg
	}

	// 空闲时：立即转发
	p1 := []byte{0xff, 0xd8, 1, 2, 3}
	ws.WriteMessage(websocket.TextMessage, photoMsg(p1))
	waitFor(t, "照片到达上游", func() bool {
		imgs := fake.snapshotImages()
		return len(imgs) == 1 && string(imgs[0]) == string(p1)
	})

	// 说话中：排队，轮次结束 flush；只留最新一张
	fake.events <- &llm.TeacherEvent{Audio: []byte{1}}
	readUntilBinary(t, ws)
	ws.WriteMessage(websocket.TextMessage, photoMsg([]byte{4, 4}))
	p2 := []byte{5, 5, 5}
	ws.WriteMessage(websocket.TextMessage, photoMsg(p2))
	time.Sleep(150 * time.Millisecond) // 给错误路径一个暴露窗口
	if imgs := fake.snapshotImages(); len(imgs) != 1 {
		t.Fatalf("说话中不应转发照片, got %d 张", len(imgs))
	}
	fake.events <- &llm.TeacherEvent{TurnComplete: true}
	readUntilJSON(t, ws, "turn_complete")
	waitFor(t, "排队照片 flush", func() bool {
		imgs := fake.snapshotImages()
		return len(imgs) == 2 && string(imgs[1]) == string(p2)
	})

	// 超限：回非致命 error 帧，不转发
	ws.WriteMessage(websocket.TextMessage, photoMsg(make([]byte, teacherMaxPhotoBytes+1)))
	m := readUntilJSON(t, ws, "error")
	if m["fatal"] == true {
		t.Errorf("超限照片不应是致命错误: %v", m)
	}
	// 坏 base64：同样回 error 帧
	ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"photo","data":"!!!"}`))
	readUntilJSON(t, ws, "error")
	if imgs := fake.snapshotImages(); len(imgs) != 2 {
		t.Errorf("被拒照片不应转发, got %d 张", len(imgs))
	}
}

func TestTeacherRotationReplaysContext(t *testing.T) {
	fake1, fake2 := newFakeTeacher(), newFakeTeacher()
	d := &scriptedDialer{conns: []*fakeTeacher{fake1, fake2}}
	ws, cleanup := newTeacherTestClient(t, d)
	defer cleanup()
	readUntilJSON(t, ws, "ready")

	ws.WriteMessage(websocket.TextMessage,
		[]byte(`{"type":"context","group":{"name":"g1","words":["cat"]}}`))
	waitFor(t, "首会话收到便签", func() bool {
		_, inj, _ := fake1.snapshot()
		return len(inj) == 1
	})
	photo := []byte{0xff, 0xd8, 9}
	msg, _ := json.Marshal(map[string]string{
		"type": "photo", "data": base64.StdEncoding.EncodeToString(photo),
	})
	ws.WriteMessage(websocket.TextMessage, msg)
	waitFor(t, "首会话收到照片", func() bool {
		return len(fake1.snapshotImages()) == 1
	})

	// handle 更新 + 轮换点
	fake1.events <- &llm.TeacherEvent{ResumeHandle: "h1"}
	fake1.setRotate()
	fake1.events <- &llm.TeacherEvent{TurnComplete: true}

	readUntilJSON(t, ws, "restarted")
	d.mu.Lock()
	handles := append([]string(nil), d.handles...)
	d.mu.Unlock()
	if len(handles) != 2 || handles[1] != "h1" {
		t.Errorf("重连应带 handle h1, got %v", handles)
	}
	_, inj, _ := fake2.snapshot()
	if len(inj) != 2 || !strings.Contains(inj[0], "refreshed") || !strings.Contains(inj[1], `"g1"`) {
		t.Errorf("新会话应先收衔接便签再重放组便签, got %v", inj)
	}
	if imgs := fake2.snapshotImages(); len(imgs) != 1 || string(imgs[0]) != string(photo) {
		t.Errorf("新会话应重放最近照片, got %d 张", len(imgs))
	}
}

func TestTeacherTakeover(t *testing.T) {
	fake1, fake2 := newFakeTeacher(), newFakeTeacher()
	d := &scriptedDialer{conns: []*fakeTeacher{fake1, fake2}}

	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(st, nil, nil, fstest.MapFS{})
	srv.teacherDial = d.dial
	ts := httptest.NewServer(srv)
	defer ts.Close()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/teacher/live"

	ws1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws1.Close()
	readUntilJSON(t, ws1, "ready")

	ws2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws2.Close()
	readUntilJSON(t, ws2, "ready")
	defer srv.CloseTeacher()

	// 旧连接应被踢：读到关闭错误
	ws1.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := ws1.ReadMessage(); err != nil {
			break
		}
	}
	// 新连接仍然可用
	up := []byte{5, 5}
	if err := ws2.WriteMessage(websocket.BinaryMessage, up); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "新连接音频可达", func() bool {
		audio, _, _ := fake2.snapshot()
		return len(audio) == 1
	})
}
