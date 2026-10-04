package fabricchange

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const MaxInputBytes = 16 << 20

// Decode accepts exactly one JSON document, rejecting unknown fields, duplicate
// object keys, excessive nesting, and oversized documents before typed decoding.
func Decode(r io.Reader) (Input, error) {
	var in Input
	b, err := io.ReadAll(io.LimitReader(r, MaxInputBytes+1))
	if err != nil {
		return in, err
	}
	if len(b) > MaxInputBytes {
		return in, fmt.Errorf("input exceeds %d bytes", MaxInputBytes)
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := checkJSON(d, 0); err != nil {
		return in, err
	}
	if _, err := d.Token(); err != io.EOF {
		return in, fmt.Errorf("expected exactly one JSON document")
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		return in, fmt.Errorf("decode: %w", err)
	}
	return in, nil
}

func checkJSON(d *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting exceeds 64 levels")
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("null is not accepted; omit optional fields instead")
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			t, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := t.(string)
			if !ok {
				return fmt.Errorf("expected object key")
			}
			// encoding/json otherwise accepts case-folded aliases (including
			// some Unicode variants). The input contract uses ASCII snake_case.
			for _, c := range key {
				if (c < 'a' || c > 'z') && c != '_' {
					return fmt.Errorf("noncanonical JSON field %q; field names use lowercase ASCII snake_case", key)
				}
			}
			if keys[key] {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			keys[key] = true
			if err := checkJSON(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := checkJSON(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
	_, err = d.Token()
	return err
}
