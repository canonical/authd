package bbolt

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// updateBucket is a generic function to update any bucket. It panics if we call it in RO transaction.
func updateBucket[K uint32 | string](bucket bucketWithName, key K, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("programming error: %v is not a valid json", err))
	}

	// TODO: switch to https://github.com/golang/go/issues/45380 if accepted.
	var k []byte
	switch v := any(key).(type) {
	case uint32:
		k = []byte(strconv.FormatUint(uint64(v), 10))
	case string:
		k = []byte(v)
	default:
		panic(fmt.Sprintf("unhandled type: %T", key))
	}

	if err = bucket.Put(k, data); err != nil {
		panic(fmt.Sprintf("programming error: Put is not executed in a RW transaction: %v", err))
	}
}
