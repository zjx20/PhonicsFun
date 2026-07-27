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

func TestCardValidate(t *testing.T) {
	good := &Card{
		Word: "cake",
		Chunks: []Chunk{
			{Grapheme: "c", Phoneme: "/k/", Respell: "k", AnchorWord: "kite"},
			{Grapheme: "a", Phoneme: "/eɪ/", Respell: "ay", AnchorWord: "name"},
			{Grapheme: "k", Phoneme: "/k/", Respell: "k", AnchorWord: "kite"},
			{Grapheme: "e", Silent: true},
		},
	}
	if err := good.Validate(); err != nil {
		t.Errorf("valid card rejected: %v", err)
	}

	bad := &Card{
		Word: "cake",
		Chunks: []Chunk{
			{Grapheme: "c", Respell: "k", AnchorWord: "kite"},
			{Grapheme: "ake", Respell: "ake", AnchorWord: "cake"},
		},
	}
	if err := bad.Validate(); err != nil {
		t.Errorf("joined graphemes match, should pass: %v", err)
	}

	mismatch := &Card{
		Word:   "cake",
		Chunks: []Chunk{{Grapheme: "ca", Respell: "ka", AnchorWord: "cat"}},
	}
	if err := mismatch.Validate(); err == nil {
		t.Error("grapheme mismatch not detected")
	}

	missingRespell := &Card{
		Word:   "at",
		Chunks: []Chunk{{Grapheme: "a", Respell: "a", AnchorWord: "apple"}, {Grapheme: "t"}},
	}
	if err := missingRespell.Validate(); err == nil {
		t.Error("missing respell on non-silent chunk not detected")
	}
}

func TestCardRoundTripAndAtomicity(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	card := &Card{
		Word: "word", IPA: "/wɝːd/",
		DefinitionZH: "单词", DefinitionEN: "a unit of language",
		Chunks: []Chunk{
			{Grapheme: "w", Phoneme: "/w/", Respell: "wuh", AnchorWord: "wet"},
			{Grapheme: "or", Phoneme: "/ɝː/", Respell: "er", AnchorWord: "her"},
			{Grapheme: "d", Phoneme: "/d/", Respell: "duh", AnchorWord: "dog"},
		},
		GeneratedAt: time.Now().UTC(), Model: "test",
	}
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
	if got.Word != "word" || len(got.Chunks) != 3 || got.Chunks[1].Grapheme != "or" {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// 原子写不留临时文件
	entries, _ := os.ReadDir(filepath.Dir(s.CardPath(slug)))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}

	// 音频写入与删除
	if err := s.WriteAudio(slug, AudioWord, []byte("RIFFfake")); err != nil {
		t.Fatal(err)
	}
	if !s.HasAudio(slug, AudioWord) || s.HasAudio(slug, AudioBlend) {
		t.Error("audio presence wrong")
	}
	if err := s.DeleteAudio(slug); err != nil {
		t.Fatal(err)
	}
	if s.HasAudio(slug, AudioWord) {
		t.Error("audio still present after delete")
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
