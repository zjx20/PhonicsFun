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

func TestSyllableCount(t *testing.T) {
	cases := map[string]int{
		"W ER1 D":                      1,
		"R EH2 P L AH0 K EY1 SH AH0 N": 4,
		"K AE1 T":                      1,
		"":                             0,
	}
	for in, want := range cases {
		if got := SyllableCount(in); got != want {
			t.Errorf("SyllableCount(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestHyphDict(t *testing.T) {
	h, err := loadHyphDict()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"replication": "rep-li-ca-tion",
		"tiger":       "ti-ger",
		"about":       "a-bout",
		"cat":         "cat", // 单音节词也收录，防过度拆分
	}
	for w, want := range cases {
		if got := h.lookup(w); got != want {
			t.Errorf("hyph.lookup(%q) = %q, want %q", w, got, want)
		}
	}
	if got := h.lookup("zzzznotaword"); got != "" {
		t.Errorf("miss 应返回空，got %q", got)
	}
	// 后缀剥离兜底：tigers 不在词表，剥 -s 命中 tiger
	if got := h.Ref("tigers"); got != "ti-ger + -s" {
		t.Errorf("Ref(tigers) = %q", got)
	}
	// 直查命中时原样返回
	if got := h.Ref("replication"); got != "rep-li-ca-tion" {
		t.Errorf("Ref(replication) = %q", got)
	}
}

func TestPOSDict(t *testing.T) {
	p, err := loadPOSDict()
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Lookup("light"); got != "n.,adj.,v.,adv." {
		t.Errorf("pos.Lookup(light) = %q", got)
	}
	if got := p.Lookup("cat"); !strings.HasPrefix(got, "n.") {
		t.Errorf("pos.Lookup(cat) = %q", got)
	}
	if got := p.Lookup("zzzznotaword"); got != "" {
		t.Errorf("miss 应返回空，got %q", got)
	}
}

func TestBuildCardPrompt(t *testing.T) {
	refs := cardRefs{ARPAbet: "W ER1 D", SylCount: 1, Hyph: "word", POS: "n.,v."}
	p := buildCardPrompt("word", refs, "")
	for _, want := range []string{"W ER1 D", "音节切分参照", "词性参照", "n.,v.", "现在处理单词：word"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt 缺少 %q", want)
		}
	}
	if strings.Contains(p, "<feedback>") {
		t.Error("无反馈时不应有反馈段落")
	}
	p2 := buildCardPrompt("zzz", cardRefs{}, "")
	if strings.Contains(p2, "权威参照") {
		t.Error("无任何参照时不应有参照段落")
	}

	// 用户反馈注入在 <feedback> 标签内，且位于"现在处理单词"之前
	pf := buildCardPrompt("word", refs, "音标不对")
	if !strings.Contains(pf, "<feedback>\n音标不对\n</feedback>") {
		t.Errorf("反馈未注入 prompt:\n%s", pf)
	}
	// 校验失败重试的 prompt 同样要保留反馈
	pr := buildCardRetryPrompt("word", refs, "音标不对", "拼接不等于原词")
	if !strings.Contains(pr, "<feedback>") || !strings.Contains(pr, "拼接不等于原词") {
		t.Error("重试 prompt 应同时含反馈与失败原因")
	}
}

// multiCard 构造多音节测试卡（action = ac + tion，tion 是单 chunk 音节）。
func multiCard() *store.Card {
	return &store.Card{
		Word: "action",
		Syllables: []store.Syllable{
			{Text: "ac", Respell: "ak", Chunks: []store.Chunk{
				{Grapheme: "a", Respell: "a", AnchorWord: "apple"},
				{Grapheme: "c", Respell: "k", AnchorWord: "cat"},
			}},
			{Text: "tion", Respell: "shun", Chunks: []store.Chunk{
				{Grapheme: "tion", Respell: "shun", AnchorWord: "station"},
			}},
		},
	}
}

func TestBuildBlendLinesMulti(t *testing.T) {
	lines := BuildBlendLines(multiCard())
	// 期望：a、c 两个 chunk 行 + ac 音节行 + tion 音节行（单 chunk 音节无
	// chunk 行，但音节行要带上那个 chunk 的下标）+ tail
	if len(lines) != 5 {
		t.Fatalf("行数 = %d, want 5: %+v", len(lines), lines)
	}
	if lines[0].Kind != BlendChunk || lines[0].Syllable != 0 || lines[0].Chunk != 0 || lines[0].Say != "a" {
		t.Errorf("行 0 错误: %+v", lines[0])
	}
	if lines[2].Kind != BlendSyllable || lines[2].Syllable != 0 || lines[2].Chunk != -1 || lines[2].Say != "ak" {
		t.Errorf("行 2 错误: %+v", lines[2])
	}
	if lines[3].Kind != BlendSyllable || lines[3].Syllable != 1 || lines[3].Chunk != 0 || lines[3].Say != "shun" {
		t.Errorf("行 3（单 chunk 音节）错误: %+v", lines[3])
	}
	if lines[4].Kind != BlendTail {
		t.Errorf("末行应为 tail: %+v", lines[4])
	}

	body := BlendBodyScript(lines)
	if !strings.Contains(body, `"a" (hint: the sound in "apple")`) ||
		!strings.Contains(body, `"ak" (hint: one whole syllable)`) ||
		!strings.Contains(body, `"shun" (hint: one whole syllable)`) {
		t.Errorf("body 脚本错误:\n%s", body)
	}
	if strings.Contains(body, "tion") {
		t.Error("body 脚本不应出现裸拼写 tion（模型会读错）")
	}
}

func TestBuildBlendLinesSingle(t *testing.T) {
	card := &store.Card{
		Word: "cake",
		Syllables: []store.Syllable{{Text: "cake", Respell: "kayk", Chunks: []store.Chunk{
			{Grapheme: "c", Respell: "k", AnchorWord: "kite"},
			{Grapheme: "a", Respell: "ay", AnchorWord: "name"},
			{Grapheme: "k", Respell: "k", AnchorWord: "kite"},
			{Grapheme: "e", Silent: true},
		}}},
	}
	lines := BuildBlendLines(card)
	// 单音节：3 个 chunk 行（silent 跳过）+ tail，不出音节行
	if len(lines) != 4 {
		t.Fatalf("行数 = %d, want 4: %+v", len(lines), lines)
	}
	for _, ln := range lines[:3] {
		if ln.Kind != BlendChunk {
			t.Errorf("单音节词不应有音节行: %+v", ln)
		}
	}
}

func TestBuildWordScript(t *testing.T) {
	s := BuildWordScript("Word")
	if !strings.Contains(s, `"word"`) {
		t.Errorf("word script 应含小写单词: %s", s)
	}
	// blend 收尾段靠静音分割切出慢速/常速两遍，脚本必须明确要求两遍之间静音
	if !strings.Contains(s, "silence") {
		t.Errorf("word script 应要求两遍之间的明确静音: %s", s)
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
