package pipeline

import (
	"context"
	"fmt"
	"io"
	"testing"
	"testing/synctest"
	"time"
)

func TestPipelineSetRateIsReadyAfterStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		options := testPipelineOptions()
		options.Logger = nil
		options.Rate = Rate{TPS: 0, Installed: true}
		allowRead := make(chan struct{})
		var run *Pipeline
		options.OpenFile = func(string) (ReaderFile, error) {
			return &fixtureFile{rows: &fixtureRows{read: func(rows []Transaction) (int, error) {
				<-allowRead
				run.StopReading()
				rows[0] = Transaction{ClientID: "ready"}
				return 1, io.EOF
			}}}, nil
		}
		delivered := make(chan string, 1)
		options.Deliver = func(_ context.Context, batch []Transaction, _ int) error {
			delivered <- batch[0].ClientID
			return nil
		}
		run = Start(ctx, options)
		// No separate initialization call or scheduling barrier precedes this command.
		run.SetRate(Rate{Installed: false})
		close(allowRead)
		<-run.Done()
		if got := <-delivered; got != "ready" {
			t.Fatalf("delivered=%q", got)
		}
	})
}

func TestPipelineSampleUsesElapsedInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		options := testPipelineOptions()
		release := make(chan struct{})
		var run *Pipeline
		options.OpenFile = func(string) (ReaderFile, error) {
			return &fixtureFile{rows: &fixtureRows{read: func(rows []Transaction) (int, error) {
				<-release
				run.StopReading()
				return 2, io.EOF
			}}}, nil
		}
		options.Deliver = func(context.Context, []Transaction, int) error { return nil }
		run = Start(t.Context(), options)
		close(release)
		<-run.Done()
		time.Sleep(1500 * time.Millisecond)
		if got := run.Sample(time.Now()); got != 1500*time.Millisecond {
			t.Fatalf("elapsed=%v", got)
		}
		snapshot := run.Snapshot(time.Now())
		rate := 2 / 1.5
		if snapshot.Reader.ReadTPS != rate || snapshot.ReaderQueue.SentTransactionsPerSecond != rate ||
			snapshot.ReaderQueue.ReceivedTransactionsPerSecond != rate || snapshot.SenderQueue.SentTransactionsPerSecond != rate ||
			snapshot.SenderQueue.ReceivedTransactionsPerSecond != rate || run.TakeCompleted() != 2 {
			t.Fatalf("inconsistent delayed sample: %+v", snapshot)
		}
		time.Sleep(500 * time.Millisecond)
		run.Sample(time.Now())
		snapshot = run.Snapshot(time.Now())
		if snapshot.Reader.ReadTPS != 0 || snapshot.ReaderQueue.SentTransactionsPerSecond != 0 ||
			snapshot.SenderQueue.ReceivedTransactionsPerSecond != 0 || run.TakeCompleted() != 0 {
			t.Fatalf("empty elapsed interval: %+v", snapshot)
		}
	})
}

func TestPipelineSampleIgnoresNonPositiveElapsed(t *testing.T) {
	for _, test := range []struct {
		name   string
		offset time.Duration
	}{
		{name: "same time"},
		{name: "earlier time", offset: -time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			start := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
			run := &Pipeline{sampledAt: start, readerQueue: newBatchQueue(1, 2), senderQueue: newBatchQueue(1, 2)}
			run.readerTelemetry.startInterval(start)
			run.readerTelemetry.recordRead(2)
			if !run.readerQueue.trySend(make([]Transaction, 2)) || !run.senderQueue.trySend(make([]Transaction, 2)) {
				t.Fatal("test queues did not accept a batch")
			}
			if got := run.Sample(start.Add(test.offset)); got != 0 {
				t.Fatalf("nonpositive sample elapsed=%s", got)
			}
			if got := run.Sample(start.Add(time.Second)); got != time.Second {
				t.Fatalf("sample moved the original anchor: %s", got)
			}
			if got := run.readerTelemetry.snapshot(); got.RowsRead != 2 || got.ReadTPS != 2 {
				t.Fatalf("nonpositive sample lost read counters: %+v", got)
			}
			for _, queue := range []*batchQueue{run.readerQueue, run.senderQueue} {
				if got := queue.snapshot(start.Add(time.Second)); got.SentTransactionsTotal != 2 || got.SentTransactionsPerSecond != 2 {
					t.Fatalf("nonpositive sample lost queue counters: %+v", got)
				}
			}
		})
	}
}

func TestPipelineQueuesCloseAfterReaderExits(t *testing.T) {
	for _, capacity := range []int{0, 2} {
		t.Run(fmt.Sprintf("capacity %d", capacity), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				options := testPipelineOptions()
				options.ReaderCapacity, options.SenderCapacity = capacity, capacity
				inRead := make(chan struct{})
				release := make(chan struct{})
				first := true
				options.OpenFile = func(string) (ReaderFile, error) {
					return &fixtureFile{rows: &fixtureRows{read: func(rows []Transaction) (int, error) {
						if first {
							first = false
							rows[0] = Transaction{ClientID: "before cancellation"}
							return 1, nil
						}
						close(inRead)
						<-release
						return 0, io.EOF
					}}}, nil
				}
				run := Start(ctx, options)
				<-inRead
				if cap(run.readerQueue.ch) != capacity || cap(run.senderQueue.ch) != capacity {
					t.Fatalf("physical queue capacities are not %d", capacity)
				}
				cancel()
				synctest.Wait()
				for _, queue := range []*batchQueue{run.readerQueue, run.senderQueue} {
					select {
					case _, open := <-queue.ch:
						t.Fatalf("queue became readable before Reader exited, open=%t", open)
					default:
					}
				}
				select {
				case <-run.Done():
					t.Fatal("pipeline completed before Reader left Read")
				default:
				}
				close(release)
				<-run.Done()
				for _, queue := range []*batchQueue{run.readerQueue, run.senderQueue} {
					if _, open := <-queue.ch; open {
						t.Fatal("queue remained open after drain")
					}
				}
			})
		})
	}
}
