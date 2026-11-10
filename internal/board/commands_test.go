package board

import "testing"

// A key or name bound twice would leave one command unreachable.
func TestCommandsUnique(t *testing.T) {
	keys, names := map[string]string{}, map[string]string{}
	for _, c := range commands {
		if c.key != "" {
			if other, ok := keys[c.key]; ok {
				t.Errorf("key %q runs both %s and %s", c.key, other, c.name)
			}
			keys[c.key] = c.name
		}
		for _, n := range []string{c.name, c.alias} {
			if other, ok := names[n]; ok && n != "" {
				t.Errorf(":%s names both %s and %s", n, other, c.name)
			}
			names[n] = c.name
		}
	}
}
