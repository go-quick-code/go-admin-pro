package push

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

// 事件模型测试：JSON 形状以 spec §4.5.4 的示例为独立真源。
func TestNotificationEventJSON(t *testing.T) {
	ev := NewNotification(EventData{
		Title:       "您有新的审批任务",
		Content:     "年度合同审批",
		Type:        "bpm_todo",
		BizType:     "bpm_task",
		BizID:       1,
		Route:       "/bpm/task",
		RouteParams: `{"taskId":1}`,
	})
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	want := `{"type":"notification","data":{"title":"您有新的审批任务","content":"年度合同审批",` +
		`"type":"bpm_todo","bizType":"bpm_task","bizId":1,"route":"/bpm/task",` +
		`"routeParams":"{\"taskId\":1}"},"ts":` + strconv.FormatInt(ev.Ts, 10) + `}`
	if string(raw) != want {
		t.Fatalf("event JSON = %s, want %s", raw, want)
	}
	if ev.Ts < time.Now().Unix()-1 || ev.Ts > time.Now().Unix()+1 {
		t.Fatalf("ts = %d, want 当前 unix 秒", ev.Ts)
	}
}

// 同一 (tenant, user) 的多条连接全部收到；其他用户/其他租户收不到。
func TestHubFanoutToAllConnectionsOfUser(t *testing.T) {
	hub := NewHub()
	a1 := hub.Subscribe(1, 7)
	a2 := hub.Subscribe(1, 7)
	other := hub.Subscribe(1, 8)
	otherTenant := hub.Subscribe(2, 7)

	ev := NewNotification(EventData{Title: "t1"})
	hub.Publish(1, 7, ev)

	for name, sub := range map[string]*Subscriber{"a1": a1, "a2": a2} {
		select {
		case got := <-sub.Events():
			if got != ev {
				t.Fatalf("%s: 收到 %+v, want %+v", name, got, ev)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s: 1s 内未收到事件", name)
		}
	}
	for name, sub := range map[string]*Subscriber{"other": other, "otherTenant": otherTenant} {
		select {
		case got := <-sub.Events():
			t.Fatalf("%s: 不应收到事件，却收到 %+v", name, got)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// 慢消费者：发送缓冲满时丢弃该事件并计数，发布方不被阻塞。
func TestHubSlowConsumerDropsAndCounts(t *testing.T) {
	hub := NewHub()
	sub := hub.Subscribe(1, 7)

	total := subscriberBuffer + 6
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < total; i++ {
			hub.Publish(1, 7, NewNotification(EventData{Title: "m", BizID: int64(i)}))
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish 被慢消费者阻塞")
	}

	if got := sub.Dropped(); got != 6 {
		t.Fatalf("Dropped = %d, want 6", got)
	}
	// 缓冲里恰好是最早的 buffer 条（先进先出，丢的是后面的）。
	for i := 0; i < subscriberBuffer; i++ {
		select {
		case got := <-sub.Events():
			if got.Data.BizID != int64(i) {
				t.Fatalf("缓冲第 %d 条 BizID = %d, want %d", i, got.Data.BizID, i)
			}
		case <-time.After(time.Second):
			t.Fatalf("缓冲第 %d 条未收到", i)
		}
	}
	select {
	case got := <-sub.Events():
		t.Fatalf("缓冲应已取空，却又收到 %+v", got)
	default:
	}
}

// 无订阅者发布是合法 no-op。
func TestHubPublishWithoutSubscribers(t *testing.T) {
	hub := NewHub()
	hub.Publish(1, 7, NewNotification(EventData{Title: "x"}))
}

// 退订清理：Done 关闭、计数归零、重复退订幂等。
func TestHubUnsubscribeCleansUp(t *testing.T) {
	hub := NewHub()
	sub := hub.Subscribe(1, 7)
	if got := hub.Subscribers(1, 7); got != 1 {
		t.Fatalf("Subscribers = %d, want 1", got)
	}
	hub.Unsubscribe(sub)
	hub.Unsubscribe(sub) // 幂等
	if got := hub.Subscribers(1, 7); got != 0 {
		t.Fatalf("退订后 Subscribers = %d, want 0", got)
	}
	select {
	case <-sub.Done():
	default:
		t.Fatal("退订后 Done 未关闭")
	}
}
