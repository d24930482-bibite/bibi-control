package ipc

import "encoding/json"

type Codec interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

type JSONCodec struct{}

func (JSONCodec) Marshal(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw, nil
	}
	if b, ok := v.([]byte); ok {
		return b, nil
	}
	return json.Marshal(v)
}

func (JSONCodec) Unmarshal(data []byte, v any) error {
	if len(data) == 0 || v == nil {
		return nil
	}
	if raw, ok := v.(*json.RawMessage); ok {
		*raw = append((*raw)[:0], data...)
		return nil
	}
	if b, ok := v.(*[]byte); ok {
		*b = append((*b)[:0], data...)
		return nil
	}
	return json.Unmarshal(data, v)
}

func DefaultCodec() Codec { return JSONCodec{} }
