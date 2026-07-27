package llm

import (
	"fmt"
	"strings"

	"phonicsfun/internal/store"
)

// --- 卡片文本生成 ---

// cardPromptHeader 由三部分组成：中文任务指令、英文拆解规则块（规则体系
// 依据 UFLI / Reading Rockets / Orton-Gillingham / Gates 2008，剔除了
// Clymer 1963 证伪的不可靠规则）、few-shot 示例。硬不变量与 store.Card.Validate
// 一一对应：音节拼接==单词、音节内字素拼接==音节、digraph 不可拆、blend 必拆。
const cardPromptHeader = `你是儿童自然拼读（phonics）教学内容生成器。给定一个英文单词，输出 JSON 单词卡：
- ipa：美式发音 IPA，带斜杠和重音符。
- senses：按词性组织的释义，1~3 条。若下方给出权威词性参照，按参照顺序取儿童最常用的至多 3 个词性；pos 只能用 n. v. adj. adv. pron. prep. conj. int. num. art. 这些缩写；zh 是不超过 12 字的儿童友好中文释义；en 是不超过 10 个词的简单英文释义。
- examples：1~2 条例句。英文句不超过 10 个词、只用小孩认识的简单词、必须包含这个单词或其自然变形；zh 是自然的中文翻译。
- syllables：两级拼读拆解（音节 → 音节内字素-音素块），严格遵守下面的规则块；所有 text 和 grapheme 全小写。

PHONICS DECOMPOSITION RULES (follow strictly)

STEP 1 - Morphemes first. Before any other division, cut at compound-word
boundaries (cup|cake), after real prefixes (un re de dis mis pre pro con com
ex sub non over under), and before real suffixes (s es ed ing er est ly less
ful ness ment y ish al ic ive able ible ous tion sion ture). Never cut inside
a morpheme; "un" in "uncle" or "re" in "rest" is NOT a prefix.

STEP 2 - Divide the rest into syllables; first matching rule wins:
R1 C+le ending: the consonant + le is the final syllable (can|dle, ta|ble).
R2 Final stable ending (tion sion cian cious tious tial cial ture sure):
   that unit is the final syllable (ac|tion, pic|ture).
R3 VCCV: divide between the two consonants (rab|bit, nap|kin). NEVER split a
   digraph/trigraph (sh ch th ph wh ck ng tch dge) — divide around it.
R4 VCCCV: keep digraphs and 3-letter blends (str spr scr spl shr thr) intact
   at the start of the second syllable (mon|ster, com|plete).
R5 VCV: try open first — divide before the consonant, first vowel long
   (o|pen, ti|ger). If that is not the real pronunciation, divide after the
   consonant, vowel short (sev|en, cab|in).
R6 VV: a vowel team stays in one syllable; otherwise divide between the
   vowels (po|et, li|on, qui|et).
Every syllable contains exactly one vowel sound.

STEP 3 - Chunks inside each syllable, left to right, longest match first.
NEVER split: consonant digraphs/trigraphs (sh ch th ph wh ck ng nk kn wr mb
qu tch dge igh), vowel teams (ai ay ee ea ey oa ow oe ie oo ew ue ui au aw oi
oy ou ei eigh ough augh), r-controlled units (ar or ore er ir ur air are ear
eer ure), welded endings kept as ONE chunk because the vowel sound changes
(all oll ull am an ang ing ong ung ank ink onk unk), long-vowel exceptions
(ild ind old olt ost), final stable units (tion sion cian cious tious tial
cial ture sure), and C+le (dle ble gle kle ple tle fle zle as one chunk).
ALWAYS split consonant blends into single-letter chunks: s|t|op, p|l|ay,
s|t|r|ap (in thr/shr the th/sh stays whole: th|r). Do NOT rely on "when two
vowels go walking" — ea/ie/oo/ou/ow have multiple values; pick the value
that yields the real word.
Silent letters: a letter with no sound of its own (magic-e 等) is its OWN
chunk with silent=true and empty phoneme/respell/anchor_word. Letters inside
kn/wr/mb stay in the digraph chunk. A cluster read as one sound because one
letter is silent may be one chunk (the "st" in listen = /s/).

STEP 4 - Respell（这是要喂给语音引擎朗读的，绝不能是 IPA）:
- chunk.respell = SPELLING VOICE：该音单独、清晰、不弱读的儿童可读注音
  （a→"a", i→"ih", tion→"shun", igh→"eye", k→"k"）。
- syllable.respell = 该音节在整词里的真实读音，非重读音节要体现 schwa 弱读
  （replication 的 li → "luh"，ca → "kay"）。
- anchor_word：一个常见简单词，其中同样的字素发同样的音（sh→shoe,
  igh→night, ar→car, tion→station）。

更多切分示例（音节用 - 分隔，音节内 chunk 用 , 分隔）：
tiger → ti-ger：t,i - g,er（V/CV 开音节，i 读长音 /aɪ/）
seven → sev-en：s,e,v - e,n（开音节不成词，退 VC/V，e 读短音）
king → king：k,ing（welded -ing 整块，不拆 i 和 ng）
candle → can-dle：c,a,n - dle（C+le 音节整块，读 "dul"）
poet → po-et：p,o - e,t（oe 在此不是 vowel team，元音间切开）
rabbit → rab-bit：r,a,b - b,i,t（VCCV 在双辅音间切）

输出示例：
ship → {"word":"ship","ipa":"/ʃɪp/","senses":[{"pos":"n.","zh":"船","en":"a large boat"}],"examples":[{"en":"The ship is on the sea.","zh":"船在海上。"}],"syllables":[{"text":"ship","respell":"ship","chunks":[{"grapheme":"sh","phoneme":"/ʃ/","respell":"sh","anchor_word":"shoe","silent":false},{"grapheme":"i","phoneme":"/ɪ/","respell":"ih","anchor_word":"sit","silent":false},{"grapheme":"p","phoneme":"/p/","respell":"p","anchor_word":"pig","silent":false}]}]}
cake → {"word":"cake","ipa":"/keɪk/","senses":[{"pos":"n.","zh":"蛋糕","en":"a sweet baked food"}],"examples":[{"en":"Mom made a big cake.","zh":"妈妈做了一个大蛋糕。"}],"syllables":[{"text":"cake","respell":"kayk","chunks":[{"grapheme":"c","phoneme":"/k/","respell":"k","anchor_word":"kite","silent":false},{"grapheme":"a","phoneme":"/eɪ/","respell":"ay","anchor_word":"name","silent":false},{"grapheme":"k","phoneme":"/k/","respell":"k","anchor_word":"kite","silent":false},{"grapheme":"e","phoneme":"","respell":"","anchor_word":"","silent":true}]}]}
light → {"word":"light","ipa":"/laɪt/","senses":[{"pos":"n.","zh":"光；灯","en":"brightness that lets us see"},{"pos":"adj.","zh":"轻的","en":"not heavy"}],"examples":[{"en":"Turn on the light, please.","zh":"请把灯打开。"},{"en":"The box is very light.","zh":"这个箱子很轻。"}],"syllables":[{"text":"light","respell":"lite","chunks":[{"grapheme":"l","phoneme":"/l/","respell":"l","anchor_word":"leg","silent":false},{"grapheme":"igh","phoneme":"/aɪ/","respell":"eye","anchor_word":"night","silent":false},{"grapheme":"t","phoneme":"/t/","respell":"t","anchor_word":"top","silent":false}]}]}
action → {"word":"action","ipa":"/ˈækʃən/","senses":[{"pos":"n.","zh":"行动；动作","en":"something you do"}],"examples":[{"en":"Let's take action now.","zh":"我们现在就行动吧。"}],"syllables":[{"text":"ac","respell":"ak","chunks":[{"grapheme":"a","phoneme":"/æ/","respell":"a","anchor_word":"apple","silent":false},{"grapheme":"c","phoneme":"/k/","respell":"k","anchor_word":"cat","silent":false}]},{"text":"tion","respell":"shun","chunks":[{"grapheme":"tion","phoneme":"/ʃən/","respell":"shun","anchor_word":"station","silent":false}]}]}`

