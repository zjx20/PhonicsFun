package llm

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"io"
	"sort"
	"strings"
)

// CMUdict（美音，ARPAbet）作为常见词发音的权威参照注入 prompt，约束小模型
// 的 IPA 转写与字素对齐。为省内存不建 map：解压后的字典按行排序（上游文件
// 本身有序），保留原始字节 + 行偏移表做二分查找，常驻约 4.5MB。
// 词典来源与许可见同目录 CMUDICT-LICENSE。
//
//go:embed cmudict.dict.gz
var cmudictGz []byte

type cmudict struct {
	data    []byte
	offsets []int32 // 每行行首在 data 中的偏移
}

func loadCMUDict() (*cmudict, error) {
	zr, err := gzip.NewReader(bytes.NewReader(cmudictGz))
	if err != nil {
		return nil, fmt.Errorf("cmudict gzip: %w", err)
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("cmudict 解压: %w", err)
	}
	d := &cmudict{data: data}
	for off := 0; off < len(data); {
		d.offsets = append(d.offsets, int32(off))
		nl := bytes.IndexByte(data[off:], '\n')
		if nl < 0 {
			break
		}
		off += nl + 1
	}
	return d, nil
}

// line 返回第 i 行（不含换行符）。
func (d *cmudict) line(i int) []byte {
	start := int(d.offsets[i])
	end := len(d.data)
	if i+1 < len(d.offsets) {
		end = int(d.offsets[i+1]) - 1
	}
	return d.data[start:end]
}

// key 返回一行的词条（首个空格前的部分）。
func lineKey(line []byte) []byte {
	if sp := bytes.IndexByte(line, ' '); sp >= 0 {
		return line[:sp]
	}
	return line
}

// SyllableCount 数 ARPAbet 音素串里带重音数字（0/1/2）的元音音素个数，
// 即该词读音的音节数；空串返回 0。注意口语吞音词（chocolate/every）的
// 拼写音节数可以合理地多于读音音节数，因此这个数只作 prompt 强参照，
// 不做 Go 侧硬校验。
func SyllableCount(arpabet string) int {
	n := 0
	for _, ph := range strings.Fields(arpabet) {
		last := ph[len(ph)-1]
		if last >= '0' && last <= '2' {
			n++
		}
	}
	return n
}

// Lookup 返回单词的 ARPAbet 音素串（如 "W ER1 D"），未收录返回 ""。
// 只取主发音；"word(2)" 这类变体行排在主条目之后，天然被跳过。
func (d *cmudict) Lookup(word string) string {
	w := []byte(strings.ToLower(strings.TrimSpace(word)))
	if len(w) == 0 {
		return ""
	}
	i := sort.Search(len(d.offsets), func(i int) bool {
		return bytes.Compare(lineKey(d.line(i)), w) >= 0
	})
	if i >= len(d.offsets) {
		return ""
	}
	line := d.line(i)
	if !bytes.Equal(lineKey(line), w) {
		return ""
	}
	phones := strings.TrimSpace(string(line[len(w):]))
	// 上游个别行带 "# 注释"，截掉
	if h := strings.Index(phones, "#"); h >= 0 {
		phones = strings.TrimSpace(phones[:h])
	}
	return phones
}
