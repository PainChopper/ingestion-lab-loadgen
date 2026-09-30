package main

import "time"

type Transaction struct {
	ClientID     string    `json:"client_id" parquet:"client_id"`
	EventTime    time.Time `json:"event_time" parquet:"event_time"`
	Amount       float32   `json:"amount" parquet:"amount"`
	EventType    int32     `json:"event_type" parquet:"event_type"`
	EventSubtype int32     `json:"event_subtype" parquet:"event_subtype"`
	Currency     int32     `json:"currency" parquet:"currency"`
	SrcType11    int32     `json:"src_type11" parquet:"src_type11"`
	SrcType12    int32     `json:"src_type12" parquet:"src_type12"`
	DstType11    int32     `json:"dst_type11" parquet:"dst_type11"`
	DstType12    int32     `json:"dst_type12" parquet:"dst_type12"`
	SrcType21    int32     `json:"src_type21" parquet:"src_type21"`
	SrcType22    int32     `json:"src_type22" parquet:"src_type22"`
	SrcType31    int32     `json:"src_type31" parquet:"src_type31"`
	SrcType32    int32     `json:"src_type32" parquet:"src_type32"`
	Fold         int32     `json:"fold" parquet:"fold"`
}
