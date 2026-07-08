package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/parquet-go/parquet-go"
)

func produceBatches(dataPath string) (<-chan TransactionBatch, error) {
	const batchReadAheadCapacity = 2

	files, err := filepath.Glob(dataPath)
	if err != nil {
		return nil, fmt.Errorf("failed to glob path: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no files found matching pattern: %s", dataPath)
	}

	batches := make(chan TransactionBatch, batchReadAheadCapacity)

	go func(files []string, batches chan<- TransactionBatch) {
		defer close(batches)
		for {
			for _, file := range files {
				rows, err := parquet.ReadFile[Transaction](file)
				if err != nil {
					panic(fmt.Sprintf("failed to read parquet file %s: %v", file, err))
				}
				fold, err := foldFromFilePath(file)
				if err != nil {
					panic(fmt.Sprintf("failed to read fold from parquet file path %s: %v", file, err))
				}
				for i := range rows {
					rows[i].Fold = fold
				}
				batches <- TransactionBatch{
					Transactions: rows,
					Source:       file,
				}
			}
		}
	}(files, batches)

	return batches, nil
}

func foldFromFilePath(file string) (int32, error) {
	dir := filepath.Base(filepath.Dir(file))
	key, value, ok := strings.Cut(dir, "=")
	if !ok || key != "fold" {
		return 0, fmt.Errorf("expected parent directory in fold=N format")
	}
	fold, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid fold value: %w", err)
	}
	return int32(fold), nil
}
