package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Word":    "word",
		"  CAT  ": "cat",
		"don't":   "don_t",
		"e-mail":  "email",
		"naïve":   "nave", // 非 ASCII 丢弃；本应用只处理英文单词
		"":        "",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

// validCard 构造一张合法的 v2 卡（action = ac + tion），供各校验用例做局部破坏。
func validCard() *Card {
	return &Card{
		Schema: CardSchemaVersion,
		Word:   "action",
		IPA:    "/ˈækʃən/",
		Senses: []Sense{{POS: "n.", ZH: "行动", EN: "something you do"}},
		Examples: []Example{
			{EN: "Let's take action now.", ZH: "我们现在就行动吧。"},
		},
		Syllables: []Syllable{
			{Text: "ac", Respell: "ak", Chunks: []Chunk{
				{Grapheme: "a", Phoneme: "/æ/", Respell: "a", AnchorWord: "apple"},
				{Grapheme: "c", Phoneme: "/k/", Respell: "k", AnchorWord: "kite"},
			}},
			{Text: "tion", Respell: "shun", Chunks: []Chunk{
				{Grapheme: "tion", Phoneme: "/ʃən/", Respell: "shun", AnchorWord: "station"},
			}},
		},
	}
}

func TestCardValidate(t *testing.T) {
	if err := validCard().Validate(); err != nil {
		t.Fatalf("合法卡被拒: %v", err)
	}

	// 单音节 + magic-e 哑音 chunk
	cake := &Card{
		Schema: CardSchemaVersion, Word: "cake",
		Senses:   []Sense{{POS: "n.", ZH: "蛋糕", EN: "a sweet food"}},
		Examples: []Example{{EN: "I like cake.", ZH: "我喜欢蛋糕。"}},
		Syllables: []Syllable{{Text: "cake", Respell: "kayk", Chunks: []Chunk{
			{Grapheme: "c", Phoneme: "/k/", Respell: "k", AnchorWord: "kite"},
			{Grapheme: "a", Phoneme: "/eɪ/", Respell: "ay", AnchorWord: "name"},
			{Grapheme: "k", Phoneme: "/k/", Respell: "k", AnchorWord: "kite"},
			{Grapheme: "e", Silent: true},
		}}},
	}
	if err := cake.Validate(); err != nil {
		t.Fatalf("合法单音节卡被拒: %v", err)
	}

	breakIt := func(name string, mutate func(*Card)) {
		c := validCard()
		mutate(c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s 未被检测出", name)
		}
	}
	breakIt("音节拼接与单词不一致", func(c *Card) { c.Syllables[0].Text = "ax" })
	breakIt("音节内 chunk 拼接不一致", func(c *Card) { c.Syllables[0].Chunks[1].Grapheme = "k" })
	breakIt("音节缺 respell", func(c *Card) { c.Syllables[1].Respell = "" })
	breakIt("空 syllables", func(c *Card) { c.Syllables = nil })
	breakIt("音节没有 chunk", func(c *Card) { c.Syllables[1].Chunks = nil })
	breakIt("非 silent chunk 缺 anchor_word", func(c *Card) { c.Syllables[0].Chunks[0].AnchorWord = "" })
	// 把整音节读音塞给单个 chunk（replication 的 i 曾被生成为整音节的
	// "luh"）；单 voiced chunk 音节两者相等合法，validCard 的 tion 已覆盖
	breakIt("chunk respell 等于整音节 respell", func(c *Card) { c.Syllables[0].Chunks[0].Respell = "ak" })
	breakIt("音节全 silent", func(c *Card) {
		c.Syllables[1].Chunks[0].Silent = true
		c.Syllables[1].Chunks[0].Respell = ""
	})
	breakIt("空 senses", func(c *Card) { c.Senses = nil })
	breakIt("非法词性缩写", func(c *Card) { c.Senses[0].POS = "noun" })
	breakIt("sense 缺中文", func(c *Card) { c.Senses[0].ZH = "" })
	breakIt("空 examples", func(c *Card) { c.Examples = nil })
	breakIt("example 缺英文", func(c *Card) { c.Examples[0].EN = "" })
	// digraph 被拆开：sh 拆成 s|h
	breakIt("digraph 被拆开", func(c *Card) {
		c.Word = "shac"
		c.Syllables[0].Text = "shac"
		c.Syllables[0].Chunks = []Chunk{
			{Grapheme: "s", Phoneme: "/s/", Respell: "s", AnchorWord: "sun"},
			{Grapheme: "h", Phoneme: "/h/", Respell: "h", AnchorWord: "hat"},
			{Grapheme: "a", Phoneme: "/æ/", Respell: "a", AnchorWord: "apple"},
			{Grapheme: "c", Phoneme: "/k/", Respell: "k", AnchorWord: "kite"},
		}
	})
	// blend 被合并成一个 chunk：pl
	breakIt("blend 合并成一个 chunk", func(c *Card) {
		c.Word = "plaction"
		c.Syllables[0].Text = "plac"
		c.Syllables[0].Chunks = []Chunk{
			{Grapheme: "pl", Phoneme: "/pl/", Respell: "pl", AnchorWord: "play"},
			{Grapheme: "a", Phoneme: "/æ/", Respell: "a", AnchorWord: "apple"},
			{Grapheme: "c", Phoneme: "/k/", Respell: "k", AnchorWord: "kite"},
		}
	})

	// listen 的 st 整体成 chunk（t 不发音）是合法的——st 故意不在黑名单
	listen := &Card{
		Schema: CardSchemaVersion, Word: "listen",
		Senses:   []Sense{{POS: "v.", ZH: "听", EN: "to hear"}},
		Examples: []Example{{EN: "Listen to me.", ZH: "听我说。"}},
		Syllables: []Syllable{
			{Text: "lis", Respell: "lis", Chunks: []Chunk{
				{Grapheme: "l", Phoneme: "/l/", Respell: "l", AnchorWord: "leg"},
				{Grapheme: "i", Phoneme: "/ɪ/", Respell: "ih", AnchorWord: "sit"},
				{Grapheme: "s", Phoneme: "/s/", Respell: "s", AnchorWord: "sun"},
			}},
			{Text: "ten", Respell: "tun", Chunks: []Chunk{
				{Grapheme: "t", Silent: true},
				{Grapheme: "e", Phoneme: "/ə/", Respell: "uh", AnchorWord: "about"},
				{Grapheme: "n", Phoneme: "/n/", Respell: "n", AnchorWord: "net"},
			}},
		},
	}
	if err := listen.Validate(); err != nil {
		t.Errorf("listen 卡应合法: %v", err)
	}
}

