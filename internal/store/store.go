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

// CardSchemaVersion 是 card.json 的当前结构版本。启动恢复扫描
// （pipeline.Recover）发现旧版本卡片时会删除产物并重新生成。
const CardSchemaVersion = 2

// Chunk 是音节内的一个字素-音素教学单位。硬不变量见 Card.Validate：
// 一个音节内所有 chunk 的 Grapheme 依序拼接精确等于该音节的 Text；
// silent chunk（如 magic-e 的哑音 e）的 Phoneme/Respell 为空字符串。
// Respell 是 spelling voice——每个元音读本音、不弱读，供逐块拼读。
type Chunk struct {
	Grapheme   string `json:"grapheme"`
	Phoneme    string `json:"phoneme"`
	Respell    string `json:"respell"`
	AnchorWord string `json:"anchor_word"`
	Silent     bool   `json:"silent"`
}

// Syllable 是拼读拆解的第一级：所有音节的 Text 依序拼接精确等于小写的
// Word。Respell 是该音节的真实读音注音（含 schwa 弱读，如 tion→"shun"），
// 音频脚本靠它朗读音节——Live 模型直接读 Text 会读错（tion→tee-on）。
type Syllable struct {
	Text    string  `json:"text"`
	Respell string  `json:"respell"`
	Chunks  []Chunk `json:"chunks"`
}

// Sense 是一条按词性组织的释义。
type Sense struct {
	POS string `json:"pos"` // 标准缩写：n. v. adj. adv. pron. prep. conj. int. num. art.
	ZH  string `json:"zh"`
	EN  string `json:"en"`
}

// Example 是一条面向儿童的例句。
type Example struct {
	EN string `json:"en"`
	ZH string `json:"zh"`
}

// Card 是单词卡的全部文本内容，前端渲染与拼读音频 prompt 的唯一数据源。
type Card struct {
	Schema      int        `json:"schema"`
	Word        string     `json:"word"`
	IPA         string     `json:"ipa"`
	Senses      []Sense    `json:"senses"`
	Examples    []Example  `json:"examples"`
	Syllables   []Syllable `json:"syllables"`
	GeneratedAt time.Time  `json:"generated_at"`
	Model       string     `json:"model"`
}

// ValidPOS 是 Sense.POS 允许的标准缩写集合（生成 prompt 与校验共用）。
var ValidPOS = map[string]bool{
	"n.": true, "v.": true, "adj.": true, "adv.": true, "pron.": true,
	"prep.": true, "conj.": true, "int.": true, "num.": true, "art.": true,
}

// neverSplitDigraphs：同一音节内被拆成相邻两个 chunk 即判违规的辅音
// digraph/trigraph（长的在前，检查时先匹配 trigraph）。
var neverSplitDigraphs = []string{"tch", "dge", "sh", "ch", "th", "ph", "wh", "ck", "ng"}

// mergedBlends：辅音 blend 里每个音素都发音（Moats："blend 不是一个音"），
// 合成一个 chunk 即判违规。故意不含 st/sc——它们可以合法地作为带哑音
// 字母的整体 chunk 出现（listen 的 st 读 /s/、science 的 sc 读 /s/）。
var mergedBlends = map[string]bool{
	"bl": true, "cl": true, "fl": true, "gl": true, "pl": true, "sl": true,
	"br": true, "cr": true, "dr": true, "fr": true, "gr": true, "pr": true, "tr": true,
	"sk": true, "sm": true, "sn": true, "sp": true, "sw": true, "tw": true,
	"str": true, "spr": true, "scr": true, "spl": true,
}

// JoinedGraphemes 返回全部音节的 chunk 字素依序拼接出的字符串。
func (c *Card) JoinedGraphemes() string {
	var b strings.Builder
	for _, s := range c.Syllables {
		for _, ch := range s.Chunks {
			b.WriteString(ch.Grapheme)
		}
	}
	return b.String()
}

// joinedSyllables 返回音节 Text 依序拼接出的字符串。
func (c *Card) joinedSyllables() string {
	var b strings.Builder
	for _, s := range c.Syllables {
		b.WriteString(s.Text)
	}
	return b.String()
}

