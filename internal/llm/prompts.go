package llm

import (
	"fmt"
	"strings"

	"phonicsfun/internal/store"
)

// --- 卡片文本生成 ---

const cardPromptHeader = `你是儿童自然拼读（phonics）教学内容生成器。给定一个英文单词，输出 JSON：美式发音 IPA、儿童友好的释义（definition_zh 为中文且不超过 15 字；definition_en 为英文且不超过 10 个词）、拼读拆解 chunks。

拆解规则：
1. 所有 chunk 的 grapheme 按顺序拼接必须精确等于这个单词（全小写，一个字母不多不少）。
2. 按教学音素单位拆分：辅音组合（sh/ch/th/ck/ph/wh）、r-controlled 元音（ar/or/er/ir/ur）、元音组合（ai/ay/ee/ea/oa/oo/ou/ow/oy/oi/igh）必须作为一个整体 chunk，不可拆开。
3. 不发音的字母（如词尾 magic-e 的 e）单独成 chunk：silent 为 true，phoneme、respell、anchor_word 为空字符串。其余 chunk 的 silent 为 false。
4. respell 是英语母语儿童能直接读出来的注音（如 wuh、sh、ay、k），不是 IPA。
5. anchor_word 是一个清晰包含该音的常见简单英文单词。
6. phoneme 用 IPA 音素并带斜杠，如 /ʃ/。

示例：
cat → {"word":"cat","ipa":"/kæt/","definition_zh":"猫","definition_en":"a small furry pet animal","chunks":[{"grapheme":"c","phoneme":"/k/","respell":"k","anchor_word":"kite","silent":false},{"grapheme":"a","phoneme":"/æ/","respell":"a","anchor_word":"apple","silent":false},{"grapheme":"t","phoneme":"/t/","respell":"t","anchor_word":"top","silent":false}]}
ship → {"word":"ship","ipa":"/ʃɪp/","definition_zh":"船","definition_en":"a large boat","chunks":[{"grapheme":"sh","phoneme":"/ʃ/","respell":"sh","anchor_word":"shoe","silent":false},{"grapheme":"i","phoneme":"/ɪ/","respell":"i","anchor_word":"sit","silent":false},{"grapheme":"p","phoneme":"/p/","respell":"p","anchor_word":"pig","silent":false}]}
word → {"word":"word","ipa":"/wɝːd/","definition_zh":"单词；词语","definition_en":"a unit of language","chunks":[{"grapheme":"w","phoneme":"/w/","respell":"wuh","anchor_word":"wet","silent":false},{"grapheme":"or","phoneme":"/ɝː/","respell":"er","anchor_word":"her","silent":false},{"grapheme":"d","phoneme":"/d/","respell":"duh","anchor_word":"dog","silent":false}]}
cake → {"word":"cake","ipa":"/keɪk/","definition_zh":"蛋糕","definition_en":"a sweet baked food","chunks":[{"grapheme":"c","phoneme":"/k/","respell":"k","anchor_word":"kite","silent":false},{"grapheme":"a","phoneme":"/eɪ/","respell":"ay","anchor_word":"name","silent":false},{"grapheme":"k","phoneme":"/k/","respell":"k","anchor_word":"kite","silent":false},{"grapheme":"e","phoneme":"","respell":"","anchor_word":"","silent":true}]}
rain → {"word":"rain","ipa":"/reɪn/","definition_zh":"雨","definition_en":"water falling from clouds","chunks":[{"grapheme":"r","phoneme":"/r/","respell":"ruh","anchor_word":"run","silent":false},{"grapheme":"ai","phoneme":"/eɪ/","respell":"ay","anchor_word":"play","silent":false},{"grapheme":"n","phoneme":"/n/","respell":"n","anchor_word":"net","silent":false}]}`

func buildCardPrompt(word, arpabet string) string {
	var b strings.Builder
	b.WriteString(cardPromptHeader)
	if arpabet != "" {
		fmt.Fprintf(&b, "\n\n权威发音参照（CMUdict ARPAbet，美音）：%s: %s\n请以该音素序列为准进行 IPA 转写和 chunk 对齐（音素后的数字是重音标记，可忽略）。", word, arpabet)
	}
	fmt.Fprintf(&b, "\n\n现在处理单词：%s", word)
	return b.String()
}

// retryFeedback 在首次生成校验失败后附加到重试 prompt。
func buildCardRetryPrompt(word, arpabet, problem string) string {
	return buildCardPrompt(word, arpabet) +
		fmt.Sprintf("\n\n注意：上一次生成失败，原因是「%s」。请特别检查规则 1：grapheme 依序拼接必须精确等于 %q。", problem, strings.ToLower(word))
}

// --- 单词提取 ---

const extractTextPrompt = `从下面的原始文本中提取所有英文单词，输出 JSON。要求：全部转小写、去重、去掉标点和数字、忽略非英语内容和单个字母（"a"、"I" 这类冠词/代词除外——它们也是词，保留）、保持首次出现的顺序。

<text>
%s
</text>`

const extractImagePrompt = `提取这张图片中出现的所有英文单词（印刷体或手写均可），输出 JSON。要求：全部转小写、去重、忽略标点数字和非英语内容、按图中出现顺序排列。看不清的词宁可跳过也不要猜。`

// --- Live 音频 ---

// liveSystemInstruction 每个会话设置一次。多词复用会话时的关键约束：
// 禁止引用之前的轮次、禁止寒暄，否则多轮之后输出会开始漂移。
const liveSystemInstruction = `You are a text-to-speech engine for a children's phonics app. Each user turn gives you a reading script. Read it aloud exactly as instructed, in a warm, slow, very clear voice with a standard American accent, as if teaching a young child. Never greet, never explain, never translate, never comment, and never refer to any previous turn. Words marked as hints are for your pronunciation reference only — never read them aloud. Speak only what the script requires.`

// BuildBlendScript 从卡片机械拼装拼读轮脚本：逐个非 silent chunk 的
// respelling（锚定词只作发音提示，不朗读），最后是整词。
func BuildBlendScript(card *store.Card) string {
	var b strings.Builder
	b.WriteString("Say each numbered sound one at a time, slowly, pausing about one second between lines. Say ONLY the quoted sound, never the hint:\n")
	n := 0
	for _, ch := range card.Chunks {
		if ch.Silent {
			continue
		}
		n++
		fmt.Fprintf(&b, "%d. \"%s\" (hint: the sound in \"%s\")\n", n, ch.Respell, ch.AnchorWord)
	}
	fmt.Fprintf(&b, "Finally, say the whole word: \"%s\".", strings.ToLower(card.Word))
	return b.String()
}

// BuildWordScript 整词轮：慢速一遍 + 常速一遍，便于跟读。
func BuildWordScript(word string) string {
	return fmt.Sprintf("Say only this one word, twice: first slowly and clearly, then at natural speed: \"%s\".", strings.ToLower(word))
}
