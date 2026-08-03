// Package config loads server configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	APIKey    string // GEMINI_API_KEY，必填
	Port      int    // PORT，默认 8080
	DataDir   string // DATA_DIR，默认 ./data
	TextModel string // TEXT_MODEL，默认 gemini-3.5-flash-lite
	LiveModel string // LIVE_MODEL，默认 gemini-3.1-flash-live-preview
	// TeacherModel 是 AI 老师对话会话用的 Live 模型。TEACHER_MODEL，
	// 默认跟随 LiveModel——preview 模型改版时只需改 env，不写死代码。
	TeacherModel string
	Voice        string // VOICE，默认 Kore
	RPM          int    // RPM，默认 12（免费层按 15 RPM 假设留余量）
}

func Load() (*Config, error) {
	cfg := &Config{
		APIKey:    os.Getenv("GEMINI_API_KEY"),
		Port:      8080,
		DataDir:   "./data",
		TextModel: "gemini-3.5-flash-lite",
		LiveModel: "gemini-3.1-flash-live-preview",
		Voice:     "Kore",
		RPM:       12,
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY 未设置")
	}
	if v := os.Getenv("PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p <= 0 || p > 65535 {
			return nil, fmt.Errorf("PORT 无效: %q", v)
		}
		cfg.Port = p
	}
	if v := os.Getenv("DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("TEXT_MODEL"); v != "" {
		cfg.TextModel = v
	}
	if v := os.Getenv("LIVE_MODEL"); v != "" {
		cfg.LiveModel = v
	}
	cfg.TeacherModel = cfg.LiveModel
	if v := os.Getenv("TEACHER_MODEL"); v != "" {
		cfg.TeacherModel = v
	}
	if v := os.Getenv("VOICE"); v != "" {
		cfg.Voice = v
	}
	if v := os.Getenv("RPM"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("RPM 无效: %q", v)
		}
		cfg.RPM = n
	}
	return cfg, nil
}
