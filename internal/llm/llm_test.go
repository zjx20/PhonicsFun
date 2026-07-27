package llm

import (
	"strings"
	"testing"

	"phonicsfun/internal/store"
)

func TestCMUDictLookup(t *testing.T) {
	d, err := loadCMUDict()
	if err != nil {
		t.Fatal(err)
	}
	if len(d.offsets) < 100000 {
		t.Fatalf("词典行数异常: %d", len(d.offsets))
	}
	cases := map[string]string{
		"word": "W ER1 D",
		"cat":  "K AE1 T",
	}
	for w, want := range cases {
		if got := d.Lookup(w); got != want {
			t.Errorf("Lookup(%q) = %q, want %q", w, got, want)
		}
	}
	// 大小写与空白归一化
	if got := d.Lookup("  WORD "); got != "W ER1 D" {
		t.Errorf("Lookup normalized = %q", got)
	}
	// 未收录词
	if got := d.Lookup("zzzznotaword"); got != "" {
		t.Errorf("Lookup(miss) = %q, want empty", got)
	}
	if got := d.Lookup(""); got != "" {
		t.Errorf("Lookup(empty) = %q, want empty", got)
	}
	// 首行词条（撇号开头）也可查到
	if got := d.Lookup("'bout"); got == "" {
		t.Error("Lookup('bout) should hit the first line")
	}
}

func TestBuildCardPrompt(t *testing.T) {
	p := buildCardPrompt("word", "W ER1 D")
	if !strings.Contains(p, "W ER1 D") || !strings.Contains(p, "现在处理单词：word") {
		t.Errorf("prompt 缺少注入内容:\n%s", p)
	}
	p2 := buildCardPrompt("zzz", "")
	if strings.Contains(p2, "权威发音参照") {
		t.Error("无 ARPAbet 时不应有参照段落")
	}
}

func TestBuildBlendScript(t *testing.T) {
	card := &store.Card{
		Word: "cake",
		Chunks: []store.Chunk{
			{Grapheme: "c", Respell: "k", AnchorWord: "kite"},
			{Grapheme: "a", Respell: "ay", AnchorWord: "name"},
			{Grapheme: "k", Respell: "k", AnchorWord: "kite"},
			{Grapheme: "e", Silent: true},
		},
	}
	s := BuildBlendScript(card)
	if !strings.Contains(s, `1. "k" (hint: the sound in "kite")`) ||
		!strings.Contains(s, `2. "ay" (hint: the sound in "name")`) ||
		!strings.Contains(s, `3. "k"`) ||
		!strings.Contains(s, `the whole word: "cake"`) {
		t.Errorf("blend script 错误:\n%s", s)
	}
	if strings.Contains(s, "4.") {
		t.Error("silent chunk 不应出现在脚本里")
	}
}

func TestBuildWordScript(t *testing.T) {
	s := BuildWordScript("Word")
	if !strings.Contains(s, `"word"`) {
		t.Errorf("word script 应含小写单词: %s", s)
	}
}

func TestNormalizeWords(t *testing.T) {
	in := []string{"Cat", "cat", "SHIP!", " don't ", "", "'", "苹果", "a"}
	got := normalizeWords(in)
	want := []string{"cat", "ship", "don't", "a"}
	if len(got) != len(want) {
		t.Fatalf("normalizeWords = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("normalizeWords[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