func TestCardRoundTripAndAtomicity(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	card := validCard()
	card.GeneratedAt = time.Now().UTC()
	card.Model = "test"
	slug := Slug(card.Word)
	if s.HasCard(slug) {
		t.Fatal("card should not exist yet")
	}
	if err := s.WriteCard(slug, card); err != nil {
		t.Fatal(err)
	}
	if !s.HasCard(slug) {
		t.Fatal("card missing after write")
	}
	got, err := s.ReadCard(slug)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != CardSchemaVersion || got.Word != "action" ||
		len(got.Syllables) != 2 || got.Syllables[1].Text != "tion" ||
		len(got.Senses) != 1 || got.Senses[0].POS != "n." || len(got.Examples) != 1 {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// 原子写不留临时文件
	entries, _ := os.ReadDir(filepath.Dir(s.CardPath(slug)))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}

	// 音频与 cues 写入、DeleteAudio 级联删除
	if err := s.WriteAudio(slug, AudioWord, []byte("RIFFfake")); err != nil {
		t.Fatal(err)
	}
	if !s.HasAudio(slug, AudioWord) || s.HasAudio(slug, AudioBlend) {
		t.Error("audio presence wrong")
	}
	cues := &Cues{Version: 1, SampleRate: 24000, Cues: []Cue{
		{Kind: "chunk", Syllable: 0, Chunk: 0, StartMS: 0, EndMS: 400},
		{Kind: "tail", Syllable: -1, Chunk: -1, StartMS: 700, EndMS: 2000},
	}}
	if err := s.WriteCues(slug, cues); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.CuesPath(slug)); err != nil {
		t.Fatal("cues 未落盘")
	}
	if err := s.DeleteAudio(slug); err != nil {
		t.Fatal(err)
	}
	if s.HasAudio(slug, AudioWord) {
		t.Error("audio still present after delete")
	}
	if _, err := os.Stat(s.CuesPath(slug)); !os.IsNotExist(err) {
		t.Error("DeleteAudio 应连带删除 cues")
	}
}

