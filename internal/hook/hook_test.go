package hook

import (
	"testing"

	"github.com/LeikIsNRF/tt/internal/store"
)

func TestMap(t *testing.T) {
	cases := []struct {
		name string
		p    Payload
		want Transition
	}{
		{"start", Payload{Event: "SessionStart", Source: "startup"}, Transition{To: store.Idle}},
		{"compact keeps state", Payload{Event: "SessionStart", Source: "compact"}, Transition{}},
		{"prompt", Payload{Event: "UserPromptSubmit"}, Transition{To: store.Working}},
		{"permission", Payload{Event: "Notification", NotificationType: "permission_prompt"}, Transition{To: store.NeedsYou}},
		{"elicitation", Payload{Event: "Notification", NotificationType: "elicitation_dialog"}, Transition{To: store.NeedsYou}},
		{"idle reminder", Payload{Event: "Notification", NotificationType: "idle_prompt"}, Transition{To: store.YourTurn}},
		{"auth ignored", Payload{Event: "Notification", NotificationType: "auth_success"}, Transition{}},
		{"untyped permission", Payload{Event: "Notification", Message: "Claude needs your permission to use Bash"}, Transition{To: store.NeedsYou}},
		{"untyped idle", Payload{Event: "Notification", Message: "Claude is waiting for your input"}, Transition{To: store.YourTurn}},
		{"tool ran", Payload{Event: "PostToolUse"}, Transition{To: store.Working, OnlyFrom: store.NeedsYou}},
		{"stop", Payload{Event: "Stop"}, Transition{To: store.YourTurn}},
		{"end", Payload{Event: "SessionEnd"}, Transition{To: store.Ended}},
	}
	for _, c := range cases {
		if got := Map(c.p); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}
