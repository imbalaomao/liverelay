package appcore

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/imbalaomao/liverelay/internal/config"
	"github.com/imbalaomao/liverelay/internal/core"
)

// heapMB 返回当前堆占用（MB）。
func heapMB() float64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapAlloc) / (1 << 20)
}

func TestEventFloodDoesNotGrowHeap(t *testing.T) {
	// 内存红线：通宵重连能产生几万条事件。若事件日志没有真正封顶，
	// 堆会一路涨到把 8GB 的目标机拖垮
	c := newTestCore(t)
	var ids []string
	for i := 0; i < 16; i++ {
		got, err := c.AddTask(config.Task{
			Name: fmt.Sprintf("任务%d", i), SourceURL: "https://x", ToolID: "streamlink",
			Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a", Key: "k"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, got.ID)
	}

	base := heapMB()
	// 16 个任务各灌 5000 条，相当于通宵重连的量级
	for round := 0; round < 5000; round++ {
		for _, id := range ids {
			c.onEvent(core.Event{
				TaskID: id, State: core.StateReconnecting,
				Msg: fmt.Sprintf("第 %d 次断流：connection reset by peer，2s 后重连", round),
			})
		}
	}
	after := heapMB()

	t.Logf("堆占用 %.1fMB → %.1fMB（灌入 %d 条事件）", base, after, 5000*len(ids))
	if grown := after - base; grown > 8 {
		t.Errorf("堆增长 %.1fMB，事件日志没有真正封顶", grown)
	}
	for _, id := range ids {
		if n := len(c.Events(id)); n > MaxEventsPerTask {
			t.Errorf("任务 %s 留下 %d 条事件，超过上限 %d", id, n, MaxEventsPerTask)
		}
	}
}

func TestManyTasksViewStaysCheap(t *testing.T) {
	// 任务视图每次推送都会整份重建，任务多时不能成为负担
	c := newTestCore(t)
	for i := 0; i < config.MaxConcurrentCap*4; i++ {
		if _, err := c.AddTask(config.Task{
			Name: fmt.Sprintf("任务%d", i), SourceURL: "https://x", ToolID: "streamlink",
			Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a", Key: "k"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	base := heapMB()
	for i := 0; i < 2000; i++ {
		_ = c.TaskViews()
	}
	after := heapMB()
	t.Logf("堆占用 %.1fMB → %.1fMB（重建视图 2000 次）", base, after)
	if grown := after - base; grown > 4 {
		t.Errorf("重建视图导致堆增长 %.1fMB，疑似有残留引用", grown)
	}
}
