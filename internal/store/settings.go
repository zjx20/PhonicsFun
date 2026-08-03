package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// MaxTeacherPromptLen 限制家长自定义提示词长度（字符数，httpapi 与前端共用）。
const MaxTeacherPromptLen = 2000

// AI 老师 VAD 覆盖值的合法区间（毫秒；0 = 跟随内置默认，不在区间内）。
// httpapi 校验与前端 SettingsPage 的滑杆范围必须一致。
const (
	MinTeacherVADPrefixMs  = 20
	MaxTeacherVADPrefixMs  = 500
	MinTeacherVADSilenceMs = 200
	MaxTeacherVADSilenceMs = 2000
)

// Settings 是全局用户设置，存 DATA_DIR/settings.json。它是纯配置而非生成
// 状态，不参与"产物存在性即完成态"的判定；文件不存在等价于全部默认值。
// AI 老师只在建立会话时读取一次快照——改动"下次开启老师"才生效，避免
// 会话中途换人格/音色。
type Settings struct {
	// TeacherPrompt 是拼接在内置基础指令之后的老师性格设定，空 = 无。
	TeacherPrompt string `json:"teacherPrompt"`
	// TeacherVoice 是 AI 老师的 Gemini Live 预置音色名，空 = 跟随 VOICE。
	TeacherVoice string `json:"teacherVoice"`
	// TeacherVADPrefixMs 覆盖老师的"开口判定时长"（判定学生开始说话所需
	// 的持续语音毫秒数，越小越灵敏、噪音误判越多）；0 = 跟随内置默认
	//（llm 包的 teacherVADPrefixPaddingMs）。
	TeacherVADPrefixMs int `json:"teacherVadPrefixMs"`
	// TeacherVADSilenceMs 覆盖老师的"接话等待时长"（判定学生说完所需的
	// 静音毫秒数，越小接话越快、越容易打断学生）；0 = 跟随内置默认。
	TeacherVADSilenceMs int `json:"teacherVadSilenceMs"`
}

func (s *Store) settingsPath() string {
	return filepath.Join(s.dir, "settings.json")
}

// ReadSettings 读取全局设置；文件不存在时返回零值默认，不报错。
func (s *Store) ReadSettings() (*Settings, error) {
	data, err := os.ReadFile(s.settingsPath())
	if os.IsNotExist(err) {
		return &Settings{}, nil
	}
	if err != nil {
		return nil, err
	}
	var v Settings
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", s.settingsPath(), err)
	}
	return &v, nil
}

// WriteSettings 原子落盘全局设置。
func (s *Store) WriteSettings(v *Settings) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.settingsPath(), data)
}