// cardRefs 是逐词注入 prompt 的权威参照（缺项为零值即不注入对应行）。
type cardRefs struct {
	ARPAbet  string // CMUdict 音素串
	SylCount int    // 由 ARPAbet 数出的读音音节数
	Hyph     string // Moby 音节切分（或 "词干切分 + -后缀"）
	POS      string // ECDICT 词性缩写串，如 "n.,v."
}

func buildCardPrompt(word string, refs cardRefs) string {
	var b strings.Builder
	b.WriteString(cardPromptHeader)
	var lines []string
	if refs.ARPAbet != "" {
		lines = append(lines, fmt.Sprintf("- CMUdict 读音（ARPAbet，美音）：%s: %s（音素后的数字是重音标记）", word, refs.ARPAbet))
	}
	if refs.SylCount > 0 {
		lines = append(lines, fmt.Sprintf("- 读音音节数：%d。syllables 数组通常应恰好 %d 项；仅当拼写音节确实多于读音音节时（如 chocolate）才可多。", refs.SylCount, refs.SylCount))
	}
	if refs.Hyph != "" {
		lines = append(lines, fmt.Sprintf("- 音节切分参照（Moby 词典）：%s。这是参照不是命令：若某个边界与 CMUdict 元音矛盾（参照给出闭音节但 CMUdict 是长元音），按开音节修正。", refs.Hyph))
	}
	if refs.POS != "" {
		lines = append(lines, fmt.Sprintf("- 词性参照（按常用序）：%s。senses 从中取前面最多 3 个。", refs.POS))
	}
	if len(lines) > 0 {
		b.WriteString("\n\n权威参照（缺少的项按规则自行判断）：\n")
		b.WriteString(strings.Join(lines, "\n"))
	}
	fmt.Fprintf(&b, "\n\n现在处理单词：%s", word)
	return b.String()
}

