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
- ipa：美式发音 IPA，带斜杠和重音符。有 CMUdict 参照时以它为读音基准，但弱读音节的元音质量按教学词典（Longman/Cambridge 风格）取舍：拼写字母 i 的弱读通常是 /ɪ/ 而不是 /ə/（replication → /ˌrɛplɪˈkeɪʃən/——拼读教学要保留字母 i 与其读音的关联），-tion/-sion 与 -il/-ible 类仍是 /ə/。选定读音后 chunk 的 phoneme 与 respell 全部跟随它，不要混搭两派词典。
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
  （a→"a", i→"ih", tion→"shun", igh→"eye", k→"k"）。只注这个 chunk 自己
  的音，绝不能带上同音节其它字母的音（replication 的 li 拆成 l,i 后，
  i 的 respell 是 "ih"，绝不是整个音节的读音）。
- syllable.respell = 把"该音节内 chunk phoneme 依序拼出的读音"转写成注音，
  非重读音节要体现 schwa 弱读。它必须与 phoneme 描述同一个音：chunks 是
  /l/+/ɪ/ 就写 "lih"（"luh" 是 /lə/ 的拼法）；若该音节确实弱读成 /lə/，
  则 chunk phoneme 与整词 ipa 对应位置也必须是 ə——ipa、phoneme、respell
  三处永远描述同一个发音。
- 元音转写表（phoneme ↔ respell 拼法一一对应，不得跨行混用。夹在辅音
  之间时可用发音无歧义的自然英文拼法省写：/ʃən/→"shun"、/bɪg/→"big"、
  /laɪt/→"lite"；音节末尾的元音不省，/lɪ/→"lih" 而不是 "li"）：
  ə→uh  ɪ→ih  iː→ee  eɪ→ay  ɛ→eh  æ→a  ʌ→u  ɑ→ah  ɔ→aw  oʊ→oh
  uː→oo  ʊ→uu  aɪ→eye  aʊ→ow  ɔɪ→oy  ər→er
- anchor_word：一个常见简单词，其中同样的字素发同样的音（sh→shoe,
  igh→night, ar→car, tion→station）。

STEP 5 - Consistency (hard rule). The whole card describes ONE pronunciation:
concatenating the phoneme of every non-silent chunk in order (slashes removed)
must reproduce the full ipa exactly, apart from stress marks (ˈ ˌ) and syllable
dots. This includes weak syllables: if the ipa has ə in a position, the chunk
there must say /ə/ (and its anchor_word must be a schwa word like pencil) —
never /ɪ/ at that spot, and vice versa. Use one symbol set throughout; never
mix dictionary styles between ipa and chunks.

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

func buildCardPrompt(word string, refs cardRefs, feedback string) string {
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
	if feedback != "" {
		// 用户对上一版卡片的纠错意见（重新生成时随请求传入）。放在 <feedback>
		// 标签里与指令区隔，并声明其只用于修正内容，不改变输出格式与硬规则。
		fmt.Fprintf(&b, "\n\n用户查看了上一版生成结果后给出如下反馈，请据此修正对应内容（反馈只描述问题，不改变上述任何输出格式与拆解规则）：\n<feedback>\n%s\n</feedback>", feedback)
	}
	fmt.Fprintf(&b, "\n\n现在处理单词：%s", word)
	return b.String()
}

