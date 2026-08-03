package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"phonicsfun/internal/store"
)

// newTestServer 起一个只依赖 store 的 Server（settings 端点不触 pipeline/llm）。
func newTestServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(st, nil, nil, fstest.MapFS{})
}

func TestSettingsRoundTrip(t *testing.T) {
	s := newTestServer(t)

	// 默认值
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET 默认: code=%d body=%s", rec.Code, rec.Body)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"teacherPrompt":""`) {
		t.Errorf("默认应为空提示词, got %s", got)
	}

	// 写入
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/settings",
		strings.NewReader(`{"teacherPrompt":" 你叫 Lily ","teacherVoice":"Puck","teacherVadPrefixMs":100,"teacherVadSilenceMs":900}`))
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: code=%d body=%s", rec.Code, rec.Body)
	}

	// 读回（trim 生效）
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/settings", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `"teacherPrompt":"你叫 Lily"`) || !strings.Contains(body, `"teacherVoice":"Puck"`) {
		t.Errorf("读回不一致: %s", body)
	}
	if !strings.Contains(body, `"teacherVadPrefixMs":100`) || !strings.Contains(body, `"teacherVadSilenceMs":900`) {
		t.Errorf("VAD 覆盖值读回不一致: %s", body)
	}
}

func TestSettingsVADOutOfRange(t *testing.T) {
	s := newTestServer(t)
	cases := []string{
		`{"teacherVadPrefixMs":10}`,      // 低于下限
		`{"teacherVadPrefixMs":1000}`,    // 高于上限
		`{"teacherVadPrefixMs":-40}`,     // 负数
		`{"teacherVadSilenceMs":50}`,     // 低于下限
		`{"teacherVadSilenceMs":10000}`,  // 高于上限
	}
	for _, body := range cases {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/settings", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s 应 400, got %d", body, rec.Code)
		}
	}
	// 0 = 跟随默认，必须合法
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/settings",
		strings.NewReader(`{"teacherVadPrefixMs":0,"teacherVadSilenceMs":0}`)))
	if rec.Code != http.StatusOK {
		t.Errorf("0 值应 200, got %d body=%s", rec.Code, rec.Body)
	}
}

func TestSettingsPromptTooLong(t *testing.T) {
	s := newTestServer(t)
	long := strings.Repeat("啊", store.MaxTeacherPromptLen+1)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/settings",
		strings.NewReader(`{"teacherPrompt":"`+long+`"}`))
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("超长提示词应 400, got %d", rec.Code)
	}
}

func TestSettingsBadJSON(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/settings", strings.NewReader("{broken")))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("坏 JSON 应 400, got %d", rec.Code)
	}
}
