package tools

import (
	"errors"
	"strings"
	"testing"

	"github.com/imbalaomao/liverelay/internal/config"
)

func testCfg() *config.Config {
	c := config.Default()
	c.Tools = append(c.Tools, config.Tool{
		ID: "n_m3u8dl_re", Name: "N_m3u8DL-RE", Path: "tools/N_m3u8DL-RE.exe", Role: "record",
	})
	c.Tasks = []config.Task{
		{ID: "t1", Name: "测试任务", ToolID: "streamlink", RecordToolID: "n_m3u8dl_re"},
	}
	return c
}

// ---------- 新增 ----------

func TestAddTool(t *testing.T) {
	c := testCfg()
	err := Add(c, config.Tool{ID: "custom-drm", Name: "streamlink-drm", Path: "tools/sl-drm.exe", Role: "fetch"})
	if err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	if _, ok := Find(c, "custom-drm"); !ok {
		t.Error("新增后应能按 ID 找到")
	}
}

func TestAddRejectsDuplicateID(t *testing.T) {
	c := testCfg()
	err := Add(c, config.Tool{ID: "streamlink", Name: "山寨", Path: "x.exe", Role: "fetch"})
	if !errors.Is(err, ErrDuplicateID) {
		t.Errorf("重复 ID 应返回 ErrDuplicateID，实际 %v", err)
	}
}

func TestAddRejectsBadID(t *testing.T) {
	bad := []string{"", "有中文", "a/b", "a\\b", "a b", strings.Repeat("x", 65)}
	for _, id := range bad {
		c := testCfg()
		if err := Add(c, config.Tool{ID: id, Name: "n", Path: "x.exe", Role: "fetch"}); !errors.Is(err, ErrInvalidID) {
			t.Errorf("ID %q 应被拒绝，实际 %v", id, err)
		}
	}
}

func TestAddRejectsEmptyPath(t *testing.T) {
	c := testCfg()
	if err := Add(c, config.Tool{ID: "ok-id", Name: "n", Path: "", Role: "fetch"}); !errors.Is(err, ErrEmptyPath) {
		t.Error("空路径应被拒绝")
	}
}

func TestAddRejectsBadRole(t *testing.T) {
	c := testCfg()
	if err := Add(c, config.Tool{ID: "ok-id", Name: "n", Path: "x.exe", Role: "什么"}); !errors.Is(err, ErrInvalidRole) {
		t.Error("非法 role 应被拒绝")
	}
}

func TestAddForcesBuiltinFalse(t *testing.T) {
	// 用户提交的 JSON 若带 builtin:true，会让这个内核变成删不掉的僵尸条目
	c := testCfg()
	if err := Add(c, config.Tool{ID: "fake", Name: "n", Path: "x.exe", Role: "fetch", Builtin: true}); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	got, _ := Find(c, "fake")
	if got.Builtin {
		t.Error("新增的内核必须是非内置，否则用户再也删不掉")
	}
}

// ---------- 删除 ----------

func TestDeleteBuiltinIsRejected(t *testing.T) {
	for _, id := range []string{"streamlink", "yt-dlp", "ffmpeg"} {
		c := testCfg()
		if err := Delete(c, id); !errors.Is(err, ErrBuiltinReadOnly) {
			t.Errorf("内置内核 %s 应不可删除，实际 %v", id, err)
		}
	}
}

func TestDeleteInUseIsRejected(t *testing.T) {
	c := testCfg()
	err := Delete(c, "n_m3u8dl_re")
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("被任务引用的内核应不可删除，实际 %v", err)
	}
	// 报错要指名道姓，否则用户面对一堆任务无从下手
	if !strings.Contains(err.Error(), "测试任务") {
		t.Errorf("错误信息应含引用它的任务名，实际 %q", err.Error())
	}
}

func TestDeleteUnusedCustom(t *testing.T) {
	c := testCfg()
	c.Tasks = nil
	if err := Delete(c, "n_m3u8dl_re"); err != nil {
		t.Fatalf("未被引用的自定义内核应可删除: %v", err)
	}
	if _, ok := Find(c, "n_m3u8dl_re"); ok {
		t.Error("删除后不应再找得到")
	}
}

func TestDeleteMissing(t *testing.T) {
	c := testCfg()
	if err := Delete(c, "不存在"); !errors.Is(err, ErrNotFound) {
		t.Errorf("删除不存在的内核应返回 ErrNotFound，实际 %v", err)
	}
}

// ---------- 编辑 ----------

func TestUpdateBuiltinIsRejected(t *testing.T) {
	c := testCfg()
	err := Update(c, config.Tool{ID: "streamlink", Name: "改名", Path: "别的.exe", Role: "record"})
	if !errors.Is(err, ErrBuiltinReadOnly) {
		t.Errorf("内置内核不可编辑，实际 %v", err)
	}
}

func TestUpdateCustom(t *testing.T) {
	c := testCfg()
	err := Update(c, config.Tool{ID: "n_m3u8dl_re", Name: "新名字", Path: "新路径.exe", Role: "both"})
	if err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	got, _ := Find(c, "n_m3u8dl_re")
	if got.Name != "新名字" || got.Path != "新路径.exe" || got.Role != "both" {
		t.Errorf("字段未更新: %+v", got)
	}
}

