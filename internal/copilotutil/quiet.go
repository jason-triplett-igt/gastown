package copilotutil

import (
	"os"
	"sync"

	copilot "github.com/github/copilot-sdk/go"
)

var stdoutSilenceMu sync.Mutex

func StopClientQuietly(client *copilot.Client) error {
	if client == nil {
		return nil
	}
	return silenceStdout(func() error {
		return client.Stop()
	})
}

func ForceStopClientQuietly(client *copilot.Client) {
	if client == nil {
		return
	}
	_ = silenceStdout(func() error {
		client.ForceStop()
		return nil
	})
}

func silenceStdout(fn func() error) error {
	stdoutSilenceMu.Lock()
	defer stdoutSilenceMu.Unlock()

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return fn()
	}
	defer devNull.Close()

	oldStdout := os.Stdout
	os.Stdout = devNull
	defer func() {
		os.Stdout = oldStdout
	}()

	return fn()
}
