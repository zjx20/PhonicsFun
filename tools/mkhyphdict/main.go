// mkhyphdict 把 Moby Hyphenator II 的原始 mhyph.txt 清洗成可嵌入二进制的
// 音节参照表（internal/llm/mhyph.tsv.gz）。一次性工具，产物入库，本程序
// 留作可复现记录。
//
// 原始数据获取（public domain，Project Gutenberg #3204）：
//
//	curl -o mhyph.txt https://www.gutenberg.org/files/3204/files/mhyph.txt
//
// 原始格式：MacRoman 编码、每行一个词条、音节分隔符是 0xA5（MacRoman 的 •）、
// 行尾 CRLF；含带空格的词组、大小写重复条目和少量数据损坏行。
//
// 输出格式：每行一个切分形 "hy-phen-at-ed"（词本身 = 去掉 '-'，单列省一半
// 体积；排序按去 '-' 后的词序，查询侧用跳过 '-' 的比较器二分）。无断点的
// 单音节词也保留——"这个词只有一个音节"本身就是有价值的参照，防止 LLM
// 过度拆分 straight/through 这类长单音节词。
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
)

func main() {
	in := flag.String("in", "mhyph.txt", "Moby mhyph.txt 原始文件路径")
	out := flag.String("out", "mhyph.tsv.gz", "输出 gzip TSV 路径")
	flag.Parse()

	raw, err := os.ReadFile(*in)
	if err != nil {
		log.Fatal(err)
	}

	const sep = 0xA5 // MacRoman '•'
	entries := map[string]string{}
	total, kept := 0, 0
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		if len(line) == 0 {
			continue
		}
		total++
		var word, hyph []byte
		bad := false
		for _, b := range line {
			switch {
			case b == sep:
				hyph = append(hyph, '-')
			case b >= 'A' && b <= 'Z':
				word = append(word, b+('a'-'A'))
				hyph = append(hyph, b+('a'-'A'))
			case b >= 'a' && b <= 'z':
				word = append(word, b)
				hyph = append(hyph, b)
			default:
				// 空格（词组）、连字符复合词、MacRoman 高位字节（重音字符）
				// 以及其他符号一律弃行
				bad = true
			}
			if bad {
				break
			}
		}
		// 数据损坏行的拦截（如 mul·ti·lomultilocular 这类拼接错误由长度
		// 关系兜不住，但空词/首尾断点/连续断点这类结构性损坏在此拦截）
		w, h := string(word), string(hyph)
		if bad || len(w) < 2 ||
			strings.HasPrefix(h, "-") || strings.HasSuffix(h, "-") || strings.Contains(h, "--") {
			continue
		}
		if len(h)-strings.Count(h, "-") != len(w) {
			continue
		}
		// 大小写重复条目折叠后保留首次出现的切分
		if _, ok := entries[w]; !ok {
			entries[w] = h
			kept++
		}
	}

	// 按去 '-' 后的词排序（查询侧的二分比较器同样跳过 '-'）
	words := make([]string, 0, len(entries))
	for w := range entries {
		words = append(words, w)
	}
	sort.Strings(words)

	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	zw, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		log.Fatal(err)
	}
	bw := bufio.NewWriter(zw)
	for _, w := range words {
		fmt.Fprintln(bw, entries[w])
	}
	if err := bw.Flush(); err != nil {
		log.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		log.Fatal(err)
	}
	st, _ := os.Stat(*out)
	log.Printf("mkhyphdict: 原始 %d 行 → 保留 %d 词，输出 %s（%d 字节）", total, kept, *out, st.Size())
}
