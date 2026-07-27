package wav

import (
	"encoding/binary"
	"testing"
	"time"
)

func TestEncodeHeader(t *testing.T) {
	pcm := make([]byte, 48000) // 1 second @ 24kHz 16-bit mono
	b := Encode(pcm, 24000)

	if len(b) != 44+len(pcm) {
		t.Fatalf("total size = %d, want %d", len(b), 44+len(pcm))
	}
	if string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" || string(b[36:40]) != "data" {
		t.Fatalf("bad magic: %q %q %q", b[0:4], b[8:12], b[36:40])
	}
	if got := binary.LittleEndian.Uint32(b[4:8]); got != uint32(36+len(pcm)) {
		t.Errorf("riff size = %d", got)
	}
	if got := binary.LittleEndian.Uint32(b[24:28]); got != 24000 {
		t.Errorf("sample rate = %d", got)
	}
	if got := binary.LittleEndian.Uint32(b[28:32]); got != 48000 {
		t.Errorf("byte rate = %d", got)
	}
	if got := binary.LittleEndian.Uint32(b[40:44]); got != uint32(len(pcm)) {
		t.Errorf("data size = %d", got)
	}
}

func TestDuration(t *testing.T) {
	if d := Duration(48000, 24000); d != time.Second {
		t.Errorf("Duration(48000, 24000) = %v, want 1s", d)
	}
	if d := Duration(0, 24000); d != 0 {
		t.Errorf("Duration(0) = %v, want 0", d)
	}
	if d := Duration(24000, 24000); d != 500*time.Millisecond {
		t.Errorf("Duration(24000, 24000) = %v, want 500ms", d)
	}
}
