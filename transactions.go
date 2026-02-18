package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/parquet-go/parquet-go"
)

type Transaction struct {
	ClientID     string    `json:"client_id" parquet:"client_id"`
	EventTime    time.Time `json:"event_time" parquet:"event_time"`
	Amount       string    `json:"amount" parquet:"amount"` // string to preserve precision
	EventType    int       `json:"event_type" parquet:"event_type"`
	EventSubtype int       `json:"event_subtype" parquet:"event_subtype"`
	Currency     int       `json:"currency" parquet:"currency"`
	SrcType11    int       `json:"src_type11" parquet:"src_type11"`
	SrcType12    int       `json:"src_type12" parquet:"src_type12"`
	DstType11    int       `json:"dst_type11" parquet:"dst_type11"`
	DstType12    int       `json:"dst_type12" parquet:"dst_type12"`
	SrcType21    int       `json:"src_type21" parquet:"src_type21"`
	SrcType22    int       `json:"src_type22" parquet:"src_type22"`
	SrcType31    int       `json:"src_type31" parquet:"src_type31"`
	SrcType32    int       `json:"src_type32" parquet:"src_type32"`
	Fold         int64     `json:"fold" parquet:"fold"`
	// Fields for future ISO camt.053 mapping
	AcctId          string `json:"acct_id,omitempty"`
	CdtDbtIndicator string `json:"cdt_dbt_indicator,omitempty"`
	ISOCurrencyCode string `json:"iso_currency_code,omitempty"`
}

type TransactionReader interface {
	Next() (*Transaction, error)
	Close() error
}

type parquetTransactionReader struct {
	files   []string
	current int
	rows    []Transaction
	rowPos  int
}

func NewTransactionReader(dataPath string) (TransactionReader, error) {
	files, err := filepath.Glob(dataPath)
	if err != nil {
		return nil, fmt.Errorf("failed to glob path: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no files found matching pattern: %s", dataPath)
	}

	return &parquetTransactionReader{
		files:   files,
		current: 0,
	}, nil
}

func produceTransactions(dataPath string) <-chan *Transaction {
	c := make(chan *Transaction, 1000)

	go func() {
		defer close(c)
		reader, err := NewTransactionReader(dataPath)
		if err != nil {
			panic(err)
		}
		defer reader.Close()

		for {
			tran, err := reader.Next()
			if err != nil {
				panic(err)
			}
			if tran == nil {
				break
			}
			c <- tran
		}

	}()

	return c
}

func (r *parquetTransactionReader) Next() (*Transaction, error) {
	for {
		// Load current file if not loaded
		if len(r.rows) == 0 {
			if r.current >= len(r.files) {
				return nil, nil
			}

			// Read all rows from file using parquet.ReadFile
			rows, err := parquet.ReadFile[Transaction](r.files[r.current])
			if err != nil {
				return nil, fmt.Errorf("failed to read parquet file %s: %w", r.files[r.current], err)
			}

			r.rows = rows
			r.rowPos = 0
		}

		// Check if we have more rows in current file
		if r.rowPos >= len(r.rows) {
			// Move to next file
			r.rows = nil
			r.rowPos = 0
			r.current++
			continue
		}

		// Get current transaction
		transaction := r.rows[r.rowPos]
		r.rowPos++

		// Compute AcctId from ClientID hash
		// hash := sha256.Sum256([]byte(transaction.ClientID))
		// transaction.AcctId = hex.EncodeToString(hash[:])[:16] // first 16 chars as stable ID

		return &transaction, nil
	}
}

func (r *parquetTransactionReader) Close() error {
	// No resources to clean up with parquet.ReadFile approach
	return nil
}
