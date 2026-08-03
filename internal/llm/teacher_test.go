package llm

import (
	"strings"
	"testing"
)

func TestBuildTeacherInstruction(t *testing.T) {
	base := BuildTeacherInstruction("")
	if base != teacherBaseInstruction {
		t.Error("空 persona 应返回纯基础指令")
	}
	got := BuildTeacherInstruction("  你叫 Lily，喜欢用动物打比方。  ")
	if !strings.HasPrefix(got, teacherBaseInstruction) {
		t.Error("拼接结果应以基础指令开头")
	}
	if !strings.Contains(got, "你叫 Lily") {
		t.Error("应包含 persona 内容")
	}
	if strings.Contains(got, "  你叫") {
		t.Error("persona 应被 trim")
	}
	// [CONTEXT] 协议是与桥接层注入格式的契约，基础指令里必须声明
	if !strings.Contains(base, "[CONTEXT]") {
		t.Error("基础指令缺 [CONTEXT] 协议说明")
	}
}

func TestTeacherConfigVAD(t *testing.T) {
	// 零值（settings 未覆盖）回落内置默认
	def := TeacherConfig{}.vad()
	if *def.PrefixPaddingMs != teacherVADPrefixPaddingMs || *def.SilenceDurationMs != teacherVADSilenceDurationMs {
		t.Errorf("零值应回落内置默认, got prefix=%d silence=%d", *def.PrefixPaddingMs, *def.SilenceDurationMs)
	}
	// 覆盖值原样生效
	got := TeacherConfig{VADPrefixPaddingMs: 100, VADSilenceDurationMs: 900}.vad()
	if *got.PrefixPaddingMs != 100 || *got.SilenceDurationMs != 900 {
		t.Errorf("覆盖值未生效, got prefix=%d silence=%d", *got.PrefixPaddingMs, *got.SilenceDurationMs)
	}
}
