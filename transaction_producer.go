package main

import "fmt"

func produceTransactions(dataPath string) (<-chan *Transaction, error) {
	reader, err := NewParquetTransactionReader(dataPath)
	if err != nil {
		return nil, err
	}
	c := make(chan *Transaction, 100_000)
	go func() {
		defer close(c)
		for {
			tran, err := reader.Next()
			if err != nil {
				panic(fmt.Sprintf("produceTransactions failed: %v", err))
			}
			if tran == nil {
				break
			}
			c <- tran
		}
	}()
	return c, nil
}
