package ipc

import "encoding/json"

type Serializer interface {
	ContentType() string
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

type JSONSerializer struct{}

func (JSONSerializer) ContentType() string { return "application/json" }

func (JSONSerializer) Marshal(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	if b, ok := v.([]byte); ok {
		return b, nil
	}
	if b, ok := v.(json.RawMessage); ok {
		return b, nil
	}
	return json.Marshal(v)
}

func (JSONSerializer) Unmarshal(data []byte, v any) error {
	if len(data) == 0 || v == nil {
		return nil
	}
	if b, ok := v.(*[]byte); ok {
		*b = append((*b)[:0], data...)
		return nil
	}
	if raw, ok := v.(*json.RawMessage); ok {
		*raw = append((*raw)[:0], data...)
		return nil
	}
	return json.Unmarshal(data, v)
}

func DefaultSerializer() Serializer { return JSONSerializer{} }
