package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSettingsMissingReturnsDefaults(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if got.TeacherPrompt != "" || got.TeacherVoice != "" {
		t.Errorf("缺文件应返回零值默认，got %+v", got)
	}
}

func TestWriteReadSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := &Settings{
		TeacherPrompt: "你叫 Lily，喜欢用动物打比方", TeacherVoice: "Puck",
		TeacherVADPrefixMs: 100, TeacherVADSilenceMs: 900,
	}
	if err := s.WriteSettings(want); err != nil {
		t.Fatalf("WriteSettings: %v", err)
	}
	got, err := s.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if *got != *want {
		t.Errorf("读回不一致: got %+v want %+v", got, want)
	}
	// 原子写不应留下临时文件
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("残留临时文件 %s", e.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); err != nil {
		t.Errorf("settings.json 未落盘: %v", err)
	}
}

func TestReadSettingsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadSettings(); err == nil {
		t.Error("损坏文件应报错")
	}
}
