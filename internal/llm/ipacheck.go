package llm

import (
	"fmt"
	"strings"

	"phonicsfun/internal/store"
)

// ipaFold 把一段 IPA 折叠成可机械比较的音素序列：去掉斜杠、重音符、音节点
// 等不改变读音的记号，并把常见的同音异写（词典风格差异）折叠到同一形态。
// 折叠同时作用于比较的两侧，所以只需保证"同音异写 → 同形态"，不要求折叠
// 结果本身仍是规范 IPA。多字符条目必须排在可能与其重叠的单字符条目之前
// （Replacer 在每个位置按参数顺序取第一个匹配）。
var ipaFold = strings.NewReplacer(
	"n̩", "ən", "l̩", "əl", "m̩", "əm", // 成音节辅音（含 U+0329）与 ə+辅音 等价
	"t̬", "t", "ɾ", "t", // flap t 的两种写法折回 t
	"oʊ", "o", "əʊ", "o", // GOAT 元音的美式/英式/简写形态
	"ɚ", "ər", "ɝ", "ər", // r-colored schwa
	"ʤ", "dʒ", "ʧ", "tʃ", // 合字变体
	"ɡ", "g", "ɹ", "r", "ɫ", "l",
	"ɜ", "ə", "ᵻ", "ɪ", "e", "ɛ", "ɒ", "ɑ",
	"˞", "r",
	"/", "", "[", "", "]", "", "ˈ", "", "ˌ", "", "'", "",
	".", "", "ː", "", "‿", "", " ", "", "-", "", "(", "", ")", "",
)

// squashDoubles 把串里相邻的重复字符压成一个。双写辅音字母（hello 的
// ll、rabbit 的 bb）按 VCCV 切分分属两个音节、各成一个 chunk 且各标同一
// 音素——拼读教学上两个音节都要读这个音——但整词 IPA 只写一个辅音，
// 直接拼接必然多一个字符。比较的两侧都做此压缩即可对齐；英语 IPA 里
// 相邻重复字符只会来自双写辅音（含 mission 类 ss→ʃʃ 的标法），不存在
// 需要区分的真双读，压缩不会放过真错误。
func squashDoubles(s string) string {
	var b strings.Builder
	var prev rune
	for i, r := range s {
		if i > 0 && r == prev {
			continue
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}

// checkIPAConsistency 校验整词 ipa 与逐 chunk phoneme 描述同一读音：
// 非 silent chunk 的 phoneme 依序拼接，折叠后必须与整词 ipa 折叠结果相等
// （折叠 = ipaFold 同音异写归一 + squashDoubles 双写辅音压缩）。
// 拦的是"两处各引一派词典"的卡（如整词 /ˌrɛplɪˈkeɪʃən/ 配 chunk /ə/）——
// 孤立看各自成立，同卡并存就是教学矛盾。不一致的返回错误会作为
// validationError 注入重试 prompt，所以措辞面向模型、给出两串具体形态。
func checkIPAConsistency(c *store.Card) error {
	var raw strings.Builder
	for _, syl := range c.Syllables {
		for _, ch := range syl.Chunks {
			if !ch.Silent {
				raw.WriteString(strings.Trim(ch.Phoneme, "/"))
			}
		}
	}
	// 判定用折叠形态；报错给原始形态（折叠串是内部产物，模型读不懂）。
	got := squashDoubles(ipaFold.Replace(raw.String()))
	want := squashDoubles(ipaFold.Replace(c.IPA))
	if got != want {
		return fmt.Errorf("非 silent chunk 的 phoneme 依序拼接为 /%s/，与整词 ipa %s 不是同一读音——ipa 与 chunks 必须用同一套符号描述同一个发音，弱读元音（ə 还是 ɪ）两处要统一", raw.String(), c.IPA)
	}
	return nil
}

// checkRespellConsistency 窄校验 respell 的弱读元音拼法与 phoneme 一致。
// respell 是自由英文注音、没有严格正字法，无法全面机械校验；这里只盯
// ə/ɪ 这对最易混的弱读元音（生成两次翻车都在这）：音节读音（chunk
// phoneme 折叠拼接）含 ɪ 不含 ə 时，syllable.respell 不得含 schwa 拼法
// "uh"（如 /lɪ/ 配 "luh"——音节段朗读用它，读出来就是错音）；反向含 ə
// 不含 ɪ 时不得含 "ih"。chunk 层只查 /ɪ/ 配 "uh" 这一向——/ə/ 的
// spelling voice 允许读字母本音（弱读 i 注 "ih" 是合法教学法），反向不查。
func checkRespellConsistency(c *store.Card) error {
	for si, syl := range c.Syllables {
		var b strings.Builder
		for _, ch := range syl.Chunks {
			if !ch.Silent {
				b.WriteString(ipaFold.Replace(ch.Phoneme))
			}
		}
		ph := b.String()
		hasI, hasSchwa := strings.Contains(ph, "ɪ"), strings.Contains(ph, "ə")
		re := strings.ToLower(syl.Respell)
		if hasI && !hasSchwa && strings.Contains(re, "uh") {
			return fmt.Errorf("音节 %d (%q) 的读音是 /%s/（含 ɪ 无 ə），respell %q 却用了 schwa 拼法 \"uh\"——syllable.respell 必须转写该音节 phoneme 拼出的真实读音（ɪ→\"ih\"），或者把 phoneme 与整词 ipa 一起改成 ə", si, syl.Text, ph, syl.Respell)
		}
		if hasSchwa && !hasI && strings.Contains(re, "ih") {
			return fmt.Errorf("音节 %d (%q) 的读音是 /%s/（含 ə 无 ɪ），respell %q 却用了 /ɪ/ 的拼法 \"ih\"——syllable.respell 必须转写该音节 phoneme 拼出的真实读音（ə→\"uh\"），或者把 phoneme 与整词 ipa 一起改成 ɪ", si, syl.Text, ph, syl.Respell)
		}
		for _, ch := range syl.Chunks {
			if ch.Silent {
				continue
			}
			chPh := ipaFold.Replace(ch.Phoneme)
			if strings.Contains(chPh, "ɪ") && !strings.Contains(chPh, "ə") && strings.Contains(strings.ToLower(ch.Respell), "uh") {
				return fmt.Errorf("音节 %d 的 chunk %q phoneme 是 %s，respell %q 却是 schwa 拼法 \"uh\"——/ɪ/ 的注音用 \"ih\"", si, ch.Grapheme, ch.Phoneme, ch.Respell)
			}
		}
	}
	return nil
}