func TestGroupCRUDAndPurge(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g1, err := s.CreateGroup("", []string{"cat", "dog"})
	if err != nil {
		t.Fatal(err)
	}
	if g1.Name == "" || g1.ID == "" {
		t.Fatalf("default name/id not set: %+v", g1)
	}
	g2, err := s.CreateGroup("组二", []string{"dog", "fish"})
	if err != nil {
		t.Fatal(err)
	}
	if g1.ID == g2.ID {
		t.Fatal("duplicate group id")
	}

	list, err := s.ListGroups()
	if err != nil || len(list) != 2 {
		t.Fatalf("ListGroups = %v, %v", list, err)
	}

	// 造出产物文件再验证 purge 的引用计数
	for _, w := range []string{"cat", "dog", "fish"} {
		if err := s.WriteAudio(Slug(w), AudioWord, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	// 删 g1 并 purge：cat 无人引用应被删；dog 仍被 g2 引用应保留
	if err := s.DeleteGroup(g1.ID, true); err != nil {
		t.Fatal(err)
	}
	if s.HasAudio(Slug("cat"), AudioWord) {
		t.Error("cat should be purged")
	}
	if !s.HasAudio(Slug("dog"), AudioWord) {
		t.Error("dog still referenced by g2, must survive")
	}

	if _, err := s.GetGroup(g1.ID); !os.IsNotExist(err) {
		t.Errorf("deleted group still readable: %v", err)
	}
	if _, err := s.GetGroup("../evil"); !os.IsNotExist(err) {
		t.Errorf("path traversal not blocked: %v", err)
	}
}

func TestUpdateGroup(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g1, err := s.CreateGroup("原名", []string{"cat", "dog", "sun"})
	if err != nil {
		t.Fatal(err)
	}
	// dog 同时被另一组引用，编辑移除后必须幸免于清理
	if _, err := s.CreateGroup("组二", []string{"dog"}); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"cat", "dog", "sun"} {
		if err := s.WriteAudio(Slug(w), AudioWord, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}

	// 改名 + 改词表：移除 dog、sun，修正 cat → car，新增 fish
	got, err := s.UpdateGroup(g1.ID, "新名", []string{"car", "fish"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "新名" || got.ID != g1.ID {
		t.Errorf("update result mismatch: %+v", got)
	}
	back, err := s.GetGroup(g1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if back.Name != "新名" || len(back.Words) != 2 || back.Words[0] != "car" || back.Words[1] != "fish" {
		t.Errorf("group after update = %+v", back)
	}
	if !back.CreatedAt.Equal(g1.CreatedAt) {
		t.Errorf("CreatedAt changed on update: %v → %v", g1.CreatedAt, back.CreatedAt)
	}
	// cat、sun 已无人引用应被清理；dog 仍被组二引用必须保留
	if s.HasAudio(Slug("cat"), AudioWord) {
		t.Error("cat should be purged after removal")
	}
	if s.HasAudio(Slug("sun"), AudioWord) {
		t.Error("sun should be purged after removal")
	}
	if !s.HasAudio(Slug("dog"), AudioWord) {
		t.Error("dog still referenced by another group, must survive")
	}

	// 空白名保留原名，词表顺序即传入顺序
	back, err = s.UpdateGroup(g1.ID, "  ", []string{"fish", "car"})
	if err != nil {
		t.Fatal(err)
	}
	if back.Name != "新名" {
		t.Errorf("blank name should keep old name, got %q", back.Name)
	}
	if back.Words[0] != "fish" || back.Words[1] != "car" {
		t.Errorf("word order not preserved: %v", back.Words)
	}

	if _, err := s.UpdateGroup("19990101-000000", "x", []string{"cat"}); !os.IsNotExist(err) {
		t.Errorf("updating missing group should be not-exist, got %v", err)
	}
}
