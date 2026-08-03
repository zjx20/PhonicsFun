package wav

import (
	"fmt"
	"time"
)

// 静音分析都在 20ms 帧粒度上做；分段边界向两侧静音区各外扩 60ms，保护
// /s/ /f/ 这类低能量清辅音的起止不被削掉。
const (
	frameMS = 20
	padMS   = 60

	// 幅度低于全局峰值该比例的帧视为静音（TrimSilence 用固定值；
	// SplitBySilence 会在 splitThresholds 里扫描）。
	trimThresholdRatio = 0.03

	// 绝对幅度下限：整段音频峰值低于它就当作没有有效语音。
	minPeak = 1000
)

// SplitBySilence 把一段 16-bit LE 单声道 PCM 按静音间隙切成恰好 n 段。
// 段数已知是这里可靠性的关键：对（最小间隙 × 静音阈值）的组合从保守到
// 激进扫描，直到切出 n 段为止；扫不出来即报错（通常意味着朗读没有按
// 脚本逐项停顿，调用方应重试生成）。返回的分段是 pcm 的子切片。
func SplitBySilence(pcm []byte, sampleRate, n int) ([][]byte, error) {
	if n <= 0 {
		return nil, fmt.Errorf("分段数 %d 非法", n)
	}
	peaks, globalPeak := framePeaks(pcm, sampleRate)
	if globalPeak < minPeak {
		return nil, fmt.Errorf("音频峰值 %d 过低，没有有效语音", globalPeak)
	}

	gapsMS := []int{600, 500, 400, 300, 250}
	thresholds := []float64{0.02, 0.03, 0.05, 0.08}
	counts := map[int]bool{}
	for _, gapMS := range gapsMS {
		for _, ratio := range thresholds {
			thr := threshold(globalPeak, ratio)
			runs := voicedRuns(peaks, thr, gapMS/frameMS)
			counts[len(runs)] = true
			if len(runs) != n {
				continue
			}
			return runsToSegments(pcm, sampleRate, runs, len(peaks)), nil
		}
	}
	return nil, fmt.Errorf("静音分割失败：期望 %d 段，各参数组合切出 %v 段", n, keys(counts))
}

// TrimSilence 去掉 PCM 首尾的静音，两端各保留 padMS 的缓冲。
func TrimSilence(pcm []byte, sampleRate int) []byte {
	peaks, globalPeak := framePeaks(pcm, sampleRate)
	if globalPeak < minPeak {
		return pcm
	}
	thr := threshold(globalPeak, trimThresholdRatio)
	first, last := -1, -1
	for i, p := range peaks {
		if p >= thr {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return pcm
	}
	fb := frameBytes(sampleRate)
	pad := padMS / frameMS
	start := (first - pad) * fb
	if start < 0 {
		start = 0
	}
	end := (last + 1 + pad) * fb
	if end > len(pcm) {
		end = len(pcm)
	}
	return pcm[start:end]
}

// Silence 返回指定时长的静音 PCM。
func Silence(d time.Duration, sampleRate int) []byte {
	bytesPerSec := sampleRate * NumChannels * BitsPerSample / 8
	n := int(d * time.Duration(bytesPerSec) / time.Second)
	n -= n % 2 // 对齐到采样边界
	return make([]byte, n)
}

// --- 内部 ---

type frameRun struct{ start, end int } // 帧下标区间 [start, end)

func frameBytes(sampleRate int) int {
	return sampleRate * NumChannels * BitsPerSample / 8 * frameMS / 1000
}

// framePeaks 返回每 20ms 帧的峰值幅度和全局峰值。
func framePeaks(pcm []byte, sampleRate int) ([]int, int) {
	fb := frameBytes(sampleRate)
	var peaks []int
	global := 0
	for off := 0; off < len(pcm); off += fb {
		end := off + fb
		if end > len(pcm) {
			end = len(pcm)
		}
		peak := 0
		for i := off; i+1 < end; i += 2 {
			v := int(int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8))
			if v < 0 {
				v = -v
			}
			if v > peak {
				peak = v
			}
		}
		peaks = append(peaks, peak)
		if peak > global {
			global = peak
		}
	}
	return peaks, global
}

func threshold(globalPeak int, ratio float64) int {
	thr := int(float64(globalPeak) * ratio)
	if thr < minPeak/4 {
		thr = minPeak / 4
	}
	return thr
}

// voicedRuns 找出有声帧的连续区间：间隔小于 gapFrames 的相邻区间合并，
// 短于 60ms 的孤立区间（呼吸声、咔哒声）丢弃。
func voicedRuns(peaks []int, thr, gapFrames int) []frameRun {
	var raw []frameRun
	start := -1
	for i, p := range peaks {
		if p >= thr {
			if start < 0 {
				start = i
			}
		} else if start >= 0 {
			raw = append(raw, frameRun{start, i})
			start = -1
		}
	}
	if start >= 0 {
		raw = append(raw, frameRun{start, len(peaks)})
	}

	var merged []frameRun
	for _, r := range raw {
		if len(merged) > 0 && r.start-merged[len(merged)-1].end < gapFrames {
			merged[len(merged)-1].end = r.end
		} else {
			merged = append(merged, r)
		}
	}

	minRun := padMS / frameMS // 60ms
	var out []frameRun
	for _, r := range merged {
		if r.end-r.start >= minRun {
			out = append(out, r)
		}
	}
	return out
}

// runsToSegments 把帧区间换算成字节区间，边界向两侧静音各外扩 padMS
// （不超过相邻区间中点，保证分段互不重叠）。
func runsToSegments(pcm []byte, sampleRate int, runs []frameRun, totalFrames int) [][]byte {
	fb := frameBytes(sampleRate)
	pad := padMS / frameMS
	segs := make([][]byte, 0, len(runs))
	for i, r := range runs {
		lo := 0
		if i > 0 {
			lo = (r.start + runs[i-1].end) / 2
		}
		hi := totalFrames
		if i+1 < len(runs) {
			hi = (r.end + runs[i+1].start + 1) / 2
		}
		start := max(r.start-pad, lo)
		end := min(r.end+pad, hi)
		b := start * fb
		e := end * fb
		if e > len(pcm) {
			e = len(pcm)
		}
		segs = append(segs, pcm[b:e])
	}
	return segs
}

func keys(m map[int]bool) []int {
	var out []int
	for k := range m {
		out = append(out, k)
	}
	return out
}
