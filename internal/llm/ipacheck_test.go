package llm

import (
	"strings"
	"testing"

	"phonicsfun/internal/store"
)

// ipaCard 构造只带 IPA 相关字段的卡（checkIPAConsistency 不看其余字段）。
// phonemes 与 graphemes 一一对应，空 phoneme 表示 silent chunk。
func ipaCard(word, ipa string, graphemes, phonemes []string) *store.Card {
	syl := store.Syllable{Text: word}
	for i, g := range graphemes {
		syl.Chunks = append(syl.Chunks, store.Chunk{
			Grapheme: g, Phoneme: phonemes[i], Silent: phonemes[i] == "",
		})
	}
	return &store.Card{Word: word, IPA: ipa, Syllables: []store.Syllable{syl}}
}

func TestCheckIPAConsistency(t *testing.T) {
	// 真实翻车案例：整词 ipa 用 Longman 风格的 /ɪ/，chunk 却按 CMUdict
	// 标 /ə/（anchor pencil）——两处各引一派词典，卡内自相矛盾
	bad := ipaCard("replication", "/ˌrɛplɪˈkeɪʃən/",
		[]string{"r", "e", "p", "l", "i", "c", "a", "tion"},
		[]string{"/r/", "/ɛ/", "/p/", "/l/", "/ə/", "/k/", "/eɪ/", "/ʃən/"})
	err := checkIPAConsistency(bad)
	if err == nil {
		t.Fatal("ə/ɪ 不一致的卡应被拒")
	}
	// 错误信息要带 chunk 拼接的原始形态，供重试 prompt 定位差异
	if !strings.Contains(err.Error(), "rɛpləkeɪʃən") || !strings.Contains(err.Error(), "/ˌrɛplɪˈkeɪʃən/") {
		t.Errorf("错误信息应含两处的具体音标: %v", err)
	}

	// chunk 与整词统一成 /ɪ/ 后通过；重音符、音节点不算差异
	good := ipaCard("replication", "/ˌrɛp.lɪˈkeɪ.ʃən/",
		[]string{"r", "e", "p", "l", "i", "c", "a", "tion"},
		[]string{"/r/", "/ɛ/", "/p/", "/l/", "/ɪ/", "/k/", "/eɪ/", "/ʃən/"})
	if err := checkIPAConsistency(good); err != nil {
		t.Errorf("一致的卡被误拒: %v", err)
	}

	// 同音异写折叠：ɡ(U+0261)/g、ɹ/r、成音节 l̩/əl、e/ɛ 属同一读音
	folds := ipaCard("table", "/ˈteɪbl̩/",
		[]string{"t", "a", "b", "le"},
		[]string{"/t/", "/eɪ/", "/b/", "/əl/"})
	if err := checkIPAConsistency(folds); err != nil {
		t.Errorf("成音节辅音异写被误拒: %v", err)
	}
	grass := ipaCard("grass", "/ɡɹæs/",
		[]string{"g", "r", "a", "ss"},
		[]string{"/g/", "/r/", "/æ/", "/s/"})
	if err := checkIPAConsistency(grass); err != nil {
		t.Errorf("ɡ/ɹ 异写被误拒: %v", err)
	}

	// silent chunk 不参与拼接（magic-e）
	cake := ipaCard("cake", "/keɪk/",
		[]string{"c", "a", "k", "e"},
		[]string{"/k/", "/eɪ/", "/k/", ""})
	if err := checkIPAConsistency(cake); err != nil {
		t.Errorf("silent chunk 卡被误拒: %v", err)
	}

	// 双写辅音：hel|lo 的两个 l 各成 chunk、各标 /l/，整词 ipa 只有一个 l
	hello := ipaCard("hello", "/həˈloʊ/",
		[]string{"h", "e", "l", "l", "o"},
		[]string{"/h/", "/ə/", "/l/", "/l/", "/oʊ/"})
	if err := checkIPAConsistency(hello); err != nil {
		t.Errorf("双写辅音卡被误拒: %v", err)
	}
	rabbit := ipaCard("rabbit", "/ˈræbɪt/",
		[]string{"r", "a", "b", "b", "i", "t"},
		[]string{"/r/", "/æ/", "/b/", "/b/", "/ɪ/", "/t/"})
	if err := checkIPAConsistency(rabbit); err != nil {
		t.Errorf("双写辅音卡被误拒: %v", err)
	}

	// 折叠不放过真差异
	wrong := ipaCard("cat", "/kæt/",
		[]string{"c", "a", "t"},
		[]string{"/k/", "/ɪ/", "/t/"})
	if err := checkIPAConsistency(wrong); err == nil {
		t.Error("音素真不一致的卡应被拒")
	}
}

