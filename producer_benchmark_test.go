package main

import (
	"runtime"
	"testing"
)

var readerBatchAssemblyBenchmarkSink struct {
	emitted     []Transaction
	accumulator []Transaction
}

func BenchmarkReaderBatchAssembly(b *testing.B) {
	cases := []struct {
		name      string
		batchSize int
		readSizes []int
	}{
		{
			name:      "default_full",
			batchSize: 1_000,
			readSizes: []int{1_000},
		},
		{
			name:      "default_residual_1",
			batchSize: 1_000,
			readSizes: []int{999, 1},
		},
		{
			name:      "default_residual_1_after_cross",
			batchSize: 1_000,
			readSizes: []int{1, 1_000},
		},
		{
			name:      "default_half_residual",
			batchSize: 1_000,
			readSizes: []int{500, 1_000},
		},
		{
			name:      "default_near_full_residual",
			batchSize: 1_000,
			readSizes: []int{999, 1_000},
		},
		{
			name:      "max_full",
			batchSize: 100_000,
			readSizes: []int{100_000},
		},
		{
			name:      "max_near_full_residual",
			batchSize: 100_000,
			readSizes: []int{99_999, 100_000},
		},
	}

	for _, benchmarkCase := range cases {
		b.Run(benchmarkCase.name, func(b *testing.B) {
			rows := make([]Transaction, benchmarkCase.batchSize)
			for index := range rows {
				rows[index].ClientID = "benchmark-client"
			}

			b.ResetTimer()
			for b.Loop() {
				benchmarkReaderBatchAssembly(
					benchmarkCase.batchSize,
					benchmarkCase.readSizes,
					rows,
				)
			}
		})
	}
}

func benchmarkReaderBatchAssembly(batchSize int, readSizes []int, rows []Transaction) {
	accumulator := make([]Transaction, 0, batchSize)

	for _, readSize := range readSizes {
		accumulator = append(accumulator, rows[:readSize]...)
		if len(accumulator) >= batchSize {
			emitted := accumulator[:batchSize]
			accumulator = append(make([]Transaction, 0, batchSize), accumulator[batchSize:]...)
			readerBatchAssemblyBenchmarkSink.emitted = emitted
			readerBatchAssemblyBenchmarkSink.accumulator = accumulator
		}
	}

	runtime.KeepAlive(readerBatchAssemblyBenchmarkSink)
}
