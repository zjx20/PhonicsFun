package wav

import (
	"testing"
	"time"
)

const testRate = 24000

// tone 生成指定时长、幅度的方波 PCM（16-bit LE mono）。
func tone(d time.Duration, amp int16) []byte {
	n := int(d * testRate / time.Second)
	out := make([]byte, n*2)
	for i := 0; i < n; i++ {
		v := amp
		if (i/24)%2 == 0 { // 500Hz 方波
			v = -amp
		}
		out[i*2] = byte(uint16(v))
		out[i*2+1] = byte(uint16(v) >> 8)
	}
	return out
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestSilence(t *testing.T) {
	s := Silence(250*time.Millisecond, testRate)
	if got := Duration(len(s), testRate); got != 250*time.Millisecond {
		t.Errorf("Silence(250ms) 时长 = %s", got)
	}
	if len(s)%2 != 0 {
		t.Error("静音长度未对齐采样边界")
	}
}

func TestTrimSilence(t *testing.T) {
	pcm := concat(
		Silence(500*time.Millisecond, testRate),
		tone(300*time.Millisecond, 20000),
		Silence(800*time.Millisecond, testRate),
	)
	trimmed := TrimSilence(pcm, testRate)
	d := Duration(len(trimmed), testRate)
	// 300ms 语音 + 两侧各 ≤60ms 缓冲（帧粒度 20ms 有量化误差）
	if d < 300*time.Millisecond || d > 460*time.Millisecond {
		t.Errorf("TrimSilence 后时长 = %s，期望 300ms~460ms", d)
	}
	// 全静音输入原样返回
	quiet := Silence(200*time.Millisecond, testRate)
	if got := TrimSilence(quiet, testRate); len(got) != len(quiet) {
		t.Error("全静音输入应原样返回")
	}
}

func TestSplitBySilence(t *testing.T) {
	gap := Silence(700*time.Millisecond, testRate)
	// 三段语音，第二段模拟低能量清辅音（幅度只有峰值的 1/10）
	pcm := concat(
		Silence(300*time.Millisecond, testRate),
		tone(400*time.Millisecond, 20000), gap,
		tone(250*time.Millisecond, 2000), gap,
		tone(500*time.Millisecond, 18000),
		Silence(400*time.Millisecond, testRate),
	)
	segs, err := SplitBySilence(pcm, testRate, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 3 {
		t.Fatalf("分段数 = %d", len(segs))
	}
	wants := []time.Duration{400 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond}
	for i, seg := range segs {
		d := Duration(len(seg), testRate)
		if d < wants[i] || d > wants[i]+160*time.Millisecond {
			t.Errorf("段 %d 时长 = %s，期望 %s ± 外扩缓冲", i, d, wants[i])
		}
	}

	// 期望段数对不上必须报错（这是"模型没按脚本读"的检测手段）
	if _, err := SplitBySilence(pcm, testRate, 5); err == nil {
		t.Error("段数不符时应报错")
	}
	// 纯静音输入报错
	if _, err := SplitBySilence(Silence(time.Second, testRate), testRate, 1); err == nil {
		t.Error("无有效语音时应报错")
	}
}

func TestSplitBySilenceShortGaps(t *testing.T) {
	// 间隙只有 300ms：保守参数（600ms 间隙）切不出 4 段，扫描应自动降到
	// 更短的间隙参数命中
	gap := Silence(300*time.Millisecond, testRate)
	pcm := concat(
		tone(200*time.Millisecond, 15000), gap,
		tone(200*time.Millisecond, 15000), gap,
		tone(200*time.Millisecond, 15000), gap,
		tone(200*time.Millisecond, 15000),
	)
	segs, err := SplitBySilence(pcm, testRate, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 4 {
		t.Fatalf("分段数 = %d", len(segs))
	}
}
