package pipeline

import (
	"context"
	"fmt"
	"io"
	"testing"
	"testing/synctest"
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
