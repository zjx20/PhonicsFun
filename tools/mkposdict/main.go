// mkposdict 从 ECDICT 的 ecdict.csv 裁剪出常用词的词性表
// （internal/llm/posdict.tsv.gz）。一次性工具，产物入库，本程序留作可复现
// 记录。
//
// 原始数据获取（MIT 许可，github.com/skywind3000/ECDICT）：
//
//	curl -o ecdict.csv https://raw.githubusercontent.com/skywind3000/ECDICT/master/ecdict.csv
//
// 重要：公开 CSV 的 pos 列 77 万行全部为空，词性必须从 translation 字段的
// 内联前缀解析（"n. 猫\nv. 抓" 这类，\n 是字面反斜杠 n）。裁剪范围取
// tag 含 zk/gk/cet4 的词条（中考+高考+四级 ≈ 5,400 词，覆盖儿童拼读词汇
// 绰绰有余，解析率实测 99%+）。
//
// 输出格式：按词排序的 TSV，每行 "word\tn.,v."（词性按释义出现顺序）。
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"
)

// posNorm 把 ECDICT 释义前缀里的词性缩写归一化到标准集合
// （与 store.ValidPOS 保持一致）。
var posNorm = map[string]string{
	"n": "n.", "v": "v.", "vi": "v.", "vt": "v.", "aux": "v.",
	"a": "adj.", "adj": "adj.",
	"ad": "adv.", "adv": "adv.",
	"pron": "pron.", "prep": "prep.", "conj": "conj.",
	"int": "int.", "interj": "int.",
	"num": "num.", "art": "art.", "det": "art.",
}

var posPrefix = regexp.MustCompile(`^([a-z]+)\.\s`)

func main() {
	in := flag.String("in", "ecdict.csv", "ECDICT ecdict.csv 路径")
	out := flag.String("out", "posdict.tsv.gz", "输出 gzip TSV 路径")
	tags := flag.String("tags", "zk,gk,cet4", "保留的 tag（逗号分隔，命中任一即保留）")
	flag.Parse()

	want := map[string]bool{}
	for _, t := range strings.Split(*tags, ",") {
		want[strings.TrimSpace(t)] = true
	}

	f, err := os.Open(*in)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	r := csv.NewReader(bufio.NewReaderSize(f, 1<<20))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	header, err := r.Read()
	if err != nil {
		log.Fatal(err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[h] = i
	}
	iWord, iTrans, iTag := col["word"], col["translation"], col["tag"]

	entries := map[string]string{}
	total, tagged, parsed := 0, 0, 0
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		total++
		if len(rec) <= iTag {
			continue
		}
		hit := false
		for _, t := range strings.Fields(rec[iTag]) {
			if want[t] {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		word := strings.ToLower(strings.TrimSpace(rec[iWord]))
		if word == "" || !regexp.MustCompile(`^[a-z']+$`).MatchString(word) {
			continue
		}
		tagged++
		var poses []string
		seen := map[string]bool{}
		// translation 里的换行是字面 "\n" 两个字符
		for _, part := range strings.Split(rec[iTrans], `\n`) {
			m := posPrefix.FindStringSubmatch(strings.TrimSpace(part))
			if m == nil {
				continue
			}
			if p, ok := posNorm[m[1]]; ok && !seen[p] {
				seen[p] = true
				poses = append(poses, p)
			}
		}
		if len(poses) == 0 {
			continue
		}
		parsed++
		if _, ok := entries[word]; !ok {
			entries[word] = strings.Join(poses, ",")
		}
	}

	words := make([]string, 0, len(entries))
	for w := range entries {
		words = append(words, w)
	}
	sort.Strings(words)

	of, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer of.Close()
	zw, err := gzip.NewWriterLevel(of, gzip.BestCompression)
	if err != nil {
		log.Fatal(err)
	}
	bw := bufio.NewWriter(zw)
	for _, w := range words {
		fmt.Fprintf(bw, "%s\t%s\n", w, entries[w])
	}
	if err := bw.Flush(); err != nil {
		log.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		log.Fatal(err)
	}
	st, _ := os.Stat(*out)
	log.Printf("mkposdict: 全库 %d 行 → tag 命中 %d → 词性可解析 %d，输出 %s（%d 字节）",
		total, tagged, parsed, *out, st.Size())
}
