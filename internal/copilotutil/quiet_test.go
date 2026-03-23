package copilotutil

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"testing"
)

func TestSilenceStdoutSuppressesOutputAndRestoresStdout(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	defer r.Close()

	oldStdout := os.Stdout
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	if err := silenceStdout(func() error {
		_, _ = fmt.Fprint(os.Stdout, "hidden")
		return nil
	}); err != nil {
		t.Fatalf("silenceStdout() error = %v", err)
	}
	_, _ = fmt.Fprint(os.Stdout, "visible")
	_ = w.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("io.Copy() error = %v", err)
	}
	if got := buf.String(); got != "visible" {
		t.Fatalf("captured stdout = %q, want visible only", got)
	}
}