// Validate 检查 card.json 的全部硬不变量（llm 生成时校验，前端渲染时复验）。
func (c *Card) Validate() error {
	if len(c.Syllables) == 0 {
		return fmt.Errorf("syllables 为空")
	}
	if got, want := c.joinedSyllables(), strings.ToLower(c.Word); got != want {
		return fmt.Errorf("音节拼接 %q 与单词 %q 不一致", got, want)
	}
	for si, syl := range c.Syllables {
		if len(syl.Chunks) == 0 {
			return fmt.Errorf("音节 %d (%q) 没有 chunk", si, syl.Text)
		}
		if syl.Respell == "" {
			return fmt.Errorf("音节 %d (%q) 缺 respell", si, syl.Text)
		}
		var joined strings.Builder
		hasVoiced := false
		for ci, ch := range syl.Chunks {
			if ch.Grapheme == "" {
				return fmt.Errorf("音节 %d (%q) 的 chunk %d 字素为空", si, syl.Text, ci)
			}
			joined.WriteString(ch.Grapheme)
			if ch.Silent {
				continue
			}
			hasVoiced = true
			if ch.Respell == "" || ch.AnchorWord == "" {
				return fmt.Errorf("音节 %d 的 chunk %q 缺 respell 或 anchor_word", si, ch.Grapheme)
			}
			if mergedBlends[strings.ToLower(ch.Grapheme)] {
				return fmt.Errorf("chunk %q 是辅音 blend——blend 不是一个音，必须逐字母拆开", ch.Grapheme)
			}
		}
		if joined.String() != syl.Text {
			return fmt.Errorf("音节 %d 的 chunk 拼接 %q 与音节 %q 不一致", si, joined.String(), syl.Text)
		}
		if !hasVoiced {
			return fmt.Errorf("音节 %d (%q) 全部 chunk 都是 silent", si, syl.Text)
		}
		if g := splitDigraph(syl.Chunks); g != "" {
			return fmt.Errorf("音节 %d 内 digraph %q 被拆开——digraph 是一个音，必须作为整体 chunk", si, g)
		}
	}
	if len(c.Senses) == 0 {
		return fmt.Errorf("senses 为空")
	}
	for i, s := range c.Senses {
		if !ValidPOS[s.POS] {
			return fmt.Errorf("sense %d 词性 %q 不在标准缩写集合内", i, s.POS)
		}
		if s.ZH == "" {
			return fmt.Errorf("sense %d (%s) 缺中文释义", i, s.POS)
		}
	}
	if len(c.Examples) == 0 {
		return fmt.Errorf("examples 为空")
	}
	for i, e := range c.Examples {
		if e.EN == "" {
			return fmt.Errorf("example %d 缺英文句", i)
		}
	}
	return nil
}

// splitDigraph 检查同一音节内是否有相邻非 silent chunk 拼出 digraph；
// 命中返回该 digraph，否则返回空串。
func splitDigraph(chunks []Chunk) string {
	for i := 0; i+1 < len(chunks); i++ {
		if chunks[i].Silent || chunks[i+1].Silent {
			continue
		}
		pair := strings.ToLower(chunks[i].Grapheme + chunks[i+1].Grapheme)
		for _, d := range neverSplitDigraphs {
			if pair == d {
				return d
			}
		}
	}
	return ""
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
	AudioBlend AudioKind = "blend" // 拼读（音节内逐块拼 → 合成音节 → 连读成词）
)

// Cue 是 blend.wav 内一个分段的时间标注（相对文件起点的毫秒）。
// Kind: "chunk" | "syllable" | "tail" | "word"；Syllable/Chunk 是卡片里的
// 下标（Chunk 含 silent 在内的音节内下标），不适用时为 -1。
// "word" 是 tail 段内整词部分的子区间（点大字区单独播一遍完整读音用），
// 与 tail 重叠且总在 tail 之后——整段播放的命中逻辑靠这个顺序先取到 tail。
type Cue struct {
	Kind     string `json:"kind"`
	Syllable int    `json:"syllable"`
	Chunk    int    `json:"chunk"`
	StartMS  int    `json:"start_ms"`
	EndMS    int    `json:"end_ms"`
}

