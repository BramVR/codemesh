package presentation

import (
	"encoding/json"
	"io"

	"github.com/BramVR/codemesh/internal/commandresult"
)

func RenderJSON[T any](w io.Writer, result commandresult.Result[T]) error {
	return json.NewEncoder(w).Encode(result)
}

func RenderHuman[T any](w io.Writer, result commandresult.Result[T], render func(io.Writer, T) error) error {
	return render(w, result.Payload)
}
