package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/parquet-go/parquet-go"
)

func readBatches(
	ctx context.Context,
	dataPath string,
	batchSize int,
	batches chan<- []Transaction,
	telemetry *readerTelemetry,
	channelTelemetry *channelTelemetry,
) (<-chan struct{}, error) {
	files, err := filepath.Glob(dataPath)
	if err != nil {
		return nil, fmt.Errorf("failed to glob path: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no files found matching pattern: %s", dataPath)
	}

	done := make(chan struct{})
	go func(files []string, batches chan<- []Transaction) {
		defer func() {
			close(done)
		}()

		// Accumulate rows across files until a batch reaches the target size.
		accumulator := make([]Transaction, 0, batchSize)
		rows := make([]Transaction, batchSize)

		for {
			for _, filePath := range files {
				if ctx.Err() != nil {
					return
				}
				file, err := os.Open(filePath)
				if err != nil {
					panic(fmt.Sprintf("failed to open file %s: %v", filePath, err))
				}
				source := filepath.ToSlash(filePath)
				func() {
					defer func() {
						if err := file.Close(); err != nil {
							panic(fmt.Sprintf("failed to close file %s: %v", filePath, err))
						}
					}()

					reader := parquet.NewGenericReader[Transaction](file)
					defer func() {
						if err := reader.Close(); err != nil {
							panic(fmt.Sprintf("failed to close reader for file %s: %v", filePath, err))
						}
					}()

					for {
						if ctx.Err() != nil {
							return
						}
						n, err := reader.Read(rows)
						if n > 0 {
							telemetry.recordRead(n, source)
							remainingRows := rows[:n]
							if accumulator == nil {
								accumulator = make([]Transaction, 0, batchSize)
							}
							remainingCapacity := batchSize - len(accumulator)
							if len(remainingRows) < remainingCapacity {
								accumulator = append(accumulator, remainingRows...)
							} else {
								accumulator = append(accumulator, remainingRows[:remainingCapacity]...)
								if !channelTelemetry.send(ctx, batches, accumulator) {
									return
								}
								remainingRows = remainingRows[remainingCapacity:]
								if len(remainingRows) == 0 {
									accumulator = nil
								} else {
									accumulator = make([]Transaction, 0, batchSize)
									accumulator = append(accumulator, remainingRows...)
								}
							}
						}
						if err != nil {
							if err == io.EOF {
								return
							}
							panic(fmt.Sprintf("failed to read rows from file %s: %v", filePath, err))
						}
					}
				}()
			}
		}
	}(files, batches)

	return done, nil
}
