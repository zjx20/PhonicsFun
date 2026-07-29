// Package wav wraps raw PCM audio in a WAV container without external deps.
package wav

import (
	"encoding/binary"
	"errors"
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

// Decode 解出 Encode 产物里的 PCM 与采样率。只支持本包写出的固定布局
// （44 字节头、16-bit 单声道 PCM、fmt 紧跟 data）——解码对象是自己落盘的
// word.wav（blend 收尾段复用其整词音频），不是通用 WAV 解析器。
func Decode(data []byte) (pcm []byte, sampleRate int, err error) {
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" ||
		string(data[12:16]) != "fmt " || string(data[36:40]) != "data" {
		return nil, 0, errors.New("不是本应用写出的 WAV 布局")
	}
	if binary.LittleEndian.Uint16(data[20:22]) != 1 ||
		binary.LittleEndian.Uint16(data[22:24]) != NumChannels ||
		binary.LittleEndian.Uint16(data[34:36]) != BitsPerSample {
		return nil, 0, errors.New("WAV 编码参数不是 16-bit 单声道 PCM")
	}
	rate := int(binary.LittleEndian.Uint32(data[24:28]))
	n := int(binary.LittleEndian.Uint32(data[40:44]))
	if n > len(data)-44 {
		n = len(data) - 44
	}
	return data[44 : 44+n], rate, nil
}

// Duration returns the playback length of raw PCM of the given byte length.
func Duration(pcmLen, sampleRate int) time.Duration {
	bytesPerSec := sampleRate * NumChannels * BitsPerSample / 8
	if bytesPerSec == 0 {
		return 0
	}
	return time.Duration(pcmLen) * time.Second / time.Duration(bytesPerSec)
}
