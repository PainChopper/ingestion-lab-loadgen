package main

import (
	"time"
	//  "github.com/segmentio/parquet-go"
)

type TrxEvent struct {
	ClientID     string    `json:"client_id"`
	EventTime    time.Time `json:"event_time"`
	Amount       string    `json:"amount"` // string to preserve precision
	EventType    int       `json:"event_type"`
	EventSubtype int       `json:"event_subtype"`
	Currency     int       `json:"currency"`
	SrcType11    int       `json:"src_type11"`
	SrcType12    int       `json:"src_type12"`
	DstType11    int       `json:"dst_type11"`
	DstType12    int       `json:"dst_type12"`
	SrcType21    int       `json:"src_type21"`
	SrcType22    int       `json:"src_type22"`
	SrcType31    int       `json:"src_type31"`
	SrcType32    int       `json:"src_type32"`
	Fold         int64     `json:"fold"`
	// Fields for future ISO camt.053 mapping
	AcctId          string `json:"acct_id,omitempty"`
	CdtDbtIndicator string `json:"cdt_dbt_indicator,omitempty"`
	ISOCurrencyCode string `json:"iso_currency_code,omitempty"`
}

type ParquetReader interface {
	Next() (*TrxEvent, error)
	Close() error
	EOF() error
}
