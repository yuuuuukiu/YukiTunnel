package cli

import (
	"io"
	"os"
)

func stdout() io.Writer { return os.Stdout }
