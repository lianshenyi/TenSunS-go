package cryptox

import (
	"bytes"
	"encoding/json"
)

// marshalCompact mirrors Python's json.dumps(..., ensure_ascii=False,
// separators=(",", ":")). Encoding/json otherwise escapes <, > and &.
func marshalCompact(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