// respellCard 构造单音节卡：phonemes/respells 与 graphemes 一一对应。
func respellCard(sylText, sylRespell string, graphemes, phonemes, respells []string) *store.Card {
	syl := store.Syllable{Text: sylText, Respell: sylRespell}
	for i, g := range graphemes {
		syl.Chunks = append(syl.Chunks, store.Chunk{
			Grapheme: g, Phoneme: phonemes[i], Respell: respells[i], Silent: phonemes[i] == "",
		})
	}
	return &store.Card{Word: sylText, Syllables: []store.Syllable{syl}}
}

func TestCheckRespellConsistency(t *testing.T) {
	// 真实翻车案例：li 音节 phoneme 已统一成 /ɪ/，syllable.respell 却还是
	// schwa 派的 "luh"——音节段朗读用它，读出来就是错音
	bad := respellCard("li", "luh",
		[]string{"l", "i"}, []string{"/l/", "/ɪ/"}, []string{"l", "ih"})
	if err := checkRespellConsistency(bad); err == nil {
		t.Fatal("/lɪ/ 配 respell \"luh\" 应被拒")
	}

	// 修正版 "lih" 通过；schwa 版整体一致也通过
	if err := checkRespellConsistency(respellCard("li", "lih",
		[]string{"l", "i"}, []string{"/l/", "/ɪ/"}, []string{"l", "ih"})); err != nil {
		t.Errorf("/lɪ/ 配 \"lih\" 被误拒: %v", err)
	}
	if err := checkRespellConsistency(respellCard("li", "luh",
		[]string{"l", "i"}, []string{"/l/", "/ə/"}, []string{"l", "uh"})); err != nil {
		t.Errorf("/lə/ 配 \"luh\" 被误拒: %v", err)
	}

	// 反向：/ə/ 音节配 /ɪ/ 拼法 "ih" 同样拒
	if err := checkRespellConsistency(respellCard("li", "lih",
		[]string{"l", "i"}, []string{"/l/", "/ə/"}, []string{"l", "uh"})); err == nil {
		t.Error("/lə/ 配 respell \"lih\" 应被拒")
	}

	// eɪ 里的 ɪ 不算弱读混淆；ʃən 的 "shun"（ə 省作 u）合法
	if err := checkRespellConsistency(respellCard("ca", "kay",
		[]string{"c", "a"}, []string{"/k/", "/eɪ/"}, []string{"k", "ay"})); err != nil {
		t.Errorf("/keɪ/ 配 \"kay\" 被误拒: %v", err)
	}
	if err := checkRespellConsistency(respellCard("tion", "shun",
		[]string{"tion"}, []string{"/ʃən/"}, []string{"shun"})); err != nil {
		t.Errorf("/ʃən/ 配 \"shun\" 被误拒: %v", err)
	}

	// chunk 层：/ɪ/ 的 chunk 注成 "uh" 也要拦
	if err := checkRespellConsistency(respellCard("li", "lih",
		[]string{"l", "i"}, []string{"/l/", "/ɪ/"}, []string{"l", "uh"})); err == nil {
		t.Error("chunk /ɪ/ 配 respell \"uh\" 应被拒")
	}
	// chunk 层反向不查：弱读 /ə/ 的 spelling voice 读字母本音 "ih" 合法
	if err := checkRespellConsistency(respellCard("li", "luh",
		[]string{"l", "i"}, []string{"/l/", "/ə/"}, []string{"l", "ih"})); err != nil {
		t.Errorf("chunk /ə/ 配 spelling voice \"ih\" 被误拒: %v", err)
	}
}
