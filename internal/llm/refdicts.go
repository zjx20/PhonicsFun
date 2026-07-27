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

// 两份注入 prompt 的参照数据，与 CMUdict 相同的存放模式：gzip 嵌入、
// 启动解压、按行偏移表二分查找。LLM 是最终裁决者，参照缺失时按规则自判。
//
// mhyph.tsv.gz：Moby Hyphenator II 音节切分（public domain，见 MOBY-LICENSE），
// 每行一个切分形如 "rep-li-ca-tion"，词本身 = 去掉 '-'，按去 '-' 后的词序
// 排列（生成工具 tools/mkhyphdict）。
//
// posdict.tsv.gz：ECDICT 裁剪出的词性表（MIT，见 ECDICT-LICENSE），每行
// "word\tn.,v."，按词排序（生成工具 tools/mkposdict）。
var (
	//go:embed mhyph.tsv.gz
	mhyphGz []byte
	//go:embed posdict.tsv.gz
	posdictGz []byte
)

// lineDict 是"解压后按行二分"的通用底座。
type lineDict struct {
	data    []byte
	offsets []int32
}

func loadLineDict(gz []byte, name string) (*lineDict, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, fmt.Errorf("%s gzip: %w", name, err)
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("%s 解压: %w", name, err)
	}
	d := &lineDict{data: data}
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

func (d *lineDict) line(i int) []byte {
	start := int(d.offsets[i])
	end := len(d.data)
	if i+1 < len(d.offsets) {
		end = int(d.offsets[i+1]) - 1
	}
	return d.data[start:end]
}

// --- 音节切分参照 ---

type hyphdict struct{ d *lineDict }

func loadHyphDict() (*hyphdict, error) {
	d, err := loadLineDict(mhyphGz, "mhyph")
	return &hyphdict{d}, err
}

// compareStripped 比较一行切分形（跳过 '-'）与目标词的字典序。
func compareStripped(line, word []byte) int {
	j := 0
	for _, b := range line {
		if b == '-' {
			continue
		}
		if j >= len(word) {
			return 1 // line 更长
		}
		if b != word[j] {
			return int(b) - int(word[j])
		}
		j++
	}
	if j < len(word) {
		return -1
	}
	return 0
}

// lookup 精确查询，命中返回切分形（如 "rep-li-ca-tion"），未收录返回 ""。
func (h *hyphdict) lookup(word string) string {
	w := []byte(strings.ToLower(strings.TrimSpace(word)))
	if len(w) == 0 {
		return ""
	}
	i := sort.Search(len(h.d.offsets), func(i int) bool {
		return compareStripped(h.d.line(i), w) >= 0
	})
	if i >= len(h.d.offsets) {
		return ""
	}
	line := h.d.line(i)
	if compareStripped(line, w) != 0 {
		return ""
	}
	return string(line)
}

// hyphSuffixes 是直查 miss 时尝试剥离的常见后缀（长的优先）。
var hyphSuffixes = []string{"ness", "ing", "est", "ies", "ed", "er", "es", "ly", "s"}

// Ref 返回给 prompt 用的音节切分参照文本：直查命中返回切分形；剥后缀命中
// 返回 "词干切分 + -后缀"；全 miss 返回 ""。
func (h *hyphdict) Ref(word string) string {
	word = strings.ToLower(strings.TrimSpace(word))
	if hy := h.lookup(word); hy != "" {
		return hy
	}
	for _, suf := range hyphSuffixes {
		stem, ok := strings.CutSuffix(word, suf)
		if !ok || len(stem) < 2 {
			continue
		}
		// 词干候选：原样（jump-s）、补 e（bake-d）、去双写（run-ning）、ies→y（babies）
		cands := []string{stem, stem + "e"}
		if n := len(stem); n >= 2 && stem[n-1] == stem[n-2] {
			cands = append(cands, stem[:n-1])
		}
		if suf == "ies" {
			cands = append(cands, stem+"y")
		}
		for _, c := range cands {
			if hy := h.lookup(c); hy != "" {
				return fmt.Sprintf("%s + -%s", hy, suf)
			}
		}
	}
	return ""
}

// --- 词性参照 ---

type posdict struct{ d *lineDict }

func loadPOSDict() (*posdict, error) {
	d, err := loadLineDict(posdictGz, "posdict")
	return &posdict{d}, err
}

// Lookup 返回词性缩写串（如 "n.,v."），未收录返回 ""。
func (p *posdict) Lookup(word string) string {
	w := []byte(strings.ToLower(strings.TrimSpace(word)))
	if len(w) == 0 {
		return ""
	}
	key := func(line []byte) []byte {
		if t := bytes.IndexByte(line, '\t'); t >= 0 {
			return line[:t]
		}
		return line
	}
	i := sort.Search(len(p.d.offsets), func(i int) bool {
		return bytes.Compare(key(p.d.line(i)), w) >= 0
	})
	if i >= len(p.d.offsets) {
		return ""
	}
	line := p.d.line(i)
	if !bytes.Equal(key(line), w) {
		return ""
	}
	return string(line[len(w)+1:])
}
