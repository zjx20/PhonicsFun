// Package store implements the pure-file data layer.
//
// 布局：
//
//	DATA_DIR/words/<slug>/{card.json, word.wav, blend.wav}
//	DATA_DIR/groups/<id>.json
//
// 生成状态不落盘：产物文件的存在性即完成态（card.json=文本完成，两个
// wav=音频完成），进行中/失败状态只存在于 pipeline 的内存里。这里所有
// 写入都走 temp+fsync+rename 的原子路径，因此磁盘上不会出现半成品文件。
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Chunk 是一个字素-音素教学单位。硬不变量（由 llm 层生成时校验，前端渲
// 染时复验）：一张卡所有 chunk 的 Grapheme 依序拼接精确等于小写的 Word；
// silent chunk（如 magic-e 的哑音 e）的 Phoneme/Respell 为空字符串。
type Chunk struct {
	Grapheme   string `json:"grapheme"`
	Phoneme    string `json:"phoneme"`
	Respell    string `json:"respell"`
	AnchorWord string `json:"anchor_word"`
	Silent     bool   `json:"silent"`
}

// Card 是单词卡的全部文本内容，前端渲染与拼读音频 prompt 的唯一数据源。
type Card struct {
	Word         string    `json:"word"`
	IPA          string    `json:"ipa"`
	DefinitionZH string    `json:"definition_zh"`
	DefinitionEN string    `json:"definition_en"`
	Chunks       []Chunk   `json:"chunks"`
	GeneratedAt  time.Time `json:"generated_at"`
	Model        string    `json:"model"`
}

// JoinedGraphemes 返回 chunks 依序拼接出的字符串，用于与 Word 比对校验。
func (c *Card) JoinedGraphemes() string {
	var b strings.Builder
	for _, ch := range c.Chunks {
		b.WriteString(ch.Grapheme)
	}
	return b.String()
}

// Validate 检查两条硬不变量。
func (c *Card) Validate() error {
	if got, want := c.JoinedGraphemes(), strings.ToLower(c.Word); got != want {
		return fmt.Errorf("chunks 拼接 %q 与单词 %q 不一致", got, want)
	}
	if len(c.Chunks) == 0 {
		return fmt.Errorf("chunks 为空")
	}
	for i, ch := range c.Chunks {
		if !ch.Silent && (ch.Respell == "" || ch.AnchorWord == "") {
			return fmt.Errorf("chunk %d (%q) 缺 respell 或 anchor_word", i, ch.Grapheme)
		}
	}
	return nil
}

type Group struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	Words     []string  `json:"words"`
}

// AudioKind 标识一个单词的两段音频之一。
type AudioKind string

const (
	AudioWord  AudioKind = "word"  // 整词发音
	AudioBlend AudioKind = "blend" // 逐音素拼读
)

type Store struct {
	dir string
}

func New(dir string) (*Store, error) {
	for _, sub := range []string{"words", "groups"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{dir: dir}, nil
}

// Slug 把单词归一化成文件系统安全的目录名：小写，仅保留 [a-z0-9']，
// 撇号替换为下划线。同 slug 即同词，跨组天然共享缓存。
func Slug(word string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(word)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '\'':
			b.WriteByte('_')
		}
	}
	return b.String()
}

// --- 单词产物 ---

func (s *Store) wordDir(slug string) string {
	return filepath.Join(s.dir, "words", slug)
}

func (s *Store) CardPath(slug string) string {
	return filepath.Join(s.wordDir(slug), "card.json")
}

func (s *Store) AudioPath(slug string, kind AudioKind) string {
	return filepath.Join(s.wordDir(slug), string(kind)+".wav")
}

func (s *Store) HasCard(slug string) bool {
	_, err := os.Stat(s.CardPath(slug))
	return err == nil
}

func (s *Store) HasAudio(slug string, kind AudioKind) bool {
	_, err := os.Stat(s.AudioPath(slug, kind))
	return err == nil
}

func (s *Store) ReadCard(slug string) (*Card, error) {
	data, err := os.ReadFile(s.CardPath(slug))
	if err != nil {
		return nil, err
	}
	var c Card
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", s.CardPath(slug), err)
	}
	return &c, nil
}

func (s *Store) WriteCard(slug string, c *Card) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.CardPath(slug), data)
}

func (s *Store) WriteAudio(slug string, kind AudioKind, wavData []byte) error {
	return writeFileAtomic(s.AudioPath(slug, kind), wavData)
}

// DeleteCard/DeleteAudio 用于 force 重新生成前清除旧产物（不存在不报错）。
func (s *Store) DeleteCard(slug string) error {
	return removeIfExists(s.CardPath(slug))
}

func (s *Store) DeleteAudio(slug string) error {
	if err := removeIfExists(s.AudioPath(slug, AudioWord)); err != nil {
		return err
	}
	return removeIfExists(s.AudioPath(slug, AudioBlend))
}

// --- 组 ---

func (s *Store) groupPath(id string) string {
	return filepath.Join(s.dir, "groups", id+".json")
}

// CreateGroup 创建组。ID 用创建时间派生，可读且天然按时间排序；同秒冲突
// 时追加序号。
func (s *Store) CreateGroup(name string, words []string) (*Group, error) {
	now := time.Now()
	if name == "" {
		name = now.Format("1月2日") + " 单词组"
	}
	id := now.Format("20060102-150405")
	for i := 2; ; i++ {
		if _, err := os.Stat(s.groupPath(id)); os.IsNotExist(err) {
			break
		}
		id = fmt.Sprintf("%s-%d", now.Format("20060102-150405"), i)
	}
	g := &Group{ID: id, Name: name, CreatedAt: now, Words: words}
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeFileAtomic(s.groupPath(id), data); err != nil {
		return nil, err
	}
	return g, nil
}

func (s *Store) GetGroup(id string) (*Group, error) {
	// id 来自 URL，先做路径穿越防护：group ID 只含 [0-9-]。
	if filepath.Base(id) != id || strings.ContainsAny(id, "/\\.") {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(s.groupPath(id))
	if err != nil {
		return nil, err
	}
	var g Group
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", s.groupPath(id), err)
	}
	return &g, nil
}

// ListGroups 按创建时间倒序返回全部组。
func (s *Store) ListGroups() ([]*Group, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, "groups"))
	if err != nil {
		return nil, err
	}
	var groups []*Group
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		g, err := s.GetGroup(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue // 跳过损坏文件，不让一个坏组拖垮列表
		}
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].CreatedAt.After(groups[j].CreatedAt)
	})
	return groups, nil
}

// DeleteGroup 删除组；purge 时连带删除不再被任何其他组引用的单词目录。
func (s *Store) DeleteGroup(id string, purge bool) error {
	g, err := s.GetGroup(id)
	if err != nil {
		return err
	}
	if err := os.Remove(s.groupPath(id)); err != nil {
		return err
	}
	if !purge {
		return nil
	}
	others, err := s.ListGroups()
	if err != nil {
		return err
	}
	referenced := map[string]bool{}
	for _, o := range others {
		for _, w := range o.Words {
			referenced[Slug(w)] = true
		}
	}
	for _, w := range g.Words {
		slug := Slug(w)
		if slug != "" && !referenced[slug] {
			if err := os.RemoveAll(s.wordDir(slug)); err != nil {
				return err
			}
		}
	}
	return nil
}

// --- 原子写 ---

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功后为 no-op
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
