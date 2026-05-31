package main

import (
	"fmt"
	"path/filepath"

	"github.com/parquet-go/parquet-go"
)

type lazyFileReader struct {
	files   []string
	current int
	rows    []Transaction
	rowPos  int
}

func NewLazyFileReader(dataPath string) (TransactionIterator, error) {
	files, err := filepath.Glob(dataPath)
	if err != nil {
		return nil, fmt.Errorf("failed to glob path: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no files found matching pattern: %s", dataPath)
	}

	return &lazyFileReader{
		files:   files,
		current: 0,
	}, nil
}

func (r *lazyFileReader) Next() (*Transaction, error) {
	for {
		if len(r.rows) == 0 {
			if r.current >= len(r.files) {
				return nil, nil
			}
			rows, err := parquet.ReadFile[Transaction](r.files[r.current])
			if err != nil {
				return nil, fmt.Errorf("failed to read parquet file %s: %w", r.files[r.current], err)
			}
			r.rows = rows
			r.rowPos = 0
		}
		if r.rowPos >= len(r.rows) {
			r.rows = nil
			r.rowPos = 0
			r.current++
			continue
		}
		tran := &r.rows[r.rowPos]
		r.rowPos++
		return tran, nil
	}
}
