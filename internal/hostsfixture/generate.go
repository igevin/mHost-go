package hostsfixture

import (
	"bufio"
	"fmt"
	"io"
)

func Generate(output io.Writer, lines int) error {
	if lines <= 0 {
		return fmt.Errorf("lines must be positive, got %d", lines)
	}
	writer := bufio.NewWriter(output)
	for index := range lines {
		if index%10000 == 0 {
			if _, err := fmt.Fprintf(writer, "# synthetic hosts block %d\n", index/10000); err != nil {
				return fmt.Errorf("write comment: %w", err)
			}
			continue
		}
		if _, err := fmt.Fprintf(writer, "192.0.2.%d host-%06d.phase0.test\n", index%254+1, index); err != nil {
			return fmt.Errorf("write host %d: %w", index, err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush hosts fixture: %w", err)
	}
	return nil
}
