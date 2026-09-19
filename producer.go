package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/parquet-go/parquet-go"
)

func produceBatches(
	ctx context.Context,
	dataPath string,
	batchSize int,
	readerChannelCapacity int,
	telemetry *readerTelemetry,
	readerChannelTelemetry *readerChannelTelemetry,
) (<-chan []Transaction, error) {
	files, err := filepath.Glob(dataPath)
	if err != nil {
		return nil, fmt.Errorf("failed to glob path: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no files found matching pattern: %s", dataPath)
	}

	batches := make(chan []Transaction, readerChannelCapacity)
	readerChannelTelemetry.start(batches, batchSize)
	go func(files []string, batches chan<- []Transaction) {
		defer close(batches)

		// Accumulate rows across files until a batch reaches the target size.
		accumulator := make([]Transaction, 0, batchSize)

		for {
			for _, filePath := range files {
				if ctx.Err() != nil {
					return
				}
				file, err := os.Open(filePath)
				if err != nil {
					panic(fmt.Sprintf("failed to open file %s: %v", filePath, err))
				}
				func() {
					defer func() {
						if err := file.Close(); err != nil {
							panic(fmt.Sprintf("failed to close file %s: %v", filePath, err))
						}
					}()

					rows := make([]Transaction, batchSize)
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
							telemetry.recordRead(n, filePath)
							accumulator = append(accumulator, rows[:n]...)
							if len(accumulator) >= batchSize {
								if readerChannelTelemetry.send(ctx, batches, accumulator[:batchSize]) {
									accumulator = append(make([]Transaction, 0, batchSize), accumulator[batchSize:]...)
								} else {
									return
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

	return batches, nil
}
