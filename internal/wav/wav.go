// Package wav wraps raw PCM audio in a WAV container without external deps.
package wav

import (
	"encoding/binary"
	"time"
)

// Live API 输出固定为 16-bit 小端单声道 PCM；采样率由调用方传入（当前为 24kHz）。
const (
	BitsPerSample = 16
	NumChannels   = 1
)

// Encode wraps raw little-endian 16-bit mono PCM in a 44-byte-header WAV container.
func Encode(pcm []byte, sampleRate int) []byte {
	byteRate := sampleRate * NumChannels * BitsPerSample / 8
	blockAlign := NumChannels * BitsPerSample / 8

	buf := make([]byte, 44+len(pcm))
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+len(pcm)))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16) // fmt chunk size
	binary.LittleEndian.PutUint16(buf[20:22], 1)  // PCM
	binary.LittleEndian.PutUint16(buf[22:24], NumChannels)
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(buf[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(buf[34:36], BitsPerSample)
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(len(pcm)))
	copy(buf[44:], pcm)
	return buf
}

// Duration returns the playback length of raw PCM of the given byte length.
func Duration(pcmLen, sampleRate int) time.Duration {
	bytesPerSec := sampleRate * NumChannels * BitsPerSample / 8
	if bytesPerSec == 0 {
		return 0
	}
	return time.Duration(pcmLen) * time.Second / time.Duration(bytesPerSec)
}
