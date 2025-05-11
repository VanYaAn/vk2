package subpub

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubPub_BasicPubSub(t *testing.T) {
	ps := NewSubPub()
	defer ps.Close(context.Background())

	var wg sync.WaitGroup
	var mu sync.Mutex
	receivedMessages := make(map[string][]interface{})

	handler := func(subject string) MessageHandler {
		return func(msg interface{}) {
			mu.Lock()
			receivedMessages[subject] = append(receivedMessages[subject], msg)
			mu.Unlock()
			wg.Done()
		}
	}

	wg.Add(2)
	sub1, err := ps.Subscribe("topic1", handler("topic1"))
	if err != nil {
		t.Fatalf("Failed to subscribe to topic1: %v", err)
	}
	_, err = ps.Subscribe("topic2", handler("topic2"))
	if err != nil {
		t.Fatalf("Failed to subscribe to topic2: %v", err)
	}

	if err := ps.Publish("topic1", "hello topic1"); err != nil {
		t.Errorf("Publish to topic1 failed: %v", err)
	}
	if err := ps.Publish("topic2", "hello topic2"); err != nil {
		t.Errorf("Publish to topic2 failed: %v", err)
	}

	waitTimeout(&wg, t, "waiting for initial messages")

	mu.Lock()
	if len(receivedMessages["topic1"]) != 1 || receivedMessages["topic1"][0] != "hello topic1" {
		t.Errorf("Expected 'hello topic1' for topic1, got %v", receivedMessages["topic1"])
	}
	if len(receivedMessages["topic2"]) != 1 || receivedMessages["topic2"][0] != "hello topic2" {
		t.Errorf("Expected 'hello topic2' for topic2, got %v", receivedMessages["topic2"])
	}
	mu.Unlock()

	wg.Add(1)
	sub1.Unsubscribe()

	if err := ps.Publish("topic1", "message after unsubscribe"); err != nil {
		t.Errorf("Publish to topic1 after unsubscribe failed: %v", err)
	}
	if err := ps.Publish("topic2", "another on topic2"); err != nil {
		t.Errorf("Publish to topic2 (2nd) failed: %v", err)
	}

	waitTimeout(&wg, t, "waiting for message on topic2 after unsubscribe on topic1")

	mu.Lock()
	if len(receivedMessages["topic1"]) != 1 {
		t.Errorf("Expected 1 message for topic1 after unsubscribe, got %v", receivedMessages["topic1"])
	}
	if len(receivedMessages["topic2"]) != 2 || receivedMessages["topic2"][1] != "another on topic2" {
		t.Errorf("Expected 2 messages for topic2, second one 'another on topic2', got %v", receivedMessages["topic2"])
	}
	mu.Unlock()
}

func TestSubPub_MultipleSubscribersSameTopic(t *testing.T) {
	ps := NewSubPub()
	defer ps.Close(context.Background())

	var wg sync.WaitGroup
	var receivedCount1 int32
	var receivedCount2 int32

	wg.Add(2)
	_, _ = ps.Subscribe("topicA", func(msg interface{}) {
		atomic.AddInt32(&receivedCount1, 1)
		wg.Done()
	})
	_, _ = ps.Subscribe("topicA", func(msg interface{}) {
		atomic.AddInt32(&receivedCount2, 1)
		wg.Done()
	})

	ps.Publish("topicA", "message for all on topicA")
	waitTimeout(&wg, t, "waiting for multiple subscribers")

	if atomic.LoadInt32(&receivedCount1) != 1 {
		t.Errorf("Subscriber 1 expected 1 message, got %d", receivedCount1)
	}
	if atomic.LoadInt32(&receivedCount2) != 1 {
		t.Errorf("Subscriber 2 expected 1 message, got %d", receivedCount2)
	}
}

func TestSubPub_SlowSubscriber(t *testing.T) {
	ps := NewSubPub()
	defer ps.Close(context.Background())

	var wg sync.WaitGroup
	fastSubscriberReceived := make(chan bool, 1)
	slowSubscriberProcessed := make(chan bool, 1)

	wg.Add(1)
	_, _ = ps.Subscribe("slowTestTopic", func(msg interface{}) {
		fastSubscriberReceived <- true
		wg.Done()
	})

	wg.Add(1)
	_, _ = ps.Subscribe("slowTestTopic", func(msg interface{}) {
		time.Sleep(100 * time.Millisecond)
		slowSubscriberProcessed <- true
		wg.Done()
	})

	publishTime := time.Now()
	ps.Publish("slowTestTopic", "message for slow/fast")

	select {
	case <-fastSubscriberReceived:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Fast subscriber did not receive message in time")
	}
	fastReceivedDuration := time.Since(publishTime)
	if fastReceivedDuration > 50*time.Millisecond {
		t.Logf("Fast subscriber took %s, longer than expected, but might be scheduler.", fastReceivedDuration)
	}

	select {
	case <-slowSubscriberProcessed:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Slow subscriber did not process message in time")
	}

	waitTimeout(&wg, t, "waiting for both slow and fast subscribers to finish wg")
}

func TestSubPub_FIFO(t *testing.T) {
	ps := NewSubPub()
	defer ps.Close(context.Background())

	var wg sync.WaitGroup
	var mu sync.Mutex
	receivedOrder := make([]int, 0)
	numMessages := 100

	wg.Add(numMessages)
	_, _ = ps.Subscribe("fifoTopic", func(msg interface{}) {
		mu.Lock()
		receivedOrder = append(receivedOrder, msg.(int))
		mu.Unlock()
		wg.Done()
	})

	for i := 0; i < numMessages; i++ {
		ps.Publish("fifoTopic", i)
	}

	waitTimeout(&wg, t, "waiting for FIFO messages")

	mu.Lock()
	defer mu.Unlock()
	if len(receivedOrder) != numMessages {
		t.Fatalf("Expected %d messages, got %d", numMessages, len(receivedOrder))
	}
	for i := 0; i < numMessages; i++ {
		if receivedOrder[i] != i {
			t.Errorf("FIFO order broken. Expected %d at index %d, got %d", i, i, receivedOrder[i])
			break
		}
	}
}

