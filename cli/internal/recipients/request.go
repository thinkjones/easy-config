package recipients

import (
	"bytes"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// RequestsDir is the folder, inside the store's .ezc, that holds join requests.
const RequestsDir = "requests"

// Request is a join request: who asks to become a recipient, with what key.
type Request struct {
	Name      string `yaml:"name"`
	Key       string `yaml:"key"`
	Kind      string `yaml:"kind"`
	Owner     string `yaml:"owner,omitempty"`
	Host      string `yaml:"host"`
	Requested string `yaml:"requested"`
}

// RequestPath is the request's path inside the .ezc folder.
func RequestPath(name string) string { return RequestsDir + "/" + name + ".yaml" }

// ParseRequest reads a request that was filed under name. Whoever can push to the store wrote it, so it is
// held to the rules: the name field is the name, the name and key are valid, the kind is user or machine.
func ParseRequest(name string, data []byte, valid func(key string) error) (Request, error) {
	var q Request
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&q); err != nil {
		return Request{}, errors.New("not a valid join request")
	}
	if q.Name != name {
		return Request{}, errors.New("the name inside the request differs from its file name")
	}
	if !ValidName(q.Name) {
		return Request{}, errors.New("not a valid recipient name")
	}
	if err := valid(q.Key); err != nil {
		return Request{}, fmt.Errorf("the key is not valid: %w", err)
	}
	switch q.Kind {
	case KindUser:
	case KindMachine:
		if q.Owner == "" {
			return Request{}, errors.New("a machine request names no owner")
		}
	default:
		return Request{}, errors.New("kind must be user or machine")
	}
	return q, nil
}

// Marshal is the request as the file holds it.
func (q Request) Marshal() []byte {
	data, _ := yaml.Marshal(q) // a struct of strings always encodes
	return data
}

// Printable makes text from a remote safe for a terminal: every character outside printable ASCII becomes
// ?, and it is cut at 80 characters.
func Printable(s string) string {
	out := make([]rune, 0, len(s))
	for _, c := range s {
		if len(out) == 80 {
			break
		}
		if c < 0x20 || c > 0x7e {
			c = '?'
		}
		out = append(out, c)
	}
	return string(out)
}
