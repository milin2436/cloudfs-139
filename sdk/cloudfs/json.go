package cloudfs

import "encoding/json"

// marshalValue and unmarshalValue are thin wrappers around encoding/json.
//
// They exist for one reason: JSON implements MarshalJSON, so a plain
// json.Marshal(j) inside that method would re-enter JSON's method set and
// recurse until the stack overflows. marshalValue receives a plain map, so
// encoding/json never sees a JSON value again.
func marshalValue(v any) ([]byte, error) {
	return json.Marshal(v)
}

func unmarshalValue(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