// buildCardRetryPrompt 在首次生成校验失败后附加失败原因重试。
func buildCardRetryPrompt(word string, refs cardRefs, problem string) string {
	return buildCardPrompt(word, refs) +
		fmt.Sprintf("\n\n注意：上一次生成失败，原因是「%s」。请检查：所有音节 text 依序拼接必须精确等于 %q；每个音节内 chunk 的 grapheme 依序拼接必须精确等于该音节的 text；digraph 不可拆开；blend 必须逐字母拆。", problem, strings.ToLower(word))
}

// --- 单词提取 ---

const extractTextPrompt = `从下面的原始文本中提取所有英文单词，输出 JSON。要求：全部转小写、去重、去掉标点和数字、忽略非英语内容和单个字母（"a"、"I" 这类冠词/代词除外——它们也是词，保留）、保持首次出现的顺序。

<text>
%s
</text>`

const extractImagePrompt = `提取这张图片中出现的所有英文单词（印刷体或手写均可），输出 JSON。要求：全部转小写、去重、忽略标点数字和非英语内容、按图中出现顺序排列。看不清的词宁可跳过也不要猜。`

// --- Live 音频 ---

// liveSystemInstruction 每个会话设置一次。多词复用会话时的关键约束：
// 禁止引用之前的轮次、禁止寒暄；条目之间的停顿必须是真正的静音——
// 后处理靠静音间隙切分音频，这条是拼读时间标注的生命线。
const liveSystemInstruction = `You are a text-to-speech engine for a children's phonics app. Each user turn gives you a reading script. Read it aloud exactly as instructed, in a warm, slow, very clear voice with a standard American accent, as if teaching a young child. Say ONLY the quoted text; text marked as hints is for your pronunciation reference only — never read hints aloud. When the script asks for pauses between items, make each pause a full second of complete silence. Never greet, never explain, never translate, never comment, and never refer to any previous turn.`

// BlendKind 标记拼读脚本行的类型，与 store.Cue.Kind 一致。
type BlendKind string

