package main

type TransactionIterator interface {
	Next() (*Transaction, error)
}
