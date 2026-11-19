package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/zap"
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
		if ctx.Err() != nil {
			t.Fatal("StopReading must drain without canceling its parent context")
		}
		if got := <-delivered; got != "ready" {
			t.Fatalf("delivered=%q", got)
		}
	})
}

func testPipelineOptions() Options {
	options := testSenderOptions()
	options.Source = Source{directory: "fixtures", files: []string{"fixtures/data.parquet"}}
	options.BatchSize = 2
	options.Readers = 1
	options.Senders = 1
	options.Rate = Rate{Installed: false}
	options.Logger = zap.NewNop()
	return options
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

func TestPipelineResourcesAndSourceErrors(t *testing.T) {
	for _, test := range []struct {
		name             string
		cancelDuringOpen bool
		cancelDuringRead bool
		initErr          error
		readErr          error
		readerCloseErr   error
		fileCloseErr     error
		wantOp           string
		want             error
	}{
		{name: "cancel between file and parquet", cancelDuringOpen: true, fileCloseErr: io.ErrShortWrite},
		{name: "cancel closes both resources", cancelDuringRead: true, readerCloseErr: io.ErrClosedPipe, fileCloseErr: io.ErrShortWrite},
		{name: "initialization failure", initErr: io.ErrUnexpectedEOF, wantOp: "open", want: io.ErrUnexpectedEOF},
		{name: "read cause has priority", readErr: io.ErrNoProgress, readerCloseErr: io.ErrClosedPipe, fileCloseErr: io.ErrShortWrite, wantOp: "read", want: io.ErrNoProgress},
		{name: "reader close has priority", readerCloseErr: io.ErrClosedPipe, fileCloseErr: io.ErrShortWrite, wantOp: "reader-close", want: io.ErrClosedPipe},
		{name: "file close", fileCloseErr: io.ErrShortWrite, wantOp: "close", want: io.ErrShortWrite},
		{name: "wrapped EOF flushes residual", readErr: fmt.Errorf("wrapped: %w", io.EOF)},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				options := testPipelineOptions()
				opened := make(chan struct{})
				allowRead := make(chan struct{})
				file := &fixtureFile{openErr: test.initErr, closeErr: test.fileCloseErr}
				rows := &fixtureRows{closeErr: test.readerCloseErr}
				file.rows = rows
				var run *Pipeline
				rows.read = func(target []Transaction) (int, error) {
					<-allowRead
					if test.cancelDuringRead {
						cancel()
						return 0, io.EOF
					}
					run.StopReading()
					target[0] = Transaction{ClientID: "residual"}
					return 1, test.readErr
				}
				if test.cancelDuringOpen {
					file.onOpen = func() { t.Error("OpenRows called after cancellation") }
				}
				options.OpenFile = func(string) (ReaderFile, error) {
					close(opened)
					if test.cancelDuringOpen {
						cancel()
					}
					return file, nil
				}
				var delivered int
				options.Deliver = func(_ context.Context, batch []Transaction, _ int) error {
					delivered += len(batch)
					return nil
				}
				run = Start(ctx, options)
				<-opened
				close(allowRead)
				<-run.Done()
				if !file.closed {
					t.Fatal("file remained open")
				}
				if !test.cancelDuringOpen && test.initErr == nil && !rows.closed {
					t.Fatal("row reader remained open")
				}
				select {
				case err := <-run.SourceErrors():
					var sourceError *SourceError
					if test.want == nil || !errors.As(err, &sourceError) || !errors.Is(err, test.want) ||
						sourceError.Op != test.wantOp || sourceError.Path != "data.parquet" {
						t.Fatalf("source error=%v (%+v), want %s %v", err, sourceError, test.wantOp, test.want)
					}
				default:
					if test.want != nil {
						t.Fatalf("missing source error %v", test.want)
					}
				}
				if test.want == nil && !test.cancelDuringOpen && !test.cancelDuringRead && delivered != 1 {
					t.Fatalf("residual delivered=%d,want 1", delivered)
				}
			})
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
