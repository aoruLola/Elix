package codec

import (
	"encoding/json"

	"google.golang.org/grpc/encoding"
)

const Name = "json"

type JSONCodec struct{}

func (JSONCodec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (JSONCodec) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func (JSONCodec) Name() string {
	return Name
}

func Register() {
	encoding.RegisterCodec(JSONCodec{})
}