const (
	BlendChunk    BlendKind = "chunk"    // 一个字素-音素块（spelling voice）
	BlendSyllable BlendKind = "syllable" // 合成一个音节（真实读音）
	BlendTail     BlendKind = "tail"     // 收尾：音节连读 + 整词
)

// BlendLine 是拼读音频的一个朗读条目。Syllable/Chunk 是卡片里的下标
// （Chunk 为音节内含 silent 的下标），不适用时为 -1。单 chunk 音节
// （如 tion）不出 chunk 行，其 syllable 行的 Chunk 指向那个唯一的
// chunk——生成 cues 时该段同时充当 chunk 段和 syllable 段。
type BlendLine struct {
	Kind     BlendKind
	Syllable int
	Chunk    int
	Say      string // 朗读内容（respell）
	Anchor   string // chunk 行的发音提示词（不朗读）
}

// BuildBlendLines 从卡片机械拼装拼读条目序列，最后一条恒为 tail。
// 音频侧按"每条目一段"切分音频并生成时间标注，条目序列的顺序和数量
// 就是 blend.wav 的分段契约。
func BuildBlendLines(card *store.Card) []BlendLine {
	multi := len(card.Syllables) > 1
	var lines []BlendLine
	for si, syl := range card.Syllables {
		voiced := make([]int, 0, len(syl.Chunks))
		for ci, ch := range syl.Chunks {
			if !ch.Silent {
				voiced = append(voiced, ci)
			}
		}
		lone := multi && len(voiced) == 1
		if !lone {
			for _, ci := range voiced {
				ch := syl.Chunks[ci]
				lines = append(lines, BlendLine{Kind: BlendChunk, Syllable: si, Chunk: ci, Say: ch.Respell, Anchor: ch.AnchorWord})
			}
		}
		if multi {
			chunkIdx := -1
			if lone {
				chunkIdx = voiced[0]
			}
			lines = append(lines, BlendLine{Kind: BlendSyllable, Syllable: si, Chunk: chunkIdx, Say: syl.Respell})
		}
	}
	return append(lines, BlendLine{Kind: BlendTail, Syllable: -1, Chunk: -1})
}

// BlendBodyScript 拼装主体轮脚本（除 tail 外的全部条目，一轮连续朗读，
// 条目间停顿约一秒——停顿是后处理切分的依据，最终会被固定间隔替换）。
func BlendBodyScript(lines []BlendLine) string {
	var b strings.Builder
	b.WriteString("Read this phonics list aloud, item by item, in order. Say ONLY the text inside quotes, never the hints. After each item, stay completely silent for about one second before the next:\n")
	for _, ln := range lines {
		if ln.Kind == BlendTail {
			continue
		}
		hint := "one whole syllable"
		if ln.Kind == BlendChunk {
			hint = fmt.Sprintf("the sound in %q", ln.Anchor)
		}
		fmt.Fprintf(&b, "- %q (hint: %s)\n", ln.Say, hint)
	}
	return strings.TrimRight(b.String(), "\n")
}

// BlendTailScript 拼装收尾轮脚本：多音节 = 逐音节连读再说整词；
// 单音节 = 只说整词。
func BlendTailScript(card *store.Card) string {
	word := strings.ToLower(card.Word)
	if len(card.Syllables) <= 1 {
		return fmt.Sprintf("Say only this one word, slowly and clearly: %q. Say nothing else.", word)
	}
	quoted := make([]string, len(card.Syllables))
	for i, s := range card.Syllables {
		quoted[i] = fmt.Sprintf("%q", s.Respell)
	}
	return fmt.Sprintf("First say the syllables one by one with a short pause between them: %s. Then say the whole word once at natural speed: %q. Say nothing else.",
		strings.Join(quoted, ", "), word)
}

// BuildWordScript 整词轮：慢速一遍 + 常速一遍，便于跟读。
func BuildWordScript(word string) string {
	return fmt.Sprintf("Say only this one word, twice: first slowly and clearly, then at natural speed: %q.", strings.ToLower(word))
}
