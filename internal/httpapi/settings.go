package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"phonicsfun/internal/store"
)

// --- 全局设置 ---

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.ReadSettings()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var body store.Settings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "请求体解析失败")
		return
	}
	body.TeacherPrompt = strings.TrimSpace(body.TeacherPrompt)
	body.TeacherVoice = strings.TrimSpace(body.TeacherVoice)
	if len([]rune(body.TeacherPrompt)) > store.MaxTeacherPromptLen {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("提示词过长（最多 %d 字）", store.MaxTeacherPromptLen))
		return
	}
	// 音色名只做长度防御，不做白名单——Gemini 预置音色会增减，前端下拉
	// 才是选择入口，这里只防异常输入。
	if len(body.TeacherVoice) > 50 {
		httpError(w, http.StatusBadRequest, "音色名无效")
		return
	}
	// VAD 覆盖值：0 = 跟随内置默认，非 0 必须落在合法区间（含负数防御）
	if v := body.TeacherVADPrefixMs; v != 0 && (v < store.MinTeacherVADPrefixMs || v > store.MaxTeacherVADPrefixMs) {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("开口判定时长需在 %d-%d 毫秒之间", store.MinTeacherVADPrefixMs, store.MaxTeacherVADPrefixMs))
		return
	}
	if v := body.TeacherVADSilenceMs; v != 0 && (v < store.MinTeacherVADSilenceMs || v > store.MaxTeacherVADSilenceMs) {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("接话等待时长需在 %d-%d 毫秒之间", store.MinTeacherVADSilenceMs, store.MaxTeacherVADSilenceMs))
		return
	}
	if err := s.store.WriteSettings(&body); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, &body)
}