func TestSubPub_Close_NoNewOperations(t *testing.T) {
	ps := NewSubPub()
	ctxClose, cancelClose := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelClose()

	if err := ps.Close(ctxClose); err != nil {
		t.Fatalf("Initial Close failed: %v", err)
	}

	_, err := ps.Subscribe("topicAfterClose", func(msg interface{}) {})
	if err == nil {
		t.Error("Expected error when subscribing after close, got nil")
	} else if err.Error() != "subpub: system is closed" {
		t.Errorf("Expected 'subpub: system is closed' error for subscribe, got: %v", err)
	}

	err = ps.Publish("topicAfterClose", "message")
	if err == nil {
		t.Error("Expected error when publishing after close, got nil")
	} else if err.Error() != "subpub: system is closed" {
		t.Errorf("Expected 'subpub: system is closed' error for publish, got: %v", err)
	}

	ctxClose2, cancelClose2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelClose2()
	if err := ps.Close(ctxClose2); err != nil {
		t.Errorf("Second Close call failed: %v", err)
	}
}

func TestSubPub_Close_ContextTimeout(t *testing.T) {
	ps := NewSubPub()

	var wg sync.WaitGroup
	blockHandler := make(chan struct{})

	wg.Add(1)
	_, _ = ps.Subscribe("blockingTopic", func(msg interface{}) {
		defer wg.Done()
		<-blockHandler
	})

	ps.Publish("blockingTopic", "message to block handler")

	time.Sleep(50 * time.Millisecond)

	ctxClose, cancelClose := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelClose()

	err := ps.Close(ctxClose)
	if err == nil {
		t.Fatal("Expected Close to timeout due to blocked handler, but it didn't")
	}
	expectedErrorMsg := fmt.Sprintf("subpub: close timed out by provided context: %s", context.DeadlineExceeded.Error())
	if err.Error() != expectedErrorMsg {
		if cerr, ok := err.(interface{ Unwrap() error }); ok {
			if cerr.Unwrap() != context.DeadlineExceeded {
				t.Errorf("Expected error to wrap context.DeadlineExceeded, got: %v", err)
			}
		} else if err != context.DeadlineExceeded {
			t.Errorf("Expected error '%s', got: %v", expectedErrorMsg, err)
		}
	}

	close(blockHandler)
	waitTimeout(&wg, t, "waiting for blocked handler to finish after unblocking")

	finalCloseCtx, finalCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer finalCancel()
	if err := ps.Close(finalCloseCtx); err != nil {
		t.Logf("Final close call returned error (expected nil if already closed cleanly): %v", err)
	}
}

func TestSubPub_GoroutineLeakOnUnsubscribe(t *testing.T) {
	ps := NewSubPub()

	sub, _ := ps.Subscribe("leakTest", func(msg interface{}) {
	})
	ps.Publish("leakTest", "some data")
	time.Sleep(10 * time.Millisecond)

	sub.Unsubscribe()
	sub.Unsubscribe()

	ctxClose, cancelClose := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancelClose()
	if err := ps.Close(ctxClose); err != nil {
		t.Errorf("Close failed after unsubscribe, possibly due to hanging goroutines: %v", err)
	}
}

func TestSubPub_PublishToNonExistentSubject(t *testing.T) {
	ps := NewSubPub()
	defer ps.Close(context.Background())

	err := ps.Publish("nonexistent", "test")
	if err != nil {
		t.Errorf("Publish to non-existent subject returned error: %v", err)
	}
}

func TestSubPub_ConcurrentSubscribePublishUnsubscribe(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode.")
	}
	ps := NewSubPub()
	defer ps.Close(context.Background())

	numGoroutines := 50
	numMessagesPerPublisher := 20
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			subject := fmt.Sprintf("concurrent_topic_%d", id%5)
			var receivedCount int32

			subWg := new(sync.WaitGroup)
			subWg.Add(1)

			subscription, err := ps.Subscribe(subject, func(msg interface{}) {
				atomic.AddInt32(&receivedCount, 1)
			})
			if err != nil {
				t.Errorf("Goroutine %d: Subscribe failed: %v", id, err)
				return
			}

			for j := 0; j < numMessagesPerPublisher; j++ {
				msgContent := fmt.Sprintf("msg_from_goroutine_%d_num_%d", id, j)
				if err := ps.Publish(subject, msgContent); err != nil {
					if err.Error() != "subpub: system is closed" {
						t.Errorf("Goroutine %d: Publish failed: %v", id, err)
					}
				}
				if j%5 == 0 {
					time.Sleep(time.Duration(id%3+1) * time.Millisecond)
				}
			}

			time.Sleep(50 * time.Millisecond)
			subscription.Unsubscribe()

		}(i)
	}

	waitTimeout(&wg, t, "waiting for concurrent operations to finish", 10*time.Second)
	time.Sleep(100 * time.Millisecond)
}

func waitTimeout(wg *sync.WaitGroup, t *testing.T, failMsg string, timeout ...time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	defaultTimeout := 1 * time.Second
	if len(timeout) > 0 {
		defaultTimeout = timeout[0]
	}

	select {
	case <-done:
	case <-time.After(defaultTimeout):
		t.Fatalf("%s: timed out after %s", failMsg, defaultTimeout)
	}
}
