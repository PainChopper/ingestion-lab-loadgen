package main

import "time"

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
