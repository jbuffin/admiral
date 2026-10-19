package config

import (
	"bytes"
	"encoding/json"
	"slices"
)

// Object is a JSON object that keeps its keys in the order of the text, so a
// command can change one member and leave the rest as they were.
type Object struct {
	keys []string
	vals map[string]json.RawMessage
}

// NewObject is an empty object.
func NewObject() *Object { return &Object{vals: map[string]json.RawMessage{}} }

// ParseObject reads a JSON object, refusing duplicate keys.
func ParseObject(raw []byte) (*Object, error) {
	ms, err := members("", raw)
	if err != nil {
		return nil, err
	}
	o := NewObject()
	for _, m := range ms {
		o.keys = append(o.keys, m.key)
		o.vals[m.key] = m.raw
	}
	return o, nil
}

// Keys are the keys in order.
func (o *Object) Keys() []string { return slices.Clone(o.keys) }

// Get returns the member's JSON.
func (o *Object) Get(key string) (json.RawMessage, bool) {
	v, ok := o.vals[key]
	return v, ok
}

// Set replaces a member in place, or appends it.
func (o *Object) Set(key string, raw json.RawMessage) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = raw
}

// SetValue marshals v and sets it.
func (o *Object) SetValue(key string, v any) {
	o.Set(key, mustJSON(v))
}

// Delete removes a member.
func (o *Object) Delete(key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	o.keys = slices.DeleteFunc(o.keys, func(k string) bool { return k == key })
}

// Len is the number of members.
func (o *Object) Len() int { return len(o.keys) }

// JSON is the object, compact, in key order.
func (o *Object) JSON() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(mustJSON(k))
		b.WriteByte(':')
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes()
}

// mustJSON marshals without escaping <, > and &, which a path or a command
// line may hold.
func mustJSON(v any) json.RawMessage {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return bytes.TrimSpace(b.Bytes())
}
