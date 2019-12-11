// Copyright 2019 Grabtaxi Holdings PTE LTE (GRAB), All rights reserved.
// Use of this source code is governed by an MIT-style license that can be found in the LICENSE file

package block

import (
	"encoding/json"
	"strconv"

	"github.com/grab/talaria/internal/encoding/orc"
	"github.com/grab/talaria/internal/encoding/typeof"
	"github.com/grab/talaria/internal/presto"
	orctype "github.com/scritchley/orc"
)

// FromOrcBy decodes a set of blocks from an orc file and repartitions
// it by the specified partition key.
func FromOrcBy(payload []byte, partitionBy string) ([]Block, error) {
	const max = 10000
	iter, err := orc.FromBuffer(payload)
	if err != nil {
		return nil, err
	}

	// Find the partition index
	schema := iter.Schema()
	cols := schema.Columns()
	partitionIdx, ok := findString(cols, partitionBy)
	if !ok {
		return nil, errPartitionNotFound
	}

	// The resulting set of blocks, repartitioned and chunked
	blocks := make([]Block, 0, 128)

	// Create presto columns and iterate
	result, count := make(map[string]presto.NamedColumns, 16), 0
	_, _ = iter.Range(func(rowIdx int, row []interface{}) bool {
		if count%max == 0 {
			pending, err := makeBlocks(result)
			if err != nil {
				return true
			}

			blocks = append(blocks, pending...)
			result = make(map[string]presto.NamedColumns, 16)
		}

		// Get the partition value, must be a string
		partition, ok := convertToString(row[partitionIdx])
		if !ok {
			return true
		}

		// Get the block for that partition
		columns, exists := result[partition]
		if !exists {
			columns = make(presto.NamedColumns, 16)
			result[partition] = columns
		}

		// Write the events into the block
		for i, v := range row {
			columnName := cols[i]
			columnType := schema[columnName]

			// Encode to JSON
			if columnType == typeof.JSON {
				if encoded, ok := convertToJSON(v); ok {
					v = encoded
				}
			}

			columns.Append(columnName, v, columnType)
		}

		count++
		columns.FillNulls()
		return false
	}, cols...)

	// Write the last chunk
	last, err := makeBlocks(result)
	if err != nil {
		return nil, err
	}

	blocks = append(blocks, last...)
	return blocks, nil
}

// Find the partition index
func findString(columns []string, partitionBy string) (int, bool) {
	for i, k := range columns {
		if k == partitionBy {
			return i, true
		}
	}
	return 0, false
}

// convertToJSON converts an ORC map/list/struct to JSON
func convertToJSON(v interface{}) (json.RawMessage, bool) {
	switch v.(type) {
	case orctype.Struct:
	case []orctype.MapEntry:
	case []interface{}:
	case interface{}:
	default:
		return nil, false
	}

	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}

	return json.RawMessage(b), true
}

// convertToString converst value to string because currently all the keys in Badger are stored in the form of string before hashing to the byte array
func convertToString(value interface{}) (string, bool) {
	v, ok := value.(string)
	if ok {
		return v, true
	}
	valueInt, ok := value.(int64)
	if ok {
		return strconv.FormatInt(valueInt, 10), true
	}
	return "", false
}