package trackhint

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

const opTracksUpdate = "tracks.update"

type trackNotFoundError struct {
	track string
	cause error
}

func (e *trackNotFoundError) Error() string {
	return fmt.Sprintf("track %q does not exist: create it first with `gplay tracks create %s`, then re-run (gplay never auto-creates a track as a side effect of an upload/promote): %v", e.track, e.track, e.cause)
}

func (e *trackNotFoundError) Unwrap() error { return e.cause }

func Classify(track string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		return err
	}
	var destErr *orchestrator.DestinationReadError
	if apiErr.Operation == opTracksUpdate || errors.As(err, &destErr) {
		return &trackNotFoundError{track: track, cause: err}
	}
	return err
}
