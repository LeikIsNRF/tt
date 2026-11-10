package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// object is a JSON object that keeps key order and leaves values untouched,
// so rewriting settings.json only changes the parts tt owns.
type object []member

type member struct {
	Key   string
	Value json.RawMessage
}

func parseObject(data []byte) (object, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("expected a JSON object")
	}
	var o object
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o = append(o, member{Key: tok.(string), Value: v})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return o, nil
}

func (o object) get(k string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.Key == k {
			return m.Value, true
		}
	}
	return nil, false
}

func (o *object) set(k string, v json.RawMessage) {
	for i, m := range *o {
		if m.Key == k {
			(*o)[i].Value = v
			return
		}
	}
	*o = append(*o, member{Key: k, Value: v})
}

func (o *object) del(k string) {
	for i, m := range *o {
		if m.Key == k {
			*o = append((*o)[:i], (*o)[i+1:]...)
			return
		}
	}
}

// encode writes compact JSON without HTML escaping (json.Marshal would turn
// "&&" in hook commands into &&).
func (o object) encode() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(encodeString(m.Key))
		b.WriteByte(':')
		b.Write(m.Value)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func encodeString(s string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return bytes.TrimRight(b.Bytes(), "\n")
}

func encodeArray(items []json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, it := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(it)
	}
	b.WriteByte(']')
	return b.Bytes()
}

// isTTHook reports whether a hook entry is one tt installed: its command
// runs a binary named tt with "hook" as the first argument.
func isTTHook(entry json.RawMessage) bool {
	var h struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(entry, &h) != nil {
		return false
	}
	_, ok := hookBinary(h.Command)
	return ok
}

// hookBinary splits "<bin> hook" (bin optionally single- or double-quoted)
// and reports whether it is a tt hook command.
func hookBinary(command string) (string, bool) {
	command = strings.TrimSpace(command)
	var bin, rest string
	if q := command[:min(1, len(command))]; q == "'" || q == `"` {
		end := strings.Index(command[1:], q)
		if end < 0 {
			return "", false
		}
		bin, rest = command[1:end+1], command[end+2:]
	} else {
		bin, rest, _ = strings.Cut(command, " ")
	}
	args := strings.Fields(rest)
	return bin, filepath.Base(bin) == "tt" && len(args) > 0 && args[0] == "hook"
}

// rewrite strips every tt hook entry from settings and, if command is
// non-empty, adds one for each event. Other hooks and keys are untouched.
func rewrite(settings []byte, events []string, command string) ([]byte, error) {
	if len(bytes.TrimSpace(settings)) == 0 {
		settings = []byte("{}")
	}
	top, err := parseObject(settings)
	if err != nil {
		return nil, fmt.Errorf("settings.json: %w", err)
	}
	hooks := object{}
	if raw, ok := top.get("hooks"); ok {
		if hooks, err = parseObject(raw); err != nil {
			return nil, fmt.Errorf("settings.json hooks: %w", err)
		}
	}
	removed := false

	for i := 0; i < len(hooks); i++ {
		groups, changed, err := stripGroups(hooks[i].Value)
		if err != nil {
			return nil, fmt.Errorf("hooks.%s: %w", hooks[i].Key, err)
		}
		if !changed {
			continue
		}
		removed = true
		if len(groups) == 0 {
			hooks.del(hooks[i].Key)
			i--
			continue
		}
		hooks[i].Value = encodeArray(groups)
	}

	if command != "" {
		entry := object{
			{"type", encodeString("command")},
			{"command", encodeString(command)},
			{"timeout", json.RawMessage("5")},
		}
		group := object{{"hooks", encodeArray([]json.RawMessage{entry.encode()})}}.encode()
		for _, ev := range events {
			var groups []json.RawMessage
			if raw, ok := hooks.get(ev); ok {
				if err := json.Unmarshal(raw, &groups); err != nil {
					return nil, fmt.Errorf("hooks.%s: %w", ev, err)
				}
			}
			hooks.set(ev, encodeArray(append(groups, group)))
		}
	}

	if len(hooks) > 0 {
		top.set("hooks", hooks.encode())
	} else if removed {
		top.del("hooks") // tt was the only user; leave no empty husk behind
	}

	var out bytes.Buffer
	if err := json.Indent(&out, top.encode(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// stripGroups removes tt entries from an event's matcher groups, dropping
// groups left empty by that.
func stripGroups(raw json.RawMessage) ([]json.RawMessage, bool, error) {
	var groups []json.RawMessage
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, false, err
	}
	changed := false
	kept := groups[:0]
	for _, g := range groups {
		obj, err := parseObject(g)
		if err != nil {
			return nil, false, err
		}
		inner, ok := obj.get("hooks")
		if !ok {
			kept = append(kept, g)
			continue
		}
		var entries []json.RawMessage
		if err := json.Unmarshal(inner, &entries); err != nil {
			return nil, false, err
		}
		var rest []json.RawMessage
		for _, e := range entries {
			if !isTTHook(e) {
				rest = append(rest, e)
			}
		}
		if len(rest) == len(entries) {
			kept = append(kept, g)
			continue
		}
		changed = true
		if len(rest) == 0 {
			continue
		}
		obj.set("hooks", encodeArray(rest))
		kept = append(kept, obj.encode())
	}
	return kept, changed, nil
}

// registeredEvents lists the events that have a tt hook, with its command.
func registeredEvents(settings []byte) (map[string]string, error) {
	var s struct {
		Hooks map[string][]struct {
			Hooks []json.RawMessage `json:"hooks"`
		} `json:"hooks"`
	}
	if len(bytes.TrimSpace(settings)) == 0 {
		return map[string]string{}, nil
	}
	if err := json.Unmarshal(settings, &s); err != nil {
		return nil, err
	}
	found := map[string]string{}
	for ev, groups := range s.Hooks {
		for _, g := range groups {
			for _, e := range g.Hooks {
				if isTTHook(e) {
					var h struct{ Command string }
					_ = json.Unmarshal(e, &h)
					found[ev] = h.Command
				}
			}
		}
	}
	return found, nil
}