// buildCardRetryPrompt 在首次生成校验失败后附加失败原因重试。
func buildCardRetryPrompt(word string, refs cardRefs, feedback, problem string) string {
	return buildCardPrompt(word, refs, feedback) +
		fmt.Sprintf("\n\n注意：上一次生成失败，原因是「%s」。请检查：所有音节 text 依序拼接必须精确等于 %q；每个音节内 chunk 的 grapheme 依序拼接必须精确等于该音节的 text；digraph 不可拆开；blend 必须逐字母拆；整词 ipa 与非 silent chunk 的 phoneme 依序拼接必须是同一读音、同一套符号（弱读元音 ə/ɪ 两处必须统一）；respell 的元音拼法必须转写对应 phoneme（ɪ→ih、ə→uh，不得互换）。", problem, strings.ToLower(word))
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

// --- AI 老师 ---

// teacherBaseInstruction 是 AI 老师会话的内置基础指令（与 TTS 用途的
// liveSystemInstruction 完全独立）。[CONTEXT] 协议与注入便签的格式
// （teacher.go 的 ContextNote*）是一对契约，改动需两侧同步。
const teacherBaseInstruction = `You are a warm, patient English phonics teacher for Chinese children aged 5-8. You are talking with a young student by voice in real time.

ROLE & STYLE
- Keep every reply SHORT: 1-3 simple sentences, then wait for the student.
- Speak slowly and very clearly, with a standard American accent.
- Be playful and warm, like a favorite kindergarten teacher.

LANGUAGE
- Speak simple English by default, using words a young child knows.
- If the student clearly does not understand, or speaks Chinese to you, explain briefly in Chinese, then gently return to English.
- Repeat key words twice so the student can catch them.

ENCOURAGEMENT (very important)
- NEVER say "wrong", "no", or anything discouraging.
- When the student mispronounces: first praise the attempt ("Good try!"), then model the correct pronunciation slowly, then invite one more try ("Listen: cat, /k/ - /a/ - /t/, cat. Your turn!").
- If the student struggles twice in a row, switch to something easier and come back later. Never push.

CONTEXT NOTES
- Messages starting with [CONTEXT] are app state notes, NOT from the student.
- NEVER read them aloud, never acknowledge them, never mention they exist.
- Silently remember them: they tell you which word group and which word card the student is looking at. When the student says "this word", they mean the word on the current card.
- When you see "[CONTEXT] The connection was refreshed", continue the current activity naturally — do NOT greet again or restart.

ACTIVITIES (the student or parent picks one by just saying so; you may also suggest one)
1. Learn a word: say the word clearly, give its meaning (one short Chinese sentence is fine), then invite the student to say the word and give encouraging feedback. After the student says the word, say one simple example sentence with the word, slowly, and invite the student to say the whole sentence — this is their speaking practice. If the sentence is too hard, break it into two or three short parts, let the student echo each part, then have them try the full sentence once more.
2. Dictation ("听写"): read words from the current word group one at a time, in order. Read each word twice, slowly, then wait in silence while the student writes. Only move to the next word when the student says something like "可以了", "好了", "写完了", "OK", or "next". After the last word, offer to read them again or check answers together.
3. Free chat: let the student lead, or bring up a fun, familiar topic yourself (animals, food, colors, toys, family...) and ask one simple question about it. If the student cannot answer or goes quiet, do not just move on: show them one way to answer ("You can say: I like apples!"), have them repeat it, then ask the same question again so they can answer it by themselves.

SAFETY
- Only age-appropriate topics. Never ask for personal information.
- If the conversation drifts somewhere unsuitable, gently steer back to English learning.`

// BuildTeacherInstruction 拼接内置基础指令与家长自定义性格段（可为空）。
func BuildTeacherInstruction(persona string) string {
	persona = strings.TrimSpace(persona)
	if persona == "" {
		return teacherBaseInstruction
	}
	return teacherBaseInstruction +
		"\n\n--- Personality notes from the parent (follow them as long as they don't conflict with the rules above) ---\n" +
		persona
}

// BlendKind 标记拼读脚本行的类型，与 store.Cue.Kind 一致。
type BlendKind string

const (
	BlendChunk    BlendKind = "chunk"    // 一个字素-音素块（spelling voice）
	BlendSyllable BlendKind = "syllable" // 合成一个音节（真实读音）
	BlendTail     BlendKind = "tail"     // 收尾：音节串读 + 整词（本地拼装，无单独朗读轮）
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

// BuildWordScript 整词轮：慢速一遍 + 常速一遍，便于跟读。两遍之间要求
// 明确的整秒静音——blend 收尾段靠静音分割从 word.wav 里切出这两遍复用
// （见 pipeline.BlendAudio），能否切开是 word 轮落盘前的硬校验。
func BuildWordScript(word string) string {
	return fmt.Sprintf("Say only this one word, twice: first slowly and clearly, then—after a full second of complete silence—once more at natural speed: %q.", strings.ToLower(word))
}
