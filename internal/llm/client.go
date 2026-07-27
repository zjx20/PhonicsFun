// Package llm wraps the Gemini API: card text generation and word extraction
// via generateContent, and blend/word audio via the Live API.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/time/rate"
	"google.golang.org/genai"

	"phonicsfun/internal/config"
	"phonicsfun/internal/store"
)

type Client struct {
	g         *genai.Client
	textModel string
	liveModel string
	voice     string
	// 免费层限额按请求数计。所有出站调用（generateContent 与 Live 会话
	// 建立）共用这一个 limiter；Live 会话内的轮次不占请求配额，不计。
	limiter *rate.Limiter
	dict    *cmudict
	hyph    *hyphdict
	pos     *posdict
}

func New(ctx context.Context, cfg *config.Config) (*Client, error) {
	g, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  cfg.APIKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("genai client: %w", err)
	}
	dict, err := loadCMUDict()
	if err != nil {
		return nil, err
	}
	hyph, err := loadHyphDict()
	if err != nil {
		return nil, err
	}
	pos, err := loadPOSDict()
	if err != nil {
		return nil, err
	}
	return &Client{
		g:         g,
		textModel: cfg.TextModel,
		liveModel: cfg.LiveModel,
		voice:     cfg.Voice,
		limiter:   rate.NewLimiter(rate.Limit(cfg.RPM)/60.0, 1),
		dict:      dict,
		hyph:      hyph,
		pos:       pos,
	}, nil
}

// IsRetryable 判断错误是否值得退避重试（限流或服务端瞬时错误）。
func IsRetryable(err error) bool {
	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case 429, 500, 502, 503, 504:
			return true
		}
		return false
	}
	// 网络层错误（连接重置等）也值得重试
	return err != nil && !errors.Is(err, context.Canceled)
}

var chunkSchema = &genai.Schema{
	Type: genai.TypeObject,
	Properties: map[string]*genai.Schema{
		"grapheme":    {Type: genai.TypeString},
		"phoneme":     {Type: genai.TypeString},
		"respell":     {Type: genai.TypeString},
		"anchor_word": {Type: genai.TypeString},
		"silent":      {Type: genai.TypeBoolean},
	},
	Required: []string{"grapheme", "phoneme", "respell", "anchor_word", "silent"},
}

var cardSchema = &genai.Schema{
	Type: genai.TypeObject,
	Properties: map[string]*genai.Schema{
		"word": {Type: genai.TypeString},
		"ipa":  {Type: genai.TypeString},
		"senses": {Type: genai.TypeArray, Items: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"pos": {Type: genai.TypeString},
				"zh":  {Type: genai.TypeString},
				"en":  {Type: genai.TypeString},
			},
			Required: []string{"pos", "zh", "en"},
		}},
		"examples": {Type: genai.TypeArray, Items: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"en": {Type: genai.TypeString},
				"zh": {Type: genai.TypeString},
			},
			Required: []string{"en", "zh"},
		}},
		"syllables": {Type: genai.TypeArray, Items: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"text":    {Type: genai.TypeString},
				"respell": {Type: genai.TypeString},
				"chunks":  {Type: genai.TypeArray, Items: chunkSchema},
			},
			Required: []string{"text", "respell", "chunks"},
		}},
	},
	Required: []string{"word", "ipa", "senses", "examples", "syllables"},
}

var wordsSchema = &genai.Schema{
	Type: genai.TypeObject,
	Properties: map[string]*genai.Schema{
		"words": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
	},
	Required: []string{"words"},
}

// GenerateCard 生成一张单词卡的文本内容。三路参照命中即注入 prompt
// （CMUdict 音素 + 音节数、Moby 音节切分、ECDICT 词性）；生成结果做硬校验
// （store.Card.Validate 全部不变量），失败带反馈重试一次。
func (c *Client) GenerateCard(ctx context.Context, word string) (*store.Card, error) {
	word = strings.ToLower(strings.TrimSpace(word))
	arpabet := c.dict.Lookup(word)
	refs := cardRefs{
		ARPAbet:  arpabet,
		SylCount: SyllableCount(arpabet),
		Hyph:     c.hyph.Ref(word),
		POS:      c.pos.Lookup(word),
	}

	card, err := c.generateCardOnce(ctx, word, buildCardPrompt(word, refs))
	if err == nil {
		return card, nil
	}
	var vErr *validationError
	if !errors.As(err, &vErr) {
		return nil, err // API 层错误交给上层退避重试
	}
	return c.generateCardOnce(ctx, word, buildCardRetryPrompt(word, refs, vErr.problem))
}

type validationError struct{ problem string }

func (e *validationError) Error() string { return "卡片校验失败: " + e.problem }

func (c *Client) generateCardOnce(ctx context.Context, word, prompt string) (*store.Card, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	resp, err := c.g.Models.GenerateContent(ctx, c.textModel, genai.Text(prompt), &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   cardSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("generateContent(%s): %w", word, err)
	}
	var card store.Card
	if err := json.Unmarshal([]byte(resp.Text()), &card); err != nil {
		return nil, &validationError{problem: "输出不是合法 JSON: " + err.Error()}
	}
	// 不信任 LLM 回显的 word 字段，以我们的输入为准
	card.Word = word
	card.Schema = store.CardSchemaVersion
	card.GeneratedAt = time.Now().UTC()
	card.Model = c.textModel
	if err := card.Validate(); err != nil {
		return nil, &validationError{problem: err.Error()}
	}
	return &card, nil
}

// ExtractWords 从杂乱文本提取英文单词列表。
func (c *Client) ExtractWords(ctx context.Context, text string) ([]string, error) {
	return c.extract(ctx, genai.Text(fmt.Sprintf(extractTextPrompt, text)))
}

// ExtractWordsFromImage 从图片提取英文单词列表。
func (c *Client) ExtractWordsFromImage(ctx context.Context, mimeType string, data []byte) ([]string, error) {
	content := genai.NewContentFromParts([]*genai.Part{
		genai.NewPartFromText(extractImagePrompt),
		genai.NewPartFromBytes(data, mimeType),
	}, genai.RoleUser)
	return c.extract(ctx, []*genai.Content{content})
}

func (c *Client) extract(ctx context.Context, contents []*genai.Content) ([]string, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	resp, err := c.g.Models.GenerateContent(ctx, c.textModel, contents, &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   wordsSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}
	var out struct {
		Words []string `json:"words"`
	}
	if err := json.Unmarshal([]byte(resp.Text()), &out); err != nil {
		return nil, fmt.Errorf("extract 输出解析: %w", err)
	}
	return normalizeWords(out.Words), nil
}

// normalizeWords 是对 LLM 输出的保险清洗：小写、只留字母和撇号、去空、
// 按 slug 去重保序。
func normalizeWords(words []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range words {
		var b strings.Builder
		for _, r := range strings.ToLower(strings.TrimSpace(w)) {
			if (r >= 'a' && r <= 'z') || r == '\'' {
				b.WriteRune(r)
			}
		}
		cleaned := strings.Trim(b.String(), "'")
		slug := store.Slug(cleaned)
		if cleaned == "" || slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, cleaned)
	}
	return out
}
