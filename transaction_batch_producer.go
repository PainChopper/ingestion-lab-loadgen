package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/parquet-go/parquet-go"
)

func produceBatches(dataPath string) (<-chan []Transaction, error) {
	const batchReadAheadCapacity = 2
	const batchSize = 50_000

	files, err := filepath.Glob(dataPath)
	if err != nil {
		return nil, fmt.Errorf("failed to glob path: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no files found matching pattern: %s", dataPath)
	}

	batches := make(chan []Transaction, batchReadAheadCapacity)
	go func(files []string, batches chan<- []Transaction) {
		defer close(batches)
		accumulator := make([]Transaction, 0, batchSize)
		for {
			for _, filePath := range files {
				file, err := os.Open(filePath)
				if err != nil {
					panic(fmt.Sprintf("failed to open file %s: %v", filePath, err))
				}
				rows := make([]Transaction, batchSize)
				reader := parquet.NewGenericReader[Transaction](file)
				for {
					n, err := reader.Read(rows)
					if n > 0 {

						accumulator = append(accumulator, rows[:n]...)
						if len(accumulator) >= batchSize {
							batches <- accumulator[:batchSize]
							accumulator = append(make([]Transaction, 0, batchSize), accumulator[batchSize:]...)
						}
					}
					if err != nil {
						if err == io.EOF {
							break
						}
						panic(fmt.Sprintf("failed to read rows from file %s: %v", filePath, err))
					}
				}
				errReaderClose := reader.Close()
				if errReaderClose != nil {
					panic(fmt.Sprintf("failed to close reader for file %s: %v", filePath, errReaderClose))
				}
				errReaderClose = file.Close()
				if errReaderClose != nil {
					panic(fmt.Sprintf("failed to close file %s: %v", filePath, errReaderClose))
				}
			}

		}
	}(files, batches)

	return batches, nil
}