func TestUpdatePreservesProbeResult(t *testing.T) {
	// 探测结果由 Probe 写入，前端编辑表单不会回传这两个字段，
	// 路径没变时 Update 不该把它们抹掉
	c := testCfg()
	SetProbe(c, "n_m3u8dl_re", Info{Version: "0.2.0", Summary: "版本 0.2.0"})
	if err := Update(c, config.Tool{ID: "n_m3u8dl_re", Name: "改名", Path: "tools/N_m3u8DL-RE.exe", Role: "record"}); err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	got, _ := Find(c, "n_m3u8dl_re")
	if got.Version != "0.2.0" {
		t.Errorf("路径未变时 Update 抹掉了探测到的版本号: %+v", got)
	}
}

func TestUpdateChangedPathClearsProbe(t *testing.T) {
	// 换了路径就是换了个二进制，旧版本号必须作废，否则设置页显示的是别人的版本
	c := testCfg()
	SetProbe(c, "n_m3u8dl_re", Info{Version: "0.2.0", Summary: "版本 0.2.0"})
	if err := Update(c, config.Tool{ID: "n_m3u8dl_re", Name: "n", Path: "别的.exe", Role: "record"}); err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	got, _ := Find(c, "n_m3u8dl_re")
	if got.Version != "" || got.CapSummary != "" {
		t.Errorf("改路径后应清空探测结果，实际 version=%q summary=%q", got.Version, got.CapSummary)
	}
}

// ---------- 路径覆盖 ----------

func TestSetOverrideOnBuiltin(t *testing.T) {
	c := testCfg()
	// 不写死默认路径：它随内核的落地方式变过一次，写死了只会变成假报警
	def, _ := Find(config.Default(), "streamlink")

	if err := SetOverride(c, "streamlink", `C:\bin\streamlink.exe`); err != nil {
		t.Fatalf("SetOverride 失败: %v", err)
	}
	got, _ := Find(c, "streamlink")
	if got.PathOverride != `C:\bin\streamlink.exe` {
		t.Errorf("PathOverride = %q", got.PathOverride)
	}
	// 默认路径必须留着，否则"恢复默认"就没得可恢复
	if got.Path != def.Path {
		t.Errorf("覆盖路径不应改动默认 Path，实际 %q，期望 %q", got.Path, def.Path)
	}
	if got.EffectivePath() != `C:\bin\streamlink.exe` {
		t.Errorf("EffectivePath = %q", got.EffectivePath())
	}
}

func TestResetOverride(t *testing.T) {
	c := testCfg()
	_ = SetOverride(c, "streamlink", `C:\bin\streamlink.exe`)
	if err := ResetOverride(c, "streamlink"); err != nil {
		t.Fatalf("ResetOverride 失败: %v", err)
	}
	def, _ := Find(config.Default(), "streamlink")
	got, _ := Find(c, "streamlink")
	if got.PathOverride != "" || got.EffectivePath() != def.Path {
		t.Errorf("恢复默认后: %+v，期望回到 %q", got, def.Path)
	}
}

func TestResetOverrideClearsStaleProbe(t *testing.T) {
	// 覆盖路径指向的是另一个二进制，恢复默认后旧版本号必须作废，
	// 否则设置页会一直显示别人的版本
	c := testCfg()
	_ = SetOverride(c, "streamlink", `C:\bin\streamlink.exe`)
	SetProbe(c, "streamlink", Info{Version: "9.9.9", Summary: "版本 9.9.9"})
	_ = ResetOverride(c, "streamlink")

	got, _ := Find(c, "streamlink")
	if got.Version != "" || got.CapSummary != "" {
		t.Errorf("恢复默认后应清空上一份探测结果，实际 version=%q summary=%q", got.Version, got.CapSummary)
	}
}

func TestSetOverrideClearsStaleProbe(t *testing.T) {
	c := testCfg()
	SetProbe(c, "streamlink", Info{Version: "6.7.4", Summary: "版本 6.7.4"})
	_ = SetOverride(c, "streamlink", `C:\bin\other.exe`)

	got, _ := Find(c, "streamlink")
	if got.Version != "" {
		t.Errorf("换了路径就该重探，旧版本号必须清空，实际 %q", got.Version)
	}
}

func TestSetOverrideEmptyPathIsReset(t *testing.T) {
	c := testCfg()
	_ = SetOverride(c, "streamlink", `C:\bin\streamlink.exe`)
	if err := SetOverride(c, "streamlink", "  "); err != nil {
		t.Fatalf("清空覆盖路径失败: %v", err)
	}
	got, _ := Find(c, "streamlink")
	if got.PathOverride != "" {
		t.Errorf("传入空白应等同恢复默认，实际 %q", got.PathOverride)
	}
}

func TestSetOverrideMissing(t *testing.T) {
	c := testCfg()
	if err := SetOverride(c, "不存在", "x.exe"); !errors.Is(err, ErrNotFound) {
		t.Errorf("实际 %v", err)
	}
}

// ---------- 引用查询 ----------

func TestUsedBy(t *testing.T) {
	c := testCfg()
	if names := UsedBy(c, "streamlink"); len(names) != 1 || names[0] != "测试任务" {
		t.Errorf("UsedBy(streamlink) = %v", names)
	}
	if names := UsedBy(c, "yt-dlp"); len(names) != 0 {
		t.Errorf("UsedBy(yt-dlp) = %v，期望空", names)
	}
}

func TestUsedByCountsRecordToolOnce(t *testing.T) {
	// 同一个任务既用它抓流又用它录制时，不该在提示里出现两次
	c := testCfg()
	c.Tasks = []config.Task{{ID: "t1", Name: "双用", ToolID: "ffmpeg", RecordToolID: "ffmpeg"}}
	if names := UsedBy(c, "ffmpeg"); len(names) != 1 {
		t.Errorf("同一任务重复引用应只计一次，实际 %v", names)
	}
}
