package main

import (
	"fmt"
	"path/filepath"

	"github.com/parquet-go/parquet-go"
)

func produceBatches(dataPath string) (<-chan TransactionBatch, error) {
	files, err := filepath.Glob(dataPath)
	if err != nil {
		return nil, fmt.Errorf("failed to glob path: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no files found matching pattern: %s", dataPath)
	}

	batches := make(chan TransactionBatch, 4)

	go func(files []string, batches chan<- TransactionBatch) {
		defer close(batches)
		for _, file := range files {
			rows, err := parquet.ReadFile[Transaction](file)
			if err != nil {
				panic(fmt.Sprintf("failed to read parquet file %s: %v", file, err))
			}
			batches <- TransactionBatch{
				Transactions: rows,
				Source:       file,
			}
		}
	}(files, batches)

	return batches, nil
}
