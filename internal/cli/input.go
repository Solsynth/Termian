package cli

import (
	"fmt"
	"io"
	"os"
	"time"
)

// readSource reads a --file value, with "-" meaning stdin.
func (a *app) readSource(path string) (string, error) {
	if path == "-" {
		data, err := io.ReadAll(a.stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(data), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), nil
}

// newClientMessageID builds the idempotency key of one send.
func newClientMessageID() string {
	return fmt.Sprintf("termian-%d", time.Now().UnixNano())
}