// Cues 是 blend.cues.json 的完整内容，与 blend.wav 成对生成。
// 写入顺序约定：先写 cues 再写 wav——完成态判定只看 wav 的存在性
// （见包注释的"产物存在性即状态"），因此 wav 在则 cues 必在。
type Cues struct {
	Version    int   `json:"version"`
	SampleRate int   `json:"sample_rate"`
	Cues       []Cue `json:"cues"`
}

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

// AudioVersion 返回音频产物的缓存版本号：word.wav 与 blend.wav 的最新
// mtime（UnixMilli），无音频时为 0。前端把它拼进音频/cues URL 的 ?v= 参数：
// 音频重新生成必然重写文件、版本必变，浏览器缓存随之失效。文本的
// generated_at 不能用作这个版本——audio-only 重生成不动 card.json。
func (s *Store) AudioVersion(slug string) int64 {
	var v int64
	for _, k := range []AudioKind{AudioWord, AudioBlend} {
		if fi, err := os.Stat(s.AudioPath(slug, k)); err == nil {
			if m := fi.ModTime().UnixMilli(); m > v {
				v = m
			}
		}
	}
	return v
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

// CuesPath 返回 blend 音频时间标注文件的路径。
func (s *Store) CuesPath(slug string) string {
	return filepath.Join(s.wordDir(slug), "blend.cues.json")
}

// WriteCues 落盘 blend 时间标注。必须先于对应的 blend.wav 写入。
func (s *Store) WriteCues(slug string, c *Cues) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.CuesPath(slug), data)
}

// DeleteCard/DeleteAudio 用于 force 重新生成前清除旧产物（不存在不报错）。
func (s *Store) DeleteCard(slug string) error {
	return removeIfExists(s.CardPath(slug))
}

func (s *Store) DeleteAudio(slug string) error {
	if err := removeIfExists(s.AudioPath(slug, AudioWord)); err != nil {
		return err
	}
	if err := removeIfExists(s.AudioPath(slug, AudioBlend)); err != nil {
		return err
	}
	// cues 与 blend.wav 同生共死
	return removeIfExists(s.CuesPath(slug))
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

// UpdateGroup 全量替换组的名称与词表（编辑保存）。name 为空白时保留原名；
// words 的顺序即学习时的翻卡顺序。被移除的词若不再被任何组引用，连带删除
// 其产物目录——修错词不留垃圾；代价是误删单词再加回时需重新生成一次。
func (s *Store) UpdateGroup(id, name string, words []string) (*Group, error) {
	g, err := s.GetGroup(id)
	if err != nil {
		return nil, err
	}
	oldWords := g.Words
	if name = strings.TrimSpace(name); name != "" {
		g.Name = name
	}
	g.Words = words
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeFileAtomic(s.groupPath(id), data); err != nil {
		return nil, err
	}
	// 清理必须在新词表落盘之后：purgeUnreferenced 重新扫描全部组，本组
	// 仍保留的词会被新词表引用而幸免，因此直接传旧词表即可。
	if err := s.purgeUnreferenced(oldWords); err != nil {
		return nil, err
	}
	return g, nil
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
	return s.purgeUnreferenced(g.Words)
}

// purgeUnreferenced 删除给定单词中不再被任何组引用的词目录（按 slug 判定，
// 以磁盘上当前的组文件为准，调用方需先完成组文件的删除/改写）。清理是
// best-effort：若某词恰在 pipeline 生成中，worker 的原子写会重建目录、留下
// 一个孤儿但完整的产物目录——无害（同词再导入时还能复用），故这里不查
// 生成状态。
func (s *Store) purgeUnreferenced(words []string) error {
	groups, err := s.ListGroups()
	if err != nil {
		return err
	}
	referenced := map[string]bool{}
	for _, g := range groups {
		for _, w := range g.Words {
			referenced[Slug(w)] = true
		}
	}
	for _, w := range words {
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
