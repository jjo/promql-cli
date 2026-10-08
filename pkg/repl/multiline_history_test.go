//go:build !noprompt

package repl

import (
	"reflect"
	"testing"
	"unsafe"

	"github.com/c-bata/go-prompt"
)

// TestClearPromptHistory guards the reflect/unsafe access to go-prompt's
// unexported history (v0.2.6): it must keep working, or fail loudly here, on
// dependency upgrades. prompt.New needs a TTY, so build the Prompt by hand.
func TestClearPromptHistory(t *testing.T) {
	p := &prompt.Prompt{}
	f := reflect.ValueOf(p).Elem().FieldByName("history")
	if !f.IsValid() {
		t.Fatal("go-prompt Prompt.history field not found; update clearPromptHistory")
	}
	h := prompt.NewHistory()
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Set(reflect.ValueOf(h))

	h.Add("fragment(")
	if got, _ := h.Older(prompt.NewBuffer()); got.Text() != "fragment(" {
		t.Fatalf("precondition: expected seeded history entry, got %q", got.Text())
	}
	clearPromptHistory(p)
	if got, _ := h.Older(prompt.NewBuffer()); got.Text() == "fragment(" {
		t.Fatal("history not cleared")
	}
	clearPromptHistory(nil)              // must not panic
	clearPromptHistory(&prompt.Prompt{}) // nil history: must not panic
}
