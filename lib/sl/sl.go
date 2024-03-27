package sl

import (
	"fmt"
	"log/slog"
)

func Err(err error) slog.Attr {
	return slog.Attr{
		Key:   "error",
		Value: slog.StringValue(err.Error()),
	}
}

// Secret returns a string with the first 5 characters of the input string
// used to hide sensitive information in logs
func Secret(some string) slog.Attr {
	r := "***"
	if len(some) > 5 {
		r = fmt.Sprintf("%s***", some[0:5])
	}
	if some == "" {
		r = "?"
	}
	return slog.Attr{
		Key:   "secret",
		Value: slog.StringValue(r),
	}
}

func Module(mod string) slog.Attr {
	return slog.Attr{
		Key:   "mod",
		Value: slog.StringValue(mod),
	}
}
